package trust

import (
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
