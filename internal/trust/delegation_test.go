package trust

import (
	"errors"
	"testing"
	"time"
)

// Member delegations (issue #47): the owner grants view or control to a
// member's key for 30 days, the owner's app renews them before they run out,
// and a revocation is final until the owner grants the key again.

func ownerBundle(t *testing.T) (Bundle, *Identity) {
	t.Helper()
	owner := mustID(t)
	return Bundle{TenantID: tenant, Version: 1, IssuedAt: now, Owner: owner.Public()}, owner
}

func TestDelegate_GrantsThirtyDaysAndReplaces(t *testing.T) {
	b, _ := ownerBundle(t)
	member := mustID(t)

	if err := b.Delegate(member.Public(), ScopeView, "Awa", now); err != nil {
		t.Fatal(err)
	}
	if len(b.Delegations) != 1 || b.Delegations[0].Scope != ScopeView || !b.Delegations[0].ExpiresAt.Equal(now.Add(DelegationTTL)) {
		t.Fatalf("after view: %+v", b.Delegations)
	}
	if DelegationTTL != 30*24*time.Hour {
		t.Fatalf("DelegationTTL = %s, RFC-005 says 30 days", DelegationTTL)
	}

	later := now.Add(48 * time.Hour)
	if err := b.Delegate(member.Public(), ScopeControl, "Awa", later); err != nil {
		t.Fatal(err)
	}
	if len(b.Delegations) != 1 || b.Delegations[0].Scope != ScopeControl || !b.Delegations[0].ExpiresAt.Equal(later.Add(DelegationTTL)) {
		t.Fatalf("a second grant replaces the first: %+v", b.Delegations)
	}
}

func TestDelegate_Refusals(t *testing.T) {
	b, owner := ownerBundle(t)
	relay := mustID(t)
	b.Relays = []Relay{{AgentID: "agent-1", Kind: "pc", Keys: relay.Public()}}
	member := mustID(t)

	if err := b.Delegate(member.Public(), ScopeHubBlock, "", now); !errors.Is(err, ErrScope) {
		t.Errorf("hub_block through Delegate = %v, want ErrScope (it is the hub key's, not a member's)", err)
	}
	if err := b.Delegate(member.Public(), "admin", "", now); !errors.Is(err, ErrScope) {
		t.Errorf("an unknown scope = %v, want ErrScope", err)
	}
	if err := b.Delegate(owner.Public(), ScopeControl, "", now); !errors.Is(err, ErrMalformed) {
		t.Errorf("delegating the owner's own key = %v, want ErrMalformed", err)
	}
	if err := b.Delegate(relay.Public(), ScopeControl, "", now); !errors.Is(err, ErrMalformed) {
		t.Errorf("delegating a relay's key = %v, want ErrMalformed (a relay signs results, never orders)", err)
	}
	if err := b.Delegate(PublicKeys{Ed25519: []byte{1, 2}}, ScopeView, "", now); !errors.Is(err, ErrMalformed) {
		t.Errorf("a malformed key = %v, want ErrMalformed", err)
	}
	if len(b.Delegations) != 0 {
		t.Fatalf("a refused grant left %+v", b.Delegations)
	}
}

func TestRevoke_RemovesAndListsTheKey(t *testing.T) {
	b, _ := ownerBundle(t)
	member := mustID(t)
	_ = b.Delegate(member.Public(), ScopeControl, "Awa", now)

	if !b.Revoke(member.KeyID()) {
		t.Fatal("Revoke reported no change")
	}
	if len(b.Delegations) != 0 || len(b.Revoked) != 1 || b.Revoked[0] != member.KeyID() {
		t.Fatalf("after revoke: delegations %+v, revoked %v", b.Delegations, b.Revoked)
	}
	if b.Revoke(member.KeyID()) {
		t.Fatal("revoking twice reported a change")
	}
	if len(b.Revoked) != 1 {
		t.Fatalf("revoked twice: %v", b.Revoked)
	}
	if _, err := b.MemberDelegation(member.KeyID(), now); !errors.Is(err, ErrRevoked) {
		t.Fatalf("MemberDelegation after revoke = %v, want ErrRevoked", err)
	}

	// The owner may change their mind: a new grant lifts the revocation.
	if err := b.Delegate(member.Public(), ScopeView, "Awa", now); err != nil {
		t.Fatal(err)
	}
	if len(b.Revoked) != 0 {
		t.Fatalf("granting again kept the revocation: %v", b.Revoked)
	}
	if b.Revoke("") {
		t.Fatal("revoking the empty key id reported a change")
	}
}

func TestRenewDelegations_OnlyWhenDue(t *testing.T) {
	b, _ := ownerBundle(t)
	awa, moussa, hub := mustID(t), mustID(t), mustID(t)
	_ = b.Delegate(awa.Public(), ScopeControl, "Awa", now)
	_ = b.Delegate(moussa.Public(), ScopeView, "Moussa", now.Add(20*24*time.Hour))
	b.Delegations = append(b.Delegations, Delegation{Keys: hub.Public(), Scope: ScopeHubBlock, ExpiresAt: now.Add(24 * time.Hour)})

	// Twenty days in, nobody is within a week of the end.
	if n := b.RenewDelegations(now.Add(20 * 24 * time.Hour)); n != 0 {
		t.Fatalf("renewed %d at day 20, want none", n)
	}
	// Day 24: Awa's (day 30) is due, Moussa's (day 50) is not, and the
	// hub key's is not a member's to renew.
	day24 := now.Add(24 * 24 * time.Hour)
	if n := b.RenewDelegations(day24); n != 1 {
		t.Fatalf("renewed %d at day 24, want 1", n)
	}
	got := map[string]time.Time{}
	for _, d := range b.Delegations {
		got[d.KeyID()] = d.ExpiresAt
	}
	if !got[awa.KeyID()].Equal(day24.Add(DelegationTTL)) {
		t.Errorf("Awa's now ends %s, want %s", got[awa.KeyID()], day24.Add(DelegationTTL))
	}
	if !got[moussa.KeyID()].Equal(now.Add(50 * 24 * time.Hour)) {
		t.Errorf("Moussa's moved to %s", got[moussa.KeyID()])
	}
	if !got[hub.KeyID()].Equal(now.Add(24 * time.Hour)) {
		t.Errorf("the hub key's moved to %s", got[hub.KeyID()])
	}

	// One that already ran out while the owner's phone was off comes back
	// when the owner's app does: the owner never took it away.
	day90 := now.Add(90 * 24 * time.Hour)
	if _, err := b.MemberDelegation(awa.KeyID(), day90); !errors.Is(err, ErrDelegationExpired) {
		t.Fatalf("at day 90 = %v, want ErrDelegationExpired", err)
	}
	if n := b.RenewDelegations(day90); n != 2 {
		t.Fatalf("renewed %d at day 90, want 2", n)
	}
	if d, err := b.MemberDelegation(awa.KeyID(), day90); err != nil || d.Scope != ScopeControl {
		t.Fatalf("after renewal = %+v, %v", d, err)
	}
}

func TestMemberDelegation(t *testing.T) {
	b, _ := ownerBundle(t)
	member, stranger := mustID(t), mustID(t)
	_ = b.Delegate(member.Public(), ScopeView, "Awa", now)

	if d, err := b.MemberDelegation(member.KeyID(), now); err != nil || d.Scope != ScopeView {
		t.Fatalf("delegated = %+v, %v", d, err)
	}
	if _, err := b.MemberDelegation(stranger.KeyID(), now); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("a key with no delegation = %v, want ErrUnknownKey", err)
	}
	if _, err := b.MemberDelegation(member.KeyID(), now.Add(DelegationTTL)); !errors.Is(err, ErrDelegationExpired) {
		t.Fatalf("at the expiry = %v, want ErrDelegationExpired", err)
	}
}

// TestVerifyCommand_MemberDelegationLifecycle: what Delegate, Revoke and
// RenewDelegations produce is what a relay enforces.
func TestVerifyCommand_MemberDelegationLifecycle(t *testing.T) {
	b, owner := ownerBundle(t)
	member := mustID(t)
	_ = b.Delegate(member.Public(), ScopeControl, "Awa", now.Add(-29*24*time.Hour))
	install := func(b Bundle, at time.Time) *Verifier {
		v := NewVerifier(tenant, owner.Public().signPub(), at.Add(-time.Minute))
		sb, err := Sign(owner, DomainBundle, b)
		if err != nil {
			t.Fatal(err)
		}
		if err := v.AcceptBundle(sb); err != nil {
			t.Fatal(err)
		}
		return v
	}
	block := func(at time.Time) SignedCommand {
		sc, err := NewCommand(member, tenant, "POST", "/api/devices/ac:bb:cc:00:00:22/block", nil, at, 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		return sc
	}

	if _, err := install(b, now).VerifyCommand(block(now), nil, now); err != nil {
		t.Fatalf("day 29 of 30: %v", err)
	}
	day31 := now.Add(2 * 24 * time.Hour)
	if _, err := install(b, day31).VerifyCommand(block(day31), nil, day31); !errors.Is(err, ErrDelegationExpired) {
		t.Fatalf("day 31, not renewed = %v, want ErrDelegationExpired", err)
	}
	renewed := b
	renewed.Delegations = append([]Delegation(nil), b.Delegations...)
	renewed.RenewDelegations(now)
	if _, err := install(renewed, day31).VerifyCommand(block(day31), nil, day31); err != nil {
		t.Fatalf("day 31, renewed at day 29: %v", err)
	}
	renewed.Revoke(member.KeyID())
	if _, err := install(renewed, day31).VerifyCommand(block(day31), nil, day31); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked = %v, want ErrRevoked", err)
	}
}
