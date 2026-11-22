package trust

import (
	"errors"
	"fmt"
	"time"
)

// DomainPolicy signs the household's relay policy.
const DomainPolicy = "kergui/policy/v1"

// ErrStalePolicy refuses a policy that is not newer than the one in force.
var ErrStalePolicy = errors.New("trust: relay policy is not newer than the current one")

// Policy is what the owner's app tells the household's relay to do on its
// own, without a command each time (RFC-006, #36): today the auto-reject
// rule, and the entitlement token that pays for it.
//
// The owner signs it, so the hub that carries it cannot turn the rule on,
// widen it, or drop a device from the accepted list: any of those would have
// the relay cut a device the household never refused. The version only moves
// forward, so a hub cannot replay one the owner replaced either.
//
// The entitlement token is signed by the hub's licensing key, not by the
// owner; the relay checks it on its own with internal/entitlement, against
// the keys it was configured with and the box it is in front of.
type Policy struct {
	TenantID string    `json:"tenant_id"`
	Version  uint64    `json:"version"`
	IssuedAt time.Time `json:"issued_at"`

	AutoReject AutoRejectPolicy `json:"auto_reject"`
	// Entitlement is the token the app holds for this box ("" for none).
	Entitlement string `json:"entitlement,omitempty"`
}

// AutoRejectPolicy is the auto-reject rule as the owner set it on the phone.
type AutoRejectPolicy struct {
	Enabled bool `json:"enabled"`
	// Since is when the rule was last turned on: a new Since tells the relay
	// to start over from Accepted.
	Since time.Time `json:"since"`
	// Accepted are the MACs the household accepted on the phone (present
	// when the rule was turned on, approved, renamed or given their access
	// back).
	Accepted []string `json:"accepted,omitempty"`
}

// AcceptPolicy installs a policy after checking the owner signed it, that it
// is for this household and that its version moved forward.
func (v *Verifier) AcceptPolicy(sp Signed[Policy]) (Policy, error) {
	p, err := sp.Open(DomainPolicy, v.ownerKey)
	if err != nil {
		return Policy{}, err
	}
	if p.TenantID != v.tenantID {
		return Policy{}, fmt.Errorf("%w: policy is for %s", ErrTenant, p.TenantID)
	}
	if p.Version == 0 {
		return Policy{}, fmt.Errorf("%w: a relay policy must carry a version", ErrMalformed)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.policy != nil && p.Version <= v.policy.Version {
		return Policy{}, fmt.Errorf("%w: version %d, current %d", ErrStalePolicy, p.Version, v.policy.Version)
	}
	v.policy = &p
	return p, nil
}

// Policy returns the policy in force, or nil before the first one arrives.
func (v *Verifier) Policy() *Policy {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.policy
}
