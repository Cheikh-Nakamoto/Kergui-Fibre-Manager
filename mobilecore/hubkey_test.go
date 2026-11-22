package mobilecore

import (
	"encoding/json"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// The owner's "Répondre 1 pour bloquer" switch (issue #49): the core signs
// the hub key's hub_block delegation into the bundle, and revokes it, and
// RemoteRelays tells the app whether it is on.

func TestHubKey_SwitchOnAndOff(t *testing.T) {
	h := newMemberHousehold(t)
	hub := newRelay(t) // any fresh identity
	pub := hub.Public()

	if v := remoteView(t, h.owner, credTenant, h.bundle); v.HubKey != nil {
		t.Fatalf("before the switch: hubKey = %+v", v.HubKey)
	}

	h.sign(t, map[string]any{"hubKey": map[string]any{"ed25519": pub.Ed25519, "x25519": pub.X25519, "label": "WhatsApp"}})
	b, err := openSigned(t, h.bundle).Open(trust.DomainBundle, ownerSigningKey(t, h.owner))
	if err != nil {
		t.Fatal(err)
	}
	d, ok := b.HubKey()
	if !ok || d.KeyID() != hub.KeyID() || d.Scope != trust.ScopeHubBlock || !d.ExpiresAt.IsZero() {
		t.Fatalf("the signed bundle's hub key = %+v, %v", d, ok)
	}
	v := remoteView(t, h.owner, credTenant, h.bundle)
	if v.HubKey == nil || v.HubKey.KeyID != hub.KeyID() {
		t.Fatalf("RemoteRelays hubKey = %+v, want %s", v.HubKey, hub.KeyID())
	}
	if len(v.Delegations) != 0 {
		t.Fatalf("the hub key is listed as a member's delegation: %+v", v.Delegations)
	}

	// A member's delegation survives the switch, and the switch survives
	// the next bundle.
	h.delegate(t, trust.ScopeView)
	if v := remoteView(t, h.owner, credTenant, h.bundle); v.HubKey == nil || len(v.Delegations) != 1 {
		t.Fatalf("after a member grant: hubKey %+v, delegations %+v", v.HubKey, v.Delegations)
	}

	h.sign(t, map[string]any{"revoke": hub.KeyID()})
	if v := remoteView(t, h.owner, credTenant, h.bundle); v.HubKey != nil {
		t.Fatalf("after switching off: hubKey = %+v", v.HubKey)
	}
	b, _ = openSigned(t, h.bundle).Unverified()
	revoked := false
	for _, r := range b.Revoked {
		revoked = revoked || r == hub.KeyID()
	}
	if !revoked {
		t.Fatalf("the hub key is not revoked by name: %v", b.Revoked)
	}

	// On again: the revocation is lifted.
	h.sign(t, map[string]any{"hubKey": map[string]any{"ed25519": pub.Ed25519, "x25519": pub.X25519}})
	if v := remoteView(t, h.owner, credTenant, h.bundle); v.HubKey == nil {
		t.Fatal("switching it back on did not delegate it")
	}
}

func TestHubKey_RefusesTheMembersKey(t *testing.T) {
	h := newMemberHousehold(t)
	h.delegate(t, trust.ScopeControl)
	ch, _ := json.Marshal(map[string]any{"hubKey": map[string]any{"ed25519": h.keys.Ed25519, "x25519": h.keys.X25519}})
	if _, err := h.owner.SignTrustBundle(credTenant, h.bundle, string(ch)); err == nil {
		t.Fatal("a member's key was made the hub key")
	}
}
