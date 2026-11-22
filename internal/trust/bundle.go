package trust

import (
	"fmt"
	"strings"
	"time"
)

// Scope is what a delegated key may do.
const (
	// ScopeView reads only: no write ever reaches the box.
	ScopeView = "view"
	// ScopeControl is the full relay allow-list, minus what is owner-only.
	ScopeControl = "control"
	// ScopeHubBlock is the narrow delegation behind WhatsApp's "1 = block":
	// blocking one device, nothing else, rate-limited, never a protected one.
	ScopeHubBlock = "hub_block"
)

// Bundle is the household's trust list. The owner signs it and bumps Version
// on every change; a relay keeps the newest one it has seen and refuses to go
// back (a hub that replayed an old bundle would otherwise resurrect a revoked
// key).
type Bundle struct {
	TenantID string    `json:"tenant_id"`
	Version  uint64    `json:"version"`
	IssuedAt time.Time `json:"issued_at"`

	Owner PublicKeys `json:"owner"`
	// OwnerOnly keeps the box to the owner: no delegation may act, whatever
	// the hub lets through.
	OwnerOnly bool `json:"owner_only"`

	Network     Network      `json:"network"`
	Relays      []Relay      `json:"relays"`
	Delegations []Delegation `json:"delegations"`
	// ProtectedMACs are devices no delegated key may cut: the household's
	// own phones, the box itself, anything the owner pinned.
	ProtectedMACs []string `json:"protected_macs,omitempty"`
	// Revoked lists key ids that are refused even if they still appear
	// elsewhere in this bundle.
	Revoked []string `json:"revoked,omitempty"`
}

// Network is where the relay must be to be eligible (RFC-005, design rule 2).
type Network struct {
	SSIDs     []string `json:"ssids,omitempty"`
	BSSIDs    []string `json:"bssids,omitempty"`
	RouterPin string   `json:"router_pin,omitempty"` // SHA-256 of the box certificate
}

// Relay is a paired agent. Kind drives the lease priority.
type Relay struct {
	AgentID string     `json:"agent_id"`
	Kind    string     `json:"kind"` // dedicated | pc | phone
	Keys    PublicKeys `json:"keys"`
	Label   string     `json:"label,omitempty"`
	// MACs are the addresses this relay answers to on the home network, as
	// the owner's app copied them from the relay's own signed snapshots.
	// They are protected like the owner's pinned devices: the relay in
	// front of the box protects itself (Verifier.Protect), but a standby
	// relay would otherwise be blockable, and cutting the household's next
	// relay is cutting its remote access.
	MACs []string `json:"macs,omitempty"`
}

// Delegation lets a key other than the owner's act, until it expires. The
// owner's app renews the 30-day ones (RFC-005).
type Delegation struct {
	Keys      PublicKeys `json:"keys"`
	Scope     string     `json:"scope"`
	ExpiresAt time.Time  `json:"expires_at"`
	Label     string     `json:"label,omitempty"`
}

// KeyID of the relay's signing key.
func (r Relay) KeyID() string { return r.Keys.KeyID() }

// KeyID of the delegated signing key.
func (d Delegation) KeyID() string { return d.Keys.KeyID() }

// isRevoked reports whether keyID appears in the revocation list.
func (b *Bundle) isRevoked(keyID string) bool {
	for _, r := range b.Revoked {
		if r == keyID {
			return true
		}
	}
	return false
}

// delegation finds the delegation for keyID, if any.
func (b *Bundle) delegation(keyID string) (Delegation, bool) {
	for _, d := range b.Delegations {
		if d.KeyID() == keyID && keyID != "" {
			return d, true
		}
	}
	return Delegation{}, false
}

// Relay finds a paired relay by its agent id.
func (b *Bundle) Relay(agentID string) (Relay, bool) {
	for _, r := range b.Relays {
		if r.AgentID == agentID {
			return r, true
		}
	}
	return Relay{}, false
}

// RelayByKeyID finds a paired relay by its signing key, which is how a hub
// checks the signature on a result or a snapshot.
func (b *Bundle) RelayByKeyID(keyID string) (Relay, bool) {
	for _, r := range b.Relays {
		if r.KeyID() == keyID && keyID != "" {
			return r, true
		}
	}
	return Relay{}, false
}

// IsProtected reports whether a MAC may not be cut by a delegated key: a
// device the owner pinned, or one of the household's relays.
// Comparison ignores case and separators, since the box mixes both.
func (b *Bundle) IsProtected(mac string) bool {
	want := normaliseMAC(mac)
	if want == "" {
		return false
	}
	for _, m := range b.ProtectedMACs {
		if normaliseMAC(m) == want {
			return true
		}
	}
	for _, r := range b.Relays {
		for _, m := range r.MACs {
			if normaliseMAC(m) == want {
				return true
			}
		}
	}
	return false
}

func normaliseMAC(mac string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(mac) {
		if r != ':' && r != '-' && r != '.' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// valid rejects a bundle that could not be enforced.
func (b *Bundle) valid() error {
	if b.TenantID == "" {
		return ErrTenant
	}
	if !b.Owner.valid() {
		return ErrMalformed
	}
	for _, d := range b.Delegations {
		if !d.Keys.valid() {
			return ErrMalformed
		}
		switch d.Scope {
		case ScopeView, ScopeControl, ScopeHubBlock:
		default:
			return ErrScope
		}
	}
	for _, r := range b.Relays {
		if !r.Keys.valid() || r.AgentID == "" {
			return ErrMalformed
		}
	}
	return nil
}

// Member delegations (issue #47). The owner's app grants a member's key
// view or control for DelegationTTL, renews it when it is within
// DelegationRenewWithin of its end, and revokes it by moving its key id to
// Revoked. Expiry is what ends a member's access when the owner's phone is
// lost or gone: nobody else can renew it.
const (
	DelegationTTL         = 30 * 24 * time.Hour
	DelegationRenewWithin = 7 * 24 * time.Hour
)

// memberScope reports whether a scope is one a member may be granted. The
// hub's narrow key (hub_block) is delegated on its own path, not as a member.
func memberScope(scope string) bool { return scope == ScopeView || scope == ScopeControl }

// Delegate grants keys the scope until now + DelegationTTL, replacing any
// delegation the key already holds and lifting a revocation of it. The
// owner's own key and the relays' keys are refused: the owner needs no
// delegation, and a relay signs results, never orders.
func (b *Bundle) Delegate(keys PublicKeys, scope, label string, now time.Time) error {
	if !memberScope(scope) {
		return fmt.Errorf("%w: a member may hold view or control, not %q", ErrScope, scope)
	}
	id := keys.KeyID()
	if !keys.valid() || id == "" || id == b.Owner.KeyID() {
		return fmt.Errorf("%w: delegated key", ErrMalformed)
	}
	if _, ok := b.RelayByKeyID(id); ok {
		return fmt.Errorf("%w: a relay's key cannot be delegated", ErrMalformed)
	}
	kept := b.Delegations[:0:0]
	for _, d := range b.Delegations {
		if d.KeyID() != id {
			kept = append(kept, d)
		}
	}
	b.Delegations = append(kept, Delegation{Keys: keys, Scope: scope, ExpiresAt: now.Add(DelegationTTL).UTC(), Label: label})
	revoked := b.Revoked[:0:0]
	for _, r := range b.Revoked {
		if r != id {
			revoked = append(revoked, r)
		}
	}
	b.Revoked = revoked
	return nil
}

// Revoke drops keyID's delegation and lists it as revoked, so a relay that
// still holds an older bundle's key refuses it by name. It reports whether
// the bundle changed.
func (b *Bundle) Revoke(keyID string) bool {
	if keyID == "" {
		return false
	}
	changed := false
	kept := b.Delegations[:0:0]
	for _, d := range b.Delegations {
		if d.KeyID() == keyID {
			changed = true
			continue
		}
		kept = append(kept, d)
	}
	b.Delegations = kept
	if !b.isRevoked(keyID) {
		b.Revoked = append(b.Revoked, keyID)
		changed = true
	}
	return changed
}

// RenewDelegations moves every member delegation that ends within
// DelegationRenewWithin of now (or has already ended) to now +
// DelegationTTL, and returns how many it moved. The hub key's delegation is
// left alone.
func (b *Bundle) RenewDelegations(now time.Time) int {
	n := 0
	for i, d := range b.Delegations {
		if !memberScope(d.Scope) || b.isRevoked(d.KeyID()) {
			continue
		}
		if d.ExpiresAt.Sub(now) <= DelegationRenewWithin {
			b.Delegations[i].ExpiresAt = now.Add(DelegationTTL).UTC()
			n++
		}
	}
	return n
}

// MemberDelegation is keyID's member delegation as a relay would judge it
// at now: ErrRevoked, ErrUnknownKey (none), ErrDelegationExpired, or the
// delegation. OwnerOnly is the caller's to weigh: it does not end a
// delegation, it suspends what the delegation may do.
func (b *Bundle) MemberDelegation(keyID string, now time.Time) (Delegation, error) {
	if b.isRevoked(keyID) {
		return Delegation{}, fmt.Errorf("%w: %s", ErrRevoked, keyID)
	}
	d, ok := b.delegation(keyID)
	if !ok || !memberScope(d.Scope) {
		return Delegation{}, fmt.Errorf("%w: %s", ErrUnknownKey, keyID)
	}
	if !d.ExpiresAt.IsZero() && !now.Before(d.ExpiresAt) {
		return d, fmt.Errorf("%w: %s", ErrDelegationExpired, keyID)
	}
	return d, nil
}
