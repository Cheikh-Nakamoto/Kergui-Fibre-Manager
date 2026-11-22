package mobilecore

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/controller/httpapi"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// A household member's phone (issue #47, RFC-005 Trust model). Its key is
// generated here, in Go, once, and kept in the vault beside the owner's
// (a phone may own its own household and be a member of another: the two
// keys are never the same). JS sees the public halves, the six digits and
// signed blobs.
//
// The owner delegates that key view or control in the bundle the owner's
// key signs; the member's core then believes the bundle, reads the relays
// from it, checks the relay's signature over every list and result, and
// signs block / unblock with its own key — only while the delegation is
// control, not expired, not revoked, and the household not owner-only. The
// relay checks all of that again; the core refusing first is what lets the
// app say why instead of waiting for a refusal.
//
// Whose bundle to believe: the member's core pins, per household, the owner
// key of the first bundle that delegates its key, and believes only that
// key afterwards. The six digits on both screens, derived from the owner's
// key and the member's, are what catch a hub that slipped its own owner in
// before the real one.

// memberStateID is the vault id of the member's identity.
const memberStateID = "member-identity"

// Error prefixes JS reads (src/core/remote.ts), like "not_entitled".
const (
	codeOwnerOnly    = "owner_only"
	codeNotDelegated = "not_delegated"
)

// memberState is what the vault keeps for the member's phone.
type memberState struct {
	Identity []byte `json:"identity"`
	// Owners pins, per household, the owner signing key whose bundle first
	// delegated this phone's key.
	Owners map[string][]byte `json:"owners,omitempty"`
}

// loadMember returns the member's state, generating the identity the first
// time. Callers hold c.ownerMu.
func (c *Core) loadMember() (*memberState, *trust.Identity, error) {
	if c.member != nil {
		return c.member, c.memberID, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	blob, ok, err := c.vault.LoadSecret(ctx, memberStateID)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", errOwnerVault, err)
	}
	var st memberState
	var id *trust.Identity
	if ok {
		if err := json.Unmarshal(blob, &st); err != nil {
			return nil, nil, fmt.Errorf("%w: %v", errOwnerVault, err)
		}
		if id, err = trust.ParseIdentity(st.Identity); err != nil {
			return nil, nil, fmt.Errorf("%w: %v", errOwnerVault, err)
		}
	} else {
		if id, err = trust.Generate(rand.Reader); err != nil {
			return nil, nil, err
		}
		st = memberState{Identity: id.Marshal()}
		if err := c.saveMember(ctx, &st); err != nil {
			return nil, nil, err
		}
		c.log.Infof("member: keys created (%s)", id.KeyID())
	}
	c.member, c.memberID = &st, id
	return c.member, c.memberID, nil
}

func (c *Core) saveMember(ctx context.Context, st *memberState) error {
	blob, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := c.vault.StoreSecret(ctx, memberStateID, blob); err != nil {
		return fmt.Errorf("%w: %v", errOwnerVault, err)
	}
	return nil
}

// MemberKeys returns this phone's member keys as JSON {"keyId", "ed25519",
// "x25519"} (standard base64), creating them on the first call: what the
// app publishes on PUT members/me/key for the owner to delegate.
func (c *Core) MemberKeys() (string, error) {
	c.ownerMu.Lock()
	_, id, err := c.loadMember()
	c.ownerMu.Unlock()
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(ownerKeysView(id))
	return string(b), err
}

// memberView is what RemoteRelays tells a member's phone about itself.
type memberView struct {
	KeyID string `json:"keyId"`
	// State: none (no delegation yet), active, expired, revoked, or
	// other_owner (a bundle signed by another key than the one pinned).
	State     string `json:"state"`
	Scope     string `json:"scope,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
	// Control: the member may sign block / unblock right now.
	Control bool `json:"control"`
	// Code is the six digits the owner's screen shows for this key.
	Code string `json:"code,omitempty"`
}

// Member states.
const (
	memberNone       = "none"
	memberActive     = "active"
	memberExpired    = "expired"
	memberRevoked    = "revoked"
	memberOtherOwner = "other_owner"
)

// memberBundle is a bundle as the member's core judges it.
type memberBundle struct {
	bundle trust.Bundle
	view   memberView
	// delegation is set when view.State is active.
	delegation trust.Delegation
}

// membersBundle opens the hub's bundle for this phone as a member: signed by
// the owner key pinned for the household, or — the first time — by the
// owner key it names, pinned only once it delegates this phone's key.
func (c *Core) membersBundle(tenantID, bundle string) (memberBundle, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return memberBundle{}, errors.New("mobilecore: remote mode needs a household")
	}
	if strings.TrimSpace(bundle) == "" {
		return memberBundle{}, trust.ErrNoBundle
	}
	var cur signedJSON
	if err := json.Unmarshal([]byte(bundle), &cur); err != nil {
		return memberBundle{}, fmt.Errorf("%w: trust bundle: %v", trust.ErrMalformed, err)
	}
	signed := trust.Signed[trust.Bundle]{Payload: cur.Payload, Sig: cur.Sig}
	claimed, err := signed.Unverified()
	if err != nil {
		return memberBundle{}, err
	}

	c.ownerMu.Lock()
	defer c.ownerMu.Unlock()
	st, id, err := c.loadMember()
	if err != nil {
		return memberBundle{}, err
	}
	out := memberBundle{view: memberView{KeyID: id.KeyID(), State: memberNone}}
	pinned := ed25519.PublicKey(st.Owners[tenantID])
	key := pinned
	if key == nil {
		key = claimed.Owner.SigningKey()
	} else if !claimed.Owner.SigningKey().Equal(pinned) {
		// Another owner key than the one that delegated this phone: a hub
		// that made one up, or an owner who reinstalled (recovery is #48).
		out.view.State = memberOtherOwner
		return out, nil
	}
	b, err := signed.Open(trust.DomainBundle, key)
	if err != nil {
		return memberBundle{}, err
	}
	if b.TenantID != tenantID {
		return memberBundle{}, fmt.Errorf("%w: the bundle is for another household", trust.ErrTenant)
	}
	out.bundle = b
	out.view.Code = trust.PairingCode(b.Owner, id.Public())

	d, err := b.MemberDelegation(id.KeyID(), c.gate.now())
	switch {
	case errors.Is(err, trust.ErrRevoked):
		out.view.State = memberRevoked
	case errors.Is(err, trust.ErrUnknownKey):
		out.view.State = memberNone
	case errors.Is(err, trust.ErrDelegationExpired):
		out.view.State, out.view.Scope, out.view.ExpiresAt = memberExpired, d.Scope, d.ExpiresAt.UTC().Format(timeLayout)
	case err != nil:
		return memberBundle{}, err
	default:
		out.view.State, out.view.Scope, out.view.ExpiresAt = memberActive, d.Scope, d.ExpiresAt.UTC().Format(timeLayout)
		out.view.Control = d.Scope == trust.ScopeControl && !b.OwnerOnly
		out.delegation = d
	}
	if pinned == nil && out.view.State != memberNone && out.view.State != memberRevoked {
		next := *st
		next.Owners = make(map[string][]byte, len(st.Owners)+1)
		for k, v := range st.Owners {
			next.Owners[k] = v
		}
		next.Owners[tenantID] = append([]byte(nil), key...)
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		if err := c.saveMember(ctx, &next); err != nil {
			return memberBundle{}, err
		}
		*st = next
		c.log.Infof("member: owner %s pinned for the household", trust.KeyID(key))
	}
	return out, nil
}

// timeLayout is how the core writes dates to JS.
const timeLayout = "2006-01-02T15:04:05Z07:00"

// readableBundle is the bundle this phone may read the relays from: the one
// its owner key signed, or one that delegates its member key, active.
func (c *Core) readableBundle(tenantID, bundle string) (trust.Bundle, error) {
	b, err := c.ownersBundle(tenantID, bundle)
	if err == nil || !notOwners(err) {
		return b, err
	}
	m, merr := c.membersBundle(tenantID, bundle)
	if merr != nil {
		return trust.Bundle{}, merr
	}
	switch m.view.State {
	case memberActive:
		return m.bundle, nil
	case memberExpired:
		return trust.Bundle{}, trust.ErrDelegationExpired
	case memberRevoked:
		return trust.Bundle{}, trust.ErrRevoked
	default:
		return trust.Bundle{}, fmt.Errorf("%w: this phone holds no delegation in the household's bundle", trust.ErrUnknownKey)
	}
}

// notOwners reports an ownersBundle refusal that only means "not this
// phone's owner key".
func notOwners(err error) bool {
	return errors.Is(err, trust.ErrBadSignature) || errors.Is(err, trust.ErrUnsigned) || errors.Is(err, trust.ErrTenant)
}

// SignMemberCommand signs a block or an unblock of mac with this phone's
// member key, for the household whose bundle (the hub's GET trust-bundle
// answer) delegates it control, and returns what SignDeviceCommand returns.
// The paid-action gate runs first, as for the owner. A refusal's message
// starts with "owner_only" (the household is), "not_delegated" (view
// only, expired, revoked, none) or "not_entitled".
func (c *Core) SignMemberCommand(tenantID, bundle, mac, action, boxID string) (string, error) {
	feature, ok := remoteActions[action]
	if !ok {
		return "", fmt.Errorf("mobilecore: %q is not a remote device command", action)
	}
	m, err := domain.ParseMAC(mac)
	if err != nil {
		return "", fmt.Errorf("mobilecore: %w", err)
	}
	mb, err := c.membersBundle(tenantID, bundle)
	if err != nil {
		return "", fmt.Errorf("%s: %w", codeNotDelegated, err)
	}
	switch {
	case mb.view.State == memberRevoked:
		return "", fmt.Errorf("%s: %w", codeNotDelegated, trust.ErrRevoked)
	case mb.view.State == memberExpired:
		return "", fmt.Errorf("%s: %w", codeNotDelegated, trust.ErrDelegationExpired)
	case mb.view.State != memberActive:
		return "", fmt.Errorf("%s: %w", codeNotDelegated, trust.ErrUnknownKey)
	case mb.bundle.OwnerOnly:
		return "", fmt.Errorf("%s: %w", codeOwnerOnly, trust.ErrOwnerOnly)
	case mb.delegation.Scope != trust.ScopeControl:
		return "", fmt.Errorf("%s: %w: view may not %s", codeNotDelegated, trust.ErrScope, action)
	}
	if err := c.gate.checkRemote(feature, boxID); err != nil {
		if errors.Is(err, entitlement.ErrNotEntitled) {
			return "", fmt.Errorf("%s: %w", httpapi.NotEntitledCode, err)
		}
		return "", err
	}
	c.ownerMu.Lock()
	_, id, err := c.loadMember()
	c.ownerMu.Unlock()
	if err != nil {
		return "", err
	}
	signed, err := trust.NewCommand(id, mb.bundle.TenantID, http.MethodPost, "/api/devices/"+m.String()+"/"+action, nil, c.gate.now(), commandTTL)
	if err != nil {
		return "", err
	}
	cmd, err := signed.Unverified()
	if err != nil {
		return "", err
	}
	c.log.Infof("member: %s %s signed (command %s)", action, m.String(), cmd.CmdID)
	raw, err := json.Marshal(signedCommandJSON{
		Payload: signed.Payload, Sig: signed.Sig, CmdID: cmd.CmdID, MAC: m.String(), ExpiresAt: cmd.ExpiresAt,
	})
	return string(raw), err
}

// RenewDelegations is the owner's app keeping its members' delegations
// alive: when one ends within a week (trust.DelegationRenewWithin), or has
// ended, it signs the next bundle with every such one moved to 30 days from
// now and returns it, the body of PUT trust-bundle; otherwise "". Only the
// owner's phone can: current must be the bundle its key signed.
func (c *Core) RenewDelegations(tenantID, current string) (string, error) {
	b, err := c.ownersBundle(tenantID, current)
	if err != nil {
		return "", err
	}
	if b.RenewDelegations(c.gate.now()) == 0 {
		return "", nil
	}
	return c.SignTrustBundle(tenantID, current, "")
}
