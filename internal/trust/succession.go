package trust

import (
	"bytes"
	"fmt"
	"time"
)

// Succession is how a household survives the loss of its owner's phone
// (issue #48, RFC-005 "Lost owner phone"). The relays and the members pin
// the owner's key; a new phone has a new key, so something they already
// trust has to vouch for it. Two things can:
//
//   - a co-owner: a member's key the owner signed into the bundle in
//     advance (Bundle.CoOwners). The co-owner's phone endorses its own
//     owner key;
//   - a relay of the bundle, at home on the Wi-Fi, once the new phone has
//     proved it knows the box password (RecoveryProof, argon2id-stretched,
//     H1 departure 5). That exchange is the relay's local listener's; the
//     hub carries none of it.
//
// The endorsement is a Handover signed by that key: from the old owner key,
// to the new one. The new owner signs the next bundle with its own key, the
// handover inside it, and the old owner key in Revoked. Whoever holds the
// previous bundle (a relay, a member) checks the endorser against that
// bundle, not against the new one: a hub cannot slip in a co-owner, nor
// pass off its own key as a relay's.
//
// Every bundle the new owner signs afterwards keeps the handover, so a relay
// or a member that missed the first one still follows. A second loss before
// they catch up breaks the chain, and they stay where they were (the member
// app says "other_owner"), which is the safe side.

// DomainHandover signs a handover.
const DomainHandover = "kergui/handover/v1"

// How a new owner key was endorsed.
const (
	ViaCoOwner = "co_owner"
	ViaRelay   = "relay"
)

// CoOwner is a key the owner signed in advance to take over the household
// if the owner's phone is lost. It is a member's key: it endorses, it does
// not sign commands by being a co-owner (a delegation does that).
type CoOwner struct {
	Keys  PublicKeys `json:"keys"`
	Label string     `json:"label,omitempty"`
}

// KeyID of the co-owner's key.
func (c CoOwner) KeyID() string { return c.Keys.KeyID() }

// Handover is the endorsement of a new owner key.
type Handover struct {
	TenantID string `json:"tenant_id"`
	// From is the key id of the owner key being replaced.
	From string `json:"from"`
	// To is the new owner's public keys.
	To PublicKeys `json:"to"`
	// Via is ViaCoOwner or ViaRelay, and Endorser the key id that signed.
	Via      string    `json:"via"`
	Endorser string    `json:"endorser"`
	IssuedAt time.Time `json:"issued_at"`
	// Nonce is the relay's challenge, for a ViaRelay handover.
	Nonce []byte `json:"nonce,omitempty"`
}

// SignedHandover is a handover with its endorser's signature.
type SignedHandover = Signed[Handover]

// Recovery refusals.
var (
	ErrNoSuccession = fmt.Errorf("%w: the bundle names another owner key and carries no handover", ErrUnknownKey)
	ErrSuccession   = fmt.Errorf("%w: the handover does not hand this household over", ErrUnknownKey)
)

// AddCoOwner signs keys in as a co-owner (replacing a previous entry for the
// same key). The owner's key and the relays' keys are refused, and so is a
// revoked key: the owner lifts a revocation by delegating the key again.
func (b *Bundle) AddCoOwner(keys PublicKeys, label string) error {
	id := keys.KeyID()
	if !keys.valid() || id == "" || id == b.Owner.KeyID() {
		return fmt.Errorf("%w: co-owner key", ErrMalformed)
	}
	if _, ok := b.RelayByKeyID(id); ok {
		return fmt.Errorf("%w: a relay's key cannot be a co-owner", ErrMalformed)
	}
	if b.isRevoked(id) {
		return fmt.Errorf("%w: %s", ErrRevoked, id)
	}
	kept := b.CoOwners[:0:0]
	for _, c := range b.CoOwners {
		if c.KeyID() != id {
			kept = append(kept, c)
		}
	}
	b.CoOwners = append(kept, CoOwner{Keys: keys, Label: label})
	return nil
}

// RemoveCoOwner drops a co-owner and reports whether it was there.
func (b *Bundle) RemoveCoOwner(keyID string) bool {
	kept := b.CoOwners[:0:0]
	for _, c := range b.CoOwners {
		if c.KeyID() != keyID {
			kept = append(kept, c)
		}
	}
	changed := len(kept) != len(b.CoOwners)
	b.CoOwners = kept
	return changed
}

// CoOwner finds a co-owner by key id, unless it is revoked.
func (b *Bundle) CoOwner(keyID string) (CoOwner, bool) {
	if keyID == "" || b.isRevoked(keyID) {
		return CoOwner{}, false
	}
	for _, c := range b.CoOwners {
		if c.KeyID() == keyID {
			return c, true
		}
	}
	return CoOwner{}, false
}

// NewHandover signs the endorsement of to as the owner of held's household.
func NewHandover(endorser *Identity, held Bundle, to PublicKeys, via string, nonce []byte, now time.Time) (SignedHandover, error) {
	if !to.valid() {
		return SignedHandover{}, fmt.Errorf("%w: new owner keys", ErrMalformed)
	}
	if to.KeyID() == held.Owner.KeyID() {
		return SignedHandover{}, fmt.Errorf("%w: the new owner key is the current one", ErrMalformed)
	}
	return Sign(endorser, DomainHandover, Handover{
		TenantID: held.TenantID, From: held.Owner.KeyID(), To: to,
		Via: via, Endorser: endorser.KeyID(), IssuedAt: now.UTC(), Nonce: nonce,
	})
}

// Succeed builds the first bundle of the new owner from the one in force:
// same relays, network, members and policy, the new owner key, the old one
// revoked, the handover kept, the version above both held's and floor. The
// endorsing co-owner's entry is dropped: that key's phone is the owner now.
// The caller signs it with the new owner's identity.
func Succeed(held Bundle, h SignedHandover, floor uint64, now time.Time) (Bundle, error) {
	ho, err := h.Unverified()
	if err != nil {
		return Bundle{}, err
	}
	next := held
	next.Version = max(held.Version, floor) + 1
	next.IssuedAt = now.UTC()
	next.Owner = ho.To
	next.Succession = &h
	next.Relays = append([]Relay(nil), held.Relays...)
	next.Delegations = append([]Delegation(nil), held.Delegations...)
	next.CoOwners = append([]CoOwner(nil), held.CoOwners...)
	next.Revoked = append([]string(nil), held.Revoked...)
	next.RemoveCoOwner(ho.To.KeyID())
	if ho.Via == ViaCoOwner {
		next.RemoveCoOwner(ho.Endorser)
	}
	// The new owner needs no delegation, and the old key none either.
	kept := next.Delegations[:0]
	for _, d := range next.Delegations {
		if d.KeyID() != ho.To.KeyID() && (ho.Via != ViaCoOwner || d.KeyID() != ho.Endorser) {
			kept = append(kept, d)
		}
	}
	next.Delegations = kept
	next.Revoke(held.Owner.KeyID())
	next.RenewDelegations(now)
	return next, nil
}

// CheckSuccession decides whether next — a bundle naming another owner key
// than held's — may replace held. It returns the new bundle once:
//
//   - it carries a handover for this household, from held's owner key, to
//     the owner key it names;
//   - the handover is signed by a co-owner of held (Via co_owner) or a
//     relay of held (Via relay), neither revoked in held;
//   - next is signed by its own owner key, valid, newer than held, and
//     revokes held's owner key.
//
// held is the bundle the caller already trusts; nothing in next is used to
// decide who may endorse.
func CheckSuccession(held Bundle, next Signed[Bundle]) (Bundle, error) {
	claimed, err := next.Unverified()
	if err != nil {
		return Bundle{}, err
	}
	if claimed.Succession == nil {
		return Bundle{}, ErrNoSuccession
	}
	ho, err := claimed.Succession.Unverified()
	if err != nil {
		return Bundle{}, err
	}
	if ho.TenantID != held.TenantID || ho.From != held.Owner.KeyID() {
		return Bundle{}, fmt.Errorf("%w: it replaces key %s of %s", ErrSuccession, ho.From, ho.TenantID)
	}
	if ho.To.KeyID() == "" || ho.To.KeyID() != claimed.Owner.KeyID() ||
		!bytes.Equal(ho.To.X25519, claimed.Owner.X25519) {
		return Bundle{}, fmt.Errorf("%w: it names another new owner", ErrSuccession)
	}
	var endorser PublicKeys
	switch ho.Via {
	case ViaCoOwner:
		c, ok := held.CoOwner(ho.Endorser)
		if !ok {
			return Bundle{}, fmt.Errorf("%w: %s is not a co-owner of the bundle in force", ErrSuccession, ho.Endorser)
		}
		endorser = c.Keys
	case ViaRelay:
		r, ok := held.RelayByKeyID(ho.Endorser)
		if !ok || held.isRevoked(ho.Endorser) {
			return Bundle{}, fmt.Errorf("%w: %s is not a relay of the bundle in force", ErrSuccession, ho.Endorser)
		}
		endorser = r.Keys
	default:
		return Bundle{}, fmt.Errorf("%w: unknown endorsement %q", ErrSuccession, ho.Via)
	}
	if _, err := claimed.Succession.Open(DomainHandover, endorser.signPub()); err != nil {
		return Bundle{}, fmt.Errorf("%w: handover: %v", ErrSuccession, err)
	}
	b, err := next.Open(DomainBundle, claimed.Owner.signPub())
	if err != nil {
		return Bundle{}, err
	}
	if b.TenantID != held.TenantID {
		return Bundle{}, fmt.Errorf("%w: bundle is for %s", ErrTenant, b.TenantID)
	}
	if err := b.valid(); err != nil {
		return Bundle{}, err
	}
	if b.Version <= held.Version {
		return Bundle{}, fmt.Errorf("%w: version %d, current %d", ErrStaleBundle, b.Version, held.Version)
	}
	if !b.isRevoked(held.Owner.KeyID()) {
		return Bundle{}, fmt.Errorf("%w: the new bundle does not revoke the old owner key", ErrSuccession)
	}
	return b, nil
}
