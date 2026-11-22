package trust

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// Recovery of a lost owner phone (issue #48). The household's relay and its
// members hold a bundle signed by the old owner; the new phone's key is
// accepted only when a co-owner or a relay of that bundle handed it over, and
// the old owner key is refused afterwards.

type recoveryCase struct {
	household
	coOwner  *Identity
	newOwner *Identity
	held     Bundle
}

func newRecoveryCase(t *testing.T) recoveryCase {
	t.Helper()
	coOwner := mustID(t)
	h := newHousehold(t, func(b *Bundle) {
		if err := b.AddCoOwner(coOwner.Public(), "Awa"); err != nil {
			t.Fatal(err)
		}
	})
	return recoveryCase{household: h, coOwner: coOwner, newOwner: mustID(t), held: *h.v.Bundle()}
}

// successor signs what a recovering phone would upload.
func (rc recoveryCase) successor(t *testing.T, endorser *Identity, via string, mutate ...func(*Bundle)) Signed[Bundle] {
	t.Helper()
	ho, err := NewHandover(endorser, rc.held, rc.newOwner.Public(), via, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	next, err := Succeed(rc.held, ho, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mutate {
		m(&next)
	}
	sb, err := Sign(rc.newOwner, DomainBundle, next)
	if err != nil {
		t.Fatal(err)
	}
	return sb
}

func TestSuccession_CoOwnerHandsTheHouseholdOver(t *testing.T) {
	rc := newRecoveryCase(t)
	sb := rc.successor(t, rc.coOwner, ViaCoOwner)

	if err := rc.v.AcceptBundle(sb); err != nil {
		t.Fatalf("a co-owner signed in advance hands over: %v", err)
	}
	if rc.v.OwnerKeyID() != rc.newOwner.KeyID() {
		t.Fatalf("pinned owner = %s, want the new one", rc.v.OwnerKeyID())
	}
	b := rc.v.Bundle()
	if len(b.Relays) != 1 || len(b.Delegations) != 2 || b.Version != 2 {
		t.Fatalf("the new bundle keeps the household: %+v", b)
	}
	if _, ok := b.CoOwner(rc.coOwner.KeyID()); ok {
		t.Fatal("the co-owner that took over is the owner now, not a co-owner")
	}

	// The new owner's commands run; the old one's are refused by name.
	if _, err := rc.v.VerifyCommand(cmd(t, rc.newOwner, http.MethodPost, "/api/devices/AA:BB:CC:DD:EE:FF/block"), nil, now); err != nil {
		t.Fatalf("new owner's command: %v", err)
	}
	if _, err := rc.v.VerifyCommand(cmd(t, rc.owner, http.MethodPost, "/api/devices/AA:BB:CC:DD:EE:FF/block"), nil, now); !errors.Is(err, ErrRevoked) {
		t.Fatalf("old owner's command = %v, want ErrRevoked", err)
	}
	// The members' delegations still hold.
	if _, err := rc.v.VerifyCommand(cmd(t, rc.member, http.MethodPost, "/api/devices/AA:BB:CC:DD:EE:FF/unblock"), nil, now); err != nil {
		t.Fatalf("member's command after the handover: %v", err)
	}

	// A bundle the old owner signs afterwards is refused, whatever its version.
	old := rc.held
	old.Version = 99
	stale, err := Sign(rc.owner, DomainBundle, old)
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.v.AcceptBundle(stale); err == nil {
		t.Fatal("the old owner's bundle was accepted after the handover")
	}

	// The new owner's next bundles keep the handover and follow normally.
	later := *b
	later.Version++
	lsb, err := Sign(rc.newOwner, DomainBundle, later)
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.v.AcceptBundle(lsb); err != nil {
		t.Fatalf("the new owner's next bundle: %v", err)
	}
}

func TestSuccession_ARelayHandsTheHouseholdOver(t *testing.T) {
	rc := newRecoveryCase(t)
	sb := rc.successor(t, rc.relay, ViaRelay)
	if err := rc.v.AcceptBundle(sb); err != nil {
		t.Fatalf("a relay of the bundle hands over: %v", err)
	}
	if rc.v.OwnerKeyID() != rc.newOwner.KeyID() {
		t.Fatal("the new owner key is not pinned")
	}
}

// A relay that missed the handover follows a later bundle of the new owner,
// because every bundle keeps it.
func TestSuccession_ALaterBundleCarriesTheHandover(t *testing.T) {
	rc := newRecoveryCase(t)
	first := rc.successor(t, rc.coOwner, ViaCoOwner)
	b, err := first.Unverified()
	if err != nil {
		t.Fatal(err)
	}
	b.Version += 3
	later, err := Sign(rc.newOwner, DomainBundle, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.v.AcceptBundle(later); err != nil {
		t.Fatalf("a later bundle of the new owner: %v", err)
	}
}

func TestSuccession_Refusals(t *testing.T) {
	rc := newRecoveryCase(t)
	stranger := mustID(t)

	cases := []struct {
		name string
		sb   func() Signed[Bundle]
	}{
		{"no handover at all (a hub's own owner key)", func() Signed[Bundle] {
			b := rc.held
			b.Version, b.Owner = 5, rc.newOwner.Public()
			b.Revoke(rc.owner.KeyID())
			sb, _ := Sign(rc.newOwner, DomainBundle, b)
			return sb
		}},
		{"endorsed by a key that is no co-owner", func() Signed[Bundle] { return rc.successor(t, stranger, ViaCoOwner) }},
		{"endorsed by the hub's narrow key", func() Signed[Bundle] { return rc.successor(t, rc.hub, ViaRelay) }},
		{"a member delegated control is no co-owner", func() Signed[Bundle] { return rc.successor(t, rc.member, ViaCoOwner) }},
		{"a co-owner claiming to be a relay", func() Signed[Bundle] { return rc.successor(t, rc.coOwner, ViaRelay) }},
		{"the old owner key is not revoked", func() Signed[Bundle] {
			return rc.successor(t, rc.coOwner, ViaCoOwner, func(b *Bundle) { b.Revoked = nil })
		}},
		{"not newer than the bundle in force", func() Signed[Bundle] {
			return rc.successor(t, rc.coOwner, ViaCoOwner, func(b *Bundle) { b.Version = rc.held.Version })
		}},
		{"another household", func() Signed[Bundle] {
			return rc.successor(t, rc.coOwner, ViaCoOwner, func(b *Bundle) { b.TenantID = "22222222-2222-2222-2222-222222222222" })
		}},
		{"the handover names another new owner", func() Signed[Bundle] {
			sb := rc.successor(t, rc.coOwner, ViaCoOwner)
			b, _ := sb.Unverified()
			b.Owner = stranger.Public()
			out, _ := Sign(stranger, DomainBundle, b)
			return out
		}},
		{"signed by another key than the new owner's", func() Signed[Bundle] {
			sb := rc.successor(t, rc.coOwner, ViaCoOwner)
			b, _ := sb.Unverified()
			out, _ := Sign(stranger, DomainBundle, b)
			return out
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := rc.v.AcceptBundle(tc.sb()); err == nil {
				t.Fatal("accepted")
			}
			if rc.v.OwnerKeyID() != rc.owner.KeyID() {
				t.Fatal("the pinned owner moved on a refused bundle")
			}
		})
	}

	// A handover for another old key (replayed from another household's
	// recovery) is refused.
	other := rc.held
	other.Owner = stranger.Public()
	ho, err := NewHandover(rc.coOwner, other, rc.newOwner.Public(), ViaCoOwner, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := Succeed(rc.held, ho, 0, now)
	sb, _ := Sign(rc.newOwner, DomainBundle, next)
	if err := rc.v.AcceptBundle(sb); !errors.Is(err, ErrSuccession) {
		t.Fatalf("handover from another key = %v, want ErrSuccession", err)
	}
}

// A co-owner the owner removed, or revoked, can no longer take over.
func TestSuccession_RemovedOrRevokedCoOwnerCannot(t *testing.T) {
	for _, change := range []func(b *Bundle, id string){
		func(b *Bundle, id string) { b.RemoveCoOwner(id) },
		func(b *Bundle, id string) { b.Revoked = append(b.Revoked, id) },
	} {
		rc := newRecoveryCase(t)
		b := rc.held
		b.CoOwners = append([]CoOwner(nil), b.CoOwners...)
		change(&b, rc.coOwner.KeyID())
		b.Version++
		sb, _ := Sign(rc.owner, DomainBundle, b)
		if err := rc.v.AcceptBundle(sb); err != nil {
			t.Fatal(err)
		}
		rc.held = *rc.v.Bundle()
		if err := rc.v.AcceptBundle(rc.successor(t, rc.coOwner, ViaCoOwner)); !errors.Is(err, ErrSuccession) {
			t.Fatalf("former co-owner = %v, want ErrSuccession", err)
		}
	}
}

func TestCoOwner_AddRefusals(t *testing.T) {
	b, owner := ownerBundle(t)
	relay := mustID(t)
	b.Relays = []Relay{{AgentID: "agent-1", Kind: "pc", Keys: relay.Public()}}
	if err := b.AddCoOwner(owner.Public(), ""); !errors.Is(err, ErrMalformed) {
		t.Errorf("the owner's own key = %v", err)
	}
	if err := b.AddCoOwner(relay.Public(), ""); !errors.Is(err, ErrMalformed) {
		t.Errorf("a relay's key = %v", err)
	}
	co := mustID(t)
	b.Revoked = []string{co.KeyID()}
	if err := b.AddCoOwner(co.Public(), ""); !errors.Is(err, ErrRevoked) {
		t.Errorf("a revoked key = %v", err)
	}
	b.Revoked = nil
	if err := b.AddCoOwner(co.Public(), "a"); err != nil {
		t.Fatal(err)
	}
	if err := b.AddCoOwner(co.Public(), "b"); err != nil || len(b.CoOwners) != 1 || b.CoOwners[0].Label != "b" {
		t.Fatalf("adding again replaces: %v %+v", err, b.CoOwners)
	}
	if !b.RemoveCoOwner(co.KeyID()) || b.RemoveCoOwner(co.KeyID()) {
		t.Fatal("RemoveCoOwner reports the change once")
	}
}

func TestSucceed_DropsTheEndorserAndRevokesTheOldOwner(t *testing.T) {
	rc := newRecoveryCase(t)
	// The co-owner also held a delegation as a member.
	rc.held.Delegations = append(rc.held.Delegations, Delegation{Keys: rc.coOwner.Public(), Scope: ScopeView, ExpiresAt: now.Add(time.Hour)})
	ho, _ := NewHandover(rc.coOwner, rc.held, rc.newOwner.Public(), ViaCoOwner, nil, now)
	next, err := Succeed(rc.held, ho, 41, now)
	if err != nil {
		t.Fatal(err)
	}
	if next.Version != 42 {
		t.Errorf("version = %d, want above the floor", next.Version)
	}
	if !next.isRevoked(rc.owner.KeyID()) || next.Owner.KeyID() != rc.newOwner.KeyID() || next.Succession == nil {
		t.Errorf("next = %+v", next)
	}
	if _, ok := next.delegation(rc.coOwner.KeyID()); ok {
		t.Error("the endorsing co-owner keeps a delegation it no longer needs")
	}
	if len(rc.held.CoOwners) != 1 {
		t.Error("Succeed changed the bundle it was given")
	}
	if _, err := NewHandover(rc.coOwner, rc.held, rc.owner.Public(), ViaCoOwner, nil, now); !errors.Is(err, ErrMalformed) {
		t.Errorf("handing over to the same key = %v", err)
	}
}
