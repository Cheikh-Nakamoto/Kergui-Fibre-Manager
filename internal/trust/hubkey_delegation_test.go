package trust

import (
	"errors"
	"testing"
)

// The hub key's delegation (issue #49): the owner's "1 = bloquer" switch
// delegates hub_block to the hub's narrow key in the bundle, and revokes it
// when switched off.

func TestDelegateHubKey_OneNarrowDelegationWithoutExpiry(t *testing.T) {
	b, _ := ownerBundle(t)
	hub, other := mustID(t), mustID(t)
	member := mustID(t)
	_ = b.Delegate(member.Public(), ScopeControl, "Awa", now)

	if err := b.DelegateHubKey(hub.Public(), "WhatsApp"); err != nil {
		t.Fatal(err)
	}
	d, ok := b.HubKey()
	if !ok || d.KeyID() != hub.KeyID() || d.Scope != ScopeHubBlock || !d.ExpiresAt.IsZero() || d.Label != "WhatsApp" {
		t.Fatalf("HubKey() = %+v, %v", d, ok)
	}
	// The members' delegations are left as they are.
	if _, err := b.MemberDelegation(member.KeyID(), now); err != nil {
		t.Fatalf("the member's delegation: %v", err)
	}

	// A new hub key (a rotated seed) replaces the old one: there is one.
	if err := b.DelegateHubKey(other.Public(), ""); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, d := range b.Delegations {
		if d.Scope == ScopeHubBlock {
			n++
		}
	}
	if d, _ := b.HubKey(); n != 1 || d.KeyID() != other.KeyID() {
		t.Fatalf("%d hub keys, current %s", n, d.KeyID())
	}
}

func TestDelegateHubKey_RevokeThenGrantAgain(t *testing.T) {
	b, _ := ownerBundle(t)
	hub := mustID(t)
	_ = b.DelegateHubKey(hub.Public(), "")

	if !b.Revoke(hub.KeyID()) {
		t.Fatal("revoking the hub key changed nothing")
	}
	if _, ok := b.HubKey(); ok {
		t.Fatal("a revoked hub key is still delegated")
	}
	if !b.isRevoked(hub.KeyID()) {
		t.Fatal("the hub key is not listed as revoked")
	}

	if err := b.DelegateHubKey(hub.Public(), ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.HubKey(); !ok || b.isRevoked(hub.KeyID()) {
		t.Fatalf("switching it back on: delegated %v, revoked %v", ok, b.Revoked)
	}
}

func TestDelegateHubKey_Refusals(t *testing.T) {
	b, owner := ownerBundle(t)
	relay, member := mustID(t), mustID(t)
	b.Relays = []Relay{{AgentID: "agent-1", Kind: "pc", Keys: relay.Public()}}
	_ = b.Delegate(member.Public(), ScopeView, "Awa", now)

	if err := b.DelegateHubKey(owner.Public(), ""); !errors.Is(err, ErrMalformed) {
		t.Errorf("the owner's key = %v, want ErrMalformed", err)
	}
	if err := b.DelegateHubKey(relay.Public(), ""); !errors.Is(err, ErrMalformed) {
		t.Errorf("a relay's key = %v, want ErrMalformed", err)
	}
	if err := b.DelegateHubKey(member.Public(), ""); !errors.Is(err, ErrMalformed) {
		t.Errorf("a member's key = %v, want ErrMalformed (it would lose its delegation)", err)
	}
	if err := b.DelegateHubKey(PublicKeys{Ed25519: []byte{1}}, ""); !errors.Is(err, ErrMalformed) {
		t.Errorf("malformed keys = %v, want ErrMalformed", err)
	}
	if _, ok := b.HubKey(); ok {
		t.Fatal("a refused hub key was delegated")
	}
}
