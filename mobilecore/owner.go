package mobilecore

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// The owner is the root of trust of the household (RFC-005, Trust model).
// Their keys — Ed25519 to sign, X25519 to seal — are generated here, in Go,
// once, and kept in the same AES-GCM vault as the box password. JS only ever
// sees the public halves, the QR payload, the six digits and signed blobs:
// nothing exported from this file returns private material.
//
// What the app does with them (issue #43): it shows a QR carrying the hub's
// pairing code and the owner's fingerprint, compares the six digits with the
// relay's screen, and signs the relay into a new trust bundle. The same
// identity is what signs commands and the relay policy later on.

// ownerStateID is the vault id of the owner's identity. It is not a router
// id, so it cannot collide with one (the PC relay uses "hub-agent").
const ownerStateID = "owner-identity"

// ownerState is what the vault keeps for the owner.
type ownerState struct {
	// Identity is the serialised private identity (trust.Identity).
	Identity []byte `json:"identity"`
	// BundleVersion is the last trust bundle version this phone signed. The
	// next one is always above it, whatever the hub answers, so a hub that
	// replayed or dropped a bundle can never make the owner sign a version a
	// relay already holds.
	BundleVersion uint64 `json:"bundleVersion"`
	// CredentialVersion is the version the box password was last sealed
	// at, and CredentialDigest the SHA-256 of that login: a new password
	// moves the version, the same one does not (credentials.go).
	CredentialVersion uint64 `json:"credentialVersion,omitempty"`
	CredentialDigest  []byte `json:"credentialDigest,omitempty"`
}

// errOwnerVault says the owner's keys could not be read or kept.
var errOwnerVault = errors.New("mobilecore: owner keys")

// ownerKeysJSON is the JS view of the owner's keys: public halves only.
type ownerKeysJSON struct {
	KeyID   string `json:"keyId"`
	Ed25519 []byte `json:"ed25519"`
	X25519  []byte `json:"x25519"`
}

func ownerKeysView(id *trust.Identity) ownerKeysJSON {
	pub := id.Public()
	return ownerKeysJSON{KeyID: pub.KeyID(), Ed25519: pub.Ed25519, X25519: pub.X25519}
}

// loadOwner returns the owner's state, generating the identity the first
// time. Callers hold c.ownerMu.
func (c *Core) loadOwner() (*ownerState, *trust.Identity, error) {
	if c.owner != nil {
		return c.owner, c.ownerID, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	blob, ok, err := c.vault.LoadSecret(ctx, ownerStateID)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", errOwnerVault, err)
	}
	var st ownerState
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
		st = ownerState{Identity: id.Marshal()}
		if err := c.saveOwner(ctx, &st); err != nil {
			return nil, nil, err
		}
		c.log.Infof("owner: keys created (%s)", id.KeyID())
	}
	c.owner, c.ownerID = &st, id
	return c.owner, c.ownerID, nil
}

func (c *Core) saveOwner(ctx context.Context, st *ownerState) error {
	blob, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := c.vault.StoreSecret(ctx, ownerStateID, blob); err != nil {
		return fmt.Errorf("%w: %v", errOwnerVault, err)
	}
	return nil
}

// ownerIdentity is the owner's private identity, for the Go code that signs
// on their behalf (bundles here; the relay policy of #36 and the commands
// later). It never leaves the package.
func (c *Core) ownerIdentity() (*trust.Identity, error) {
	c.ownerMu.Lock()
	defer c.ownerMu.Unlock()
	_, id, err := c.loadOwner()
	return id, err
}

// OwnerKeys returns the owner's public keys as JSON
// {"keyId", "ed25519", "x25519"} (keys in standard base64), creating them on
// the first call. The private halves stay in the vault.
func (c *Core) OwnerKeys() (string, error) {
	id, err := c.ownerIdentity()
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(ownerKeysView(id))
	return string(b), err
}

// PairingInvite returns what the owner's QR shows for a pairing code the hub
// issued: the code and the owner's key fingerprint, which the relay pins.
func (c *Core) PairingInvite(code string) (string, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", errors.New("mobilecore: empty pairing code")
	}
	id, err := c.ownerIdentity()
	if err != nil {
		return "", err
	}
	return trust.Invite{Code: code, OwnerFingerprint: id.KeyID()}.String(), nil
}

// PairingCode returns the six digits the owner's screen shows for a relay,
// from the relay's public keys as the hub lists them (standard base64). The
// relay derives its own from the owner's keys it pinned; a hub that swapped
// either key makes the two differ.
func (c *Core) PairingCode(relayEd25519, relayX25519 string) (string, error) {
	relay, err := parsePublicKeys(relayEd25519, relayX25519)
	if err != nil {
		return "", err
	}
	id, err := c.ownerIdentity()
	if err != nil {
		return "", err
	}
	return trust.PairingCode(id.Public(), relay), nil
}

func parsePublicKeys(ed, x string) (trust.PublicKeys, error) {
	edb, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ed))
	if err != nil {
		return trust.PublicKeys{}, fmt.Errorf("mobilecore: relay signing key: %w", err)
	}
	xb, err := base64.StdEncoding.DecodeString(strings.TrimSpace(x))
	if err != nil {
		return trust.PublicKeys{}, fmt.Errorf("mobilecore: relay sealing key: %w", err)
	}
	keys := trust.PublicKeys{Ed25519: edb, X25519: xb}
	if err := checkKeys(keys); err != nil {
		return trust.PublicKeys{}, err
	}
	return keys, nil
}

func checkKeys(k trust.PublicKeys) error {
	if k.KeyID() == "" || len(k.X25519) != 32 {
		return fmt.Errorf("%w: relay public keys", trust.ErrMalformed)
	}
	return nil
}

// signedJSON is the wire shape of a signed message, as the hub's
// GET/PUT trust-bundle carry it.
type signedJSON struct {
	Payload []byte `json:"payload"`
	Sig     []byte `json:"sig"`
	Version uint64 `json:"version,omitempty"`
}

// bundleChange is what the app asks to change in the bundle.
type bundleChange struct {
	// Relay is signed in (or replaced, by agent id) when present.
	Relay *struct {
		AgentID string `json:"agentId"`
		Kind    string `json:"kind"`
		Label   string `json:"label"`
		Ed25519 []byte `json:"ed25519"`
		X25519  []byte `json:"x25519"`
	} `json:"relay"`
	// RouterPin is the box certificate's SHA-256, when the app knows it.
	RouterPin string   `json:"routerPin"`
	SSIDs     []string `json:"ssids"`
	// Delegate grants a member's key view or control for 30 days (#47).
	Delegate *struct {
		Ed25519 []byte `json:"ed25519"`
		X25519  []byte `json:"x25519"`
		Scope   string `json:"scope"`
		Label   string `json:"label"`
	} `json:"delegate"`
	// Revoke is a member key id whose delegation ends now.
	Revoke string `json:"revoke"`
	// OwnerOnly, when present, keeps the box to the owner (true) or gives
	// the members their delegations back (false).
	OwnerOnly *bool `json:"ownerOnly"`
	// CoOwner signs a member's key in as a co-owner, who may take the
	// household over if this phone is lost (#48, recovery.go).
	CoOwner *struct {
		Ed25519 []byte `json:"ed25519"`
		X25519  []byte `json:"x25519"`
		Label   string `json:"label"`
	} `json:"coOwner"`
	// RemoveCoOwner is a co-owner key id to drop.
	RemoveCoOwner string `json:"removeCoOwner"`
}

// SignTrustBundle signs the household's next trust bundle and returns it as
// {"payload", "sig", "version"}, the body of PUT …/trust-bundle.
//
// current is the hub's GET …/trust-bundle answer, or "" when it has none.
// It is built on only when this owner key signed it for this household;
// anything else (a bundle the hub made up, one from a key this phone no
// longer has) is set aside, and the new bundle starts from the owner alone.
// change is {"relay": {agentId, kind, label, ed25519, x25519}, "routerPin",
// "ssids", "delegate": {ed25519, x25519, scope, label}, "revoke": keyId,
// "ownerOnly": bool, "coOwner": {ed25519, x25519, label}, "removeCoOwner":
// keyId}, every part optional. Every bundle signed also renews
// the member delegations due within a week (RenewDelegations).
//
// The version is one above both the hub's and the last one this phone
// signed, and is kept in the vault before the bundle is returned: it only
// ever goes up.
func (c *Core) SignTrustBundle(tenantID, current, change string) (string, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "", errors.New("mobilecore: a trust bundle needs a household")
	}
	var ch bundleChange
	if strings.TrimSpace(change) != "" {
		if err := json.Unmarshal([]byte(change), &ch); err != nil {
			return "", fmt.Errorf("mobilecore: bundle change: %w", err)
		}
	}
	var relay *trust.Relay
	if ch.Relay != nil {
		r := trust.Relay{
			AgentID: strings.TrimSpace(ch.Relay.AgentID), Kind: ch.Relay.Kind, Label: ch.Relay.Label,
			Keys: trust.PublicKeys{Ed25519: ch.Relay.Ed25519, X25519: ch.Relay.X25519},
		}
		if err := checkKeys(r.Keys); err != nil || r.AgentID == "" {
			return "", fmt.Errorf("%w: relay", trust.ErrMalformed)
		}
		relay = &r
	}

	c.ownerMu.Lock()
	defer c.ownerMu.Unlock()
	st, id, err := c.loadOwner()
	if err != nil {
		return "", err
	}

	base := trust.Bundle{TenantID: tenantID}
	stored := uint64(0)
	if strings.TrimSpace(current) != "" {
		var cur signedJSON
		if err := json.Unmarshal([]byte(current), &cur); err != nil {
			return "", fmt.Errorf("mobilecore: current trust bundle: %w", err)
		}
		stored = cur.Version
		signed := trust.Signed[trust.Bundle]{Payload: cur.Payload, Sig: cur.Sig}
		if b, err := signed.Unverified(); err == nil && b.Version > stored {
			stored = b.Version
		}
		if b, err := signed.Open(trust.DomainBundle, id.Public().SigningKey()); err == nil && b.TenantID == tenantID {
			base = b
		} else {
			c.log.Warnf("owner: the stored trust bundle is not this phone's, starting a new one")
		}
	}

	now := c.gate.now()
	base.Version = max(stored, st.BundleVersion, base.Version) + 1
	base.IssuedAt = now.UTC()
	base.Owner = id.Public()
	// The slices are the stored bundle's; copy before changing them.
	base.Delegations = append([]trust.Delegation(nil), base.Delegations...)
	base.Revoked = append([]string(nil), base.Revoked...)
	if ch.Delegate != nil {
		keys := trust.PublicKeys{Ed25519: ch.Delegate.Ed25519, X25519: ch.Delegate.X25519}
		if err := base.Delegate(keys, ch.Delegate.Scope, strings.TrimSpace(ch.Delegate.Label), now); err != nil {
			return "", err
		}
	}
	if r := strings.TrimSpace(ch.Revoke); r != "" {
		base.Revoke(r)
	}
	if ch.OwnerOnly != nil {
		base.OwnerOnly = *ch.OwnerOnly
	}
	if r := strings.TrimSpace(ch.RemoveCoOwner); r != "" {
		base.RemoveCoOwner(r)
	}
	if ch.CoOwner != nil {
		keys := trust.PublicKeys{Ed25519: ch.CoOwner.Ed25519, X25519: ch.CoOwner.X25519}
		if err := base.AddCoOwner(keys, strings.TrimSpace(ch.CoOwner.Label)); err != nil {
			return "", err
		}
	}
	base.RenewDelegations(now)
	if ch.RouterPin != "" {
		base.Network.RouterPin = ch.RouterPin
	}
	if len(ch.SSIDs) > 0 {
		base.Network.SSIDs = ch.SSIDs
	}
	if relay != nil {
		kept := base.Relays[:0:0]
		for _, r := range base.Relays {
			if r.AgentID != relay.AgentID {
				kept = append(kept, r)
			}
		}
		base.Relays = append(kept, *relay)
	}

	signed, err := trust.Sign(id, trust.DomainBundle, base)
	if err != nil {
		return "", err
	}
	// Kept before it is handed out: a crash after the upload must not let
	// the next bundle reuse this version.
	next := *st
	next.BundleVersion = base.Version
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	if err := c.saveOwner(ctx, &next); err != nil {
		return "", err
	}
	*st = next
	c.log.Infof("owner: trust bundle v%d signed (%d relays, %d delegations, %d co-owners)", base.Version, len(base.Relays), len(base.Delegations), len(base.CoOwners))
	out, err := json.Marshal(signedJSON{Payload: signed.Payload, Sig: signed.Sig, Version: base.Version})
	return string(out), err
}

// DemoRelayRedeem plays the relay's part of pairing in the demo, where there
// is no hub: it reads the QR as a relay would, pins the owner's key from it
// (refusing an invite for any other key), generates relay keys and returns
// {"agentId", "name", "kind", "ed25519", "x25519", "code"}, code being the
// six digits the relay's own screen would show. Nothing is kept.
func (c *Core) DemoRelayRedeem(invite string) (string, error) {
	inv, err := trust.ParseInvite(invite)
	if err != nil {
		return "", err
	}
	id, err := c.ownerIdentity()
	if err != nil {
		return "", err
	}
	if inv.OwnerFingerprint != id.KeyID() {
		return "", fmt.Errorf("%w: the invite names another owner", trust.ErrUnknownKey)
	}
	relay, err := trust.Generate(rand.Reader)
	if err != nil {
		return "", err
	}
	pub := relay.Public()
	out, err := json.Marshal(map[string]any{
		"agentId": "demo-relay-" + pub.KeyID()[:8],
		"name":    "PC du salon (démo)",
		"kind":    "pc",
		"ed25519": pub.Ed25519,
		"x25519":  pub.X25519,
		"code":    trust.PairingCode(id.Public(), pub),
	})
	return string(out), err
}
