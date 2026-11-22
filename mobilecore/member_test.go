package mobilecore

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Member delegations (issue #47): the member's key is created in the member
// phone's core and never leaves it; the owner's core signs a delegation of
// it into the bundle, renews it before its 30 days run out, revokes it, and
// can keep the box to itself (owner_only). The member's core believes a
// bundle only when it delegates its key, and signs a command only with a
// control delegation that the bundle still holds.

var memberT0 = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

type memberKeysAnswer struct {
	KeyID   string `json:"keyId"`
	Ed25519 []byte `json:"ed25519"`
	X25519  []byte `json:"x25519"`
}

func memberKeys(t *testing.T, c *Core) memberKeysAnswer {
	t.Helper()
	raw, err := c.MemberKeys()
	if err != nil {
		t.Fatalf("MemberKeys: %v", err)
	}
	var k memberKeysAnswer
	if err := json.Unmarshal([]byte(raw), &k); err != nil {
		t.Fatal(err)
	}
	return k
}

type remoteAnswer struct {
	Owner       bool                       `json:"owner"`
	OwnerOnly   bool                       `json:"ownerOnly"`
	Relays      []struct{ AgentID string } `json:"relays"`
	Delegations []struct {
		KeyID     string    `json:"keyId"`
		Scope     string    `json:"scope"`
		ExpiresAt time.Time `json:"expiresAt"`
		Label     string    `json:"label"`
	} `json:"delegations"`
	CoOwners []struct {
		KeyID string `json:"keyId"`
		Label string `json:"label"`
	} `json:"coOwners"`
	HubKey *struct {
		KeyID string `json:"keyId"`
		Label string `json:"label"`
	} `json:"hubKey"`
	Member *struct {
		KeyID     string    `json:"keyId"`
		State     string    `json:"state"`
		Scope     string    `json:"scope"`
		ExpiresAt time.Time `json:"expiresAt"`
		Control   bool      `json:"control"`
		Code      string    `json:"code"`
		CoOwner   bool      `json:"coOwner"`
	} `json:"member"`
}

func remoteView(t *testing.T, c *Core, tenant, bundle string) remoteAnswer {
	t.Helper()
	raw, err := c.RemoteRelays(tenant, bundle)
	if err != nil {
		t.Fatalf("RemoteRelays: %v", err)
	}
	var a remoteAnswer
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	return a
}

// household is an owner's core with a relay paired, and a member's core.
type memberHousehold struct {
	owner, member *Core
	clock         *fakeClock
	relay         *trust.Identity
	bundle        string
	keys          memberKeysAnswer
}

func newMemberHousehold(t *testing.T) *memberHousehold {
	t.Helper()
	h := &memberHousehold{owner: newCore(t, nil), member: newCore(t, nil), clock: &fakeClock{t: memberT0}, relay: newRelay(t)}
	h.owner.gate.now = h.clock.now
	h.member.gate.now = h.clock.now
	h.bundle = bundleWith(t, h.owner, credTenant, h.relay)
	h.keys = memberKeys(t, h.member)
	return h
}

// sign is the owner's app signing the next bundle with a change.
func (h *memberHousehold) sign(t *testing.T, change map[string]any) {
	t.Helper()
	ch, _ := json.Marshal(change)
	out, err := h.owner.SignTrustBundle(credTenant, h.bundle, string(ch))
	if err != nil {
		t.Fatalf("SignTrustBundle(%s): %v", ch, err)
	}
	h.bundle = out
}

func (h *memberHousehold) delegate(t *testing.T, scope string) {
	t.Helper()
	h.sign(t, map[string]any{"delegate": map[string]any{
		"ed25519": h.keys.Ed25519, "x25519": h.keys.X25519, "scope": scope, "label": "Awa",
	}})
}

func (h *memberHousehold) memberBlock(boxID string) error {
	_, err := h.member.SignMemberCommand(credTenant, h.bundle, remoteMAC, "block", boxID)
	return err
}

func TestMemberKeys_CreatedOnceAndKeptInTheVault(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(dir, "member-vault-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	first := memberKeys(t, c)
	if first.KeyID == "" || len(first.Ed25519) != 32 || len(first.X25519) != 32 {
		t.Fatalf("member keys = %+v", first)
	}
	if again := memberKeys(t, c); again.KeyID != first.KeyID {
		t.Fatal("a second call made new keys")
	}
	owner := ownerKeysOf(t, c)
	if owner == first.KeyID {
		t.Fatal("the member key is the owner key: a phone that owns one household and is a member of another must not mix them")
	}
	_ = c.Close()

	reopened, err := Open(dir, "member-vault-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if k := memberKeys(t, reopened); k.KeyID != first.KeyID {
		t.Fatal("the member key did not survive a restart")
	}
	raw, _ := reopened.MemberKeys()
	if strings.Contains(raw, "priv") || strings.Contains(raw, "seed") {
		t.Fatalf("MemberKeys leaks private material: %s", raw)
	}
}

func ownerKeysOf(t *testing.T, c *Core) string {
	t.Helper()
	raw, err := c.OwnerKeys()
	if err != nil {
		t.Fatal(err)
	}
	var k memberKeysAnswer
	_ = json.Unmarshal([]byte(raw), &k)
	return k.KeyID
}

func TestDelegation_SignedByTheOwnerOpensRemoteModeForTheMember(t *testing.T) {
	h := newMemberHousehold(t)

	// Before any delegation the member's phone stays local, and shows the
	// six digits the owner's screen shows for its key.
	before := remoteView(t, h.member, credTenant, h.bundle)
	if before.Owner || len(before.Relays) != 0 || before.Member == nil || before.Member.State != "none" {
		t.Fatalf("before the delegation: %+v", before)
	}
	ownerCode, err := h.owner.PairingCode(base64.StdEncoding.EncodeToString(h.keys.Ed25519), base64.StdEncoding.EncodeToString(h.keys.X25519))
	if err != nil {
		t.Fatal(err)
	}
	if before.Member.Code == "" || before.Member.Code != ownerCode {
		t.Fatalf("the member's code %q, the owner's %q", before.Member.Code, ownerCode)
	}

	h.delegate(t, trust.ScopeView)
	o := remoteView(t, h.owner, credTenant, h.bundle)
	if !o.Owner || len(o.Delegations) != 1 || o.Delegations[0].KeyID != h.keys.KeyID || o.Delegations[0].Scope != "view" ||
		!o.Delegations[0].ExpiresAt.Equal(memberT0.Add(trust.DelegationTTL)) || o.Delegations[0].Label != "Awa" {
		t.Fatalf("the owner's view of the delegation: %+v", o)
	}
	m := remoteView(t, h.member, credTenant, h.bundle)
	if m.Owner || m.Member == nil || m.Member.State != "active" || m.Member.Scope != "view" || m.Member.Control || len(m.Relays) != 1 {
		t.Fatalf("a view member: %+v", m)
	}
	// View lets the member read the relay's list, never sign a write.
	snap := signedSnapshot(t, h.relay, credTenant, "devices", agentName(0), `[]`, memberT0)
	if _, err := h.member.OpenDeviceSnapshot(credTenant, h.bundle, snap); err != nil {
		t.Fatalf("a view member reading the list: %v", err)
	}
	if err := h.memberBlock(""); err == nil || !strings.HasPrefix(err.Error(), "not_delegated") {
		t.Fatalf("a view member's block = %v, want not_delegated", err)
	}

	// Control: the member signs with its own key, and the command names it.
	h.delegate(t, trust.ScopeControl)
	h.member.gate.demo = true // the paid-action gate is tested below
	raw, err := h.member.SignMemberCommand(credTenant, h.bundle, remoteMAC, "block", "")
	if err != nil {
		t.Fatalf("a control member's block: %v", err)
	}
	var signed struct {
		Payload []byte `json:"payload"`
		Sig     []byte `json:"sig"`
	}
	_ = json.Unmarshal([]byte(raw), &signed)
	cmd, err := trust.SignedCommand{Payload: signed.Payload, Sig: signed.Sig}.Open(trust.DomainCommand, h.keys.Ed25519)
	if err != nil || cmd.KeyID != h.keys.KeyID || cmd.Path != "/api/devices/ac:bb:cc:00:00:22/block" {
		t.Fatalf("the member's command: %+v, %v", cmd, err)
	}
	// The relay the owner paired accepts it.
	v := trust.NewVerifier(credTenant, ownerSigningKey(t, h.owner), memberT0.Add(-time.Minute))
	if err := v.AcceptBundle(openSigned(t, h.bundle)); err != nil {
		t.Fatal(err)
	}
	if _, err := v.VerifyCommand(trust.SignedCommand{Payload: signed.Payload, Sig: signed.Sig}, nil, memberT0); err != nil {
		t.Fatalf("the relay refuses the member's command: %v", err)
	}
	// The member's result is believed on the relay's signature.
	res, _ := trust.NewResult(h.relay, credTenant, cmd.CmdID, 1, 200, []byte(`{"ok":true}`), memberT0)
	rawRes, _ := json.Marshal(res)
	if _, err := h.member.VerifyCommandResult(credTenant, cmd.CmdID, h.bundle, string(rawRes)); err != nil {
		t.Fatalf("the member verifying the relay's result: %v", err)
	}
}

func TestDelegation_PaidActionGateStillApplies(t *testing.T) {
	h := newMemberHousehold(t)
	h.delegate(t, trust.ScopeControl)
	err := h.memberBlock("ZTEG00000001")
	if err == nil || !strings.HasPrefix(err.Error(), "not_entitled") {
		t.Fatalf("a member's block with no pass = %v, want not_entitled", err)
	}
	// Unblocking is free forever.
	if _, err := h.member.SignMemberCommand(credTenant, h.bundle, remoteMAC, "unblock", "ZTEG00000001"); err != nil {
		t.Fatalf("a member's unblock: %v", err)
	}
	_ = entitlement.FeatureBlock
}

func TestDelegation_RevokedOrOwnerOnly(t *testing.T) {
	h := newMemberHousehold(t)
	h.member.gate.demo = true
	h.delegate(t, trust.ScopeControl)

	h.sign(t, map[string]any{"ownerOnly": true})
	m := remoteView(t, h.member, credTenant, h.bundle)
	if !m.OwnerOnly || m.Member == nil || m.Member.State != "active" || m.Member.Control {
		t.Fatalf("owner-only: %+v", m)
	}
	if err := h.memberBlock(""); err == nil || !strings.HasPrefix(err.Error(), "owner_only") {
		t.Fatalf("a member's block in an owner-only household = %v, want owner_only", err)
	}
	if o := remoteView(t, h.owner, credTenant, h.bundle); !o.OwnerOnly || len(o.Delegations) != 1 {
		t.Fatalf("owner-only keeps the delegation for when it is turned off: %+v", o)
	}
	h.sign(t, map[string]any{"ownerOnly": false})
	if err := h.memberBlock(""); err != nil {
		t.Fatalf("owner-only off: %v", err)
	}

	h.sign(t, map[string]any{"revoke": h.keys.KeyID})
	if o := remoteView(t, h.owner, credTenant, h.bundle); len(o.Delegations) != 0 {
		t.Fatalf("after revoking, the owner still lists %+v", o.Delegations)
	}
	m = remoteView(t, h.member, credTenant, h.bundle)
	if m.Member == nil || m.Member.State != "revoked" || len(m.Relays) != 0 {
		t.Fatalf("a revoked member: %+v", m)
	}
	if err := h.memberBlock(""); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("a revoked member's block = %v", err)
	}
	snap := signedSnapshot(t, h.relay, credTenant, "devices", agentName(0), `[]`, memberT0)
	if _, err := h.member.OpenDeviceSnapshot(credTenant, h.bundle, snap); err == nil {
		t.Fatal("a revoked member's core still believes the relay's list")
	}
}

func TestDelegation_RenewedBeforeItExpires(t *testing.T) {
	h := newMemberHousehold(t)
	h.member.gate.demo = true
	h.delegate(t, trust.ScopeControl)

	// Three weeks in, nothing is due: no new bundle.
	h.clock.set(memberT0.Add(21 * 24 * time.Hour))
	if out, err := h.owner.RenewDelegations(credTenant, h.bundle); err != nil || out != "" {
		t.Fatalf("day 21: %q, %v; want nothing to sign", out, err)
	}
	// Day 25: within the week before the end, the owner's app renews it.
	day25 := memberT0.Add(25 * 24 * time.Hour)
	h.clock.set(day25)
	out, err := h.owner.RenewDelegations(credTenant, h.bundle)
	if err != nil || out == "" {
		t.Fatalf("day 25: %q, %v; want a renewed bundle", out, err)
	}
	old := h.bundle
	h.bundle = out
	o := remoteView(t, h.owner, credTenant, h.bundle)
	if len(o.Delegations) != 1 || !o.Delegations[0].ExpiresAt.Equal(day25.Add(trust.DelegationTTL)) {
		t.Fatalf("renewed delegation: %+v", o.Delegations)
	}
	if bundleVersion(t, out) <= bundleVersion(t, old) {
		t.Fatal("the renewed bundle's version did not move")
	}

	// Day 40: the old bundle would have run out; the renewed one has not.
	day40 := memberT0.Add(40 * 24 * time.Hour)
	h.clock.set(day40)
	if m := remoteView(t, h.member, credTenant, old); m.Member == nil || m.Member.State != "expired" || len(m.Relays) != 0 {
		t.Fatalf("day 40 on the old bundle: %+v", m.Member)
	}
	if _, err := h.member.SignMemberCommand(credTenant, old, remoteMAC, "block", ""); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("day 40 on the old bundle = %v, want expired", err)
	}
	if err := h.memberBlock(""); err != nil {
		t.Fatalf("day 40 on the renewed bundle: %v", err)
	}
	// A member's phone cannot renew anything: it holds no owner key for
	// this household, so it signs a bundle the relays refuse.
	if _, err := h.member.RenewDelegations(credTenant, h.bundle); err == nil {
		t.Fatal("a member's core renewed the owner's delegations")
	}
}

func TestDelegation_TheMemberPinsTheOwner(t *testing.T) {
	h := newMemberHousehold(t)
	h.delegate(t, trust.ScopeView)
	if m := remoteView(t, h.member, credTenant, h.bundle); m.Member == nil || m.Member.State != "active" {
		t.Fatalf("delegated: %+v", m.Member)
	}
	// A hub that makes up its own owner and delegates the member under it
	// is refused: the member pinned the owner whose bundle delegated it.
	forger := newCore(t, nil)
	forged := bundleWith(t, forger, credTenant, newRelay(t))
	ch, _ := json.Marshal(map[string]any{"delegate": map[string]any{"ed25519": h.keys.Ed25519, "x25519": h.keys.X25519, "scope": "control"}})
	forged, err := forger.SignTrustBundle(credTenant, forged, string(ch))
	if err != nil {
		t.Fatal(err)
	}
	m := remoteView(t, h.member, credTenant, forged)
	if m.Member == nil || m.Member.State != "other_owner" || len(m.Relays) != 0 {
		t.Fatalf("a bundle from another owner key: %+v", m)
	}
	if err := func() error {
		_, err := h.member.SignMemberCommand(credTenant, forged, remoteMAC, "unblock", "")
		return err
	}(); err == nil {
		t.Fatal("the member signed against a bundle another key made")
	}
}

func TestDelegate_RefusedForAKeyTheOwnerHolds(t *testing.T) {
	h := newMemberHousehold(t)
	raw, _ := h.owner.OwnerKeys()
	var own memberKeysAnswer
	_ = json.Unmarshal([]byte(raw), &own)
	ch, _ := json.Marshal(map[string]any{"delegate": map[string]any{"ed25519": own.Ed25519, "x25519": own.X25519, "scope": "control"}})
	if _, err := h.owner.SignTrustBundle(credTenant, h.bundle, string(ch)); err == nil {
		t.Fatal("the owner delegated its own key")
	}
	ch, _ = json.Marshal(map[string]any{"delegate": map[string]any{"ed25519": h.keys.Ed25519, "x25519": h.keys.X25519, "scope": "hub_block"}})
	if _, err := h.owner.SignTrustBundle(credTenant, h.bundle, string(ch)); err == nil {
		t.Fatal("a member was given the hub key's scope")
	}
}

func ownerSigningKey(t *testing.T, c *Core) []byte {
	t.Helper()
	raw, _ := c.OwnerKeys()
	var k memberKeysAnswer
	_ = json.Unmarshal([]byte(raw), &k)
	return k.Ed25519
}

func openSigned(t *testing.T, bundle string) trust.Signed[trust.Bundle] {
	t.Helper()
	var s signedJSON
	if err := json.Unmarshal([]byte(bundle), &s); err != nil {
		t.Fatal(err)
	}
	return trust.Signed[trust.Bundle]{Payload: s.Payload, Sig: s.Sig}
}

func bundleVersion(t *testing.T, bundle string) uint64 {
	t.Helper()
	b, err := openSigned(t, bundle).Unverified()
	if err != nil {
		t.Fatal(err)
	}
	return b.Version
}
