package mobilecore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Recovery of a lost owner phone (issue #48), the phones' side: the owner's
// core names a co-owner; the co-owner's core takes the household over; or a
// new phone proves the box password to the relay at home. Each time a relay
// holding the previous bundle follows the new one, and refuses the old owner
// key from then on.

type recoveryAnswer struct {
	TenantID string       `json:"tenantId"`
	Bundle   signedAnswer `json:"bundle"`
	Revoked  string       `json:"revoked"`
}

func decodeRecovery(t *testing.T, raw string) (recoveryAnswer, string) {
	t.Helper()
	var a recoveryAnswer
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	b, _ := json.Marshal(a.Bundle)
	return a, string(b)
}

// relayVerifier is a relay that paired with the owner and holds bundle.
func relayVerifier(t *testing.T, ownerCore *Core, bundle string, now time.Time) *trust.Verifier {
	t.Helper()
	v := trust.NewVerifier(credTenant, ownerKeys(t, ownerCore).Ed25519, now)
	if err := v.AcceptBundle(signedOf(t, bundle)); err != nil {
		t.Fatalf("the relay refused the owner's bundle: %v", err)
	}
	return v
}

func signedOf(t *testing.T, bundle string) trust.Signed[trust.Bundle] {
	t.Helper()
	var s signedAnswer
	if err := json.Unmarshal([]byte(bundle), &s); err != nil {
		t.Fatal(err)
	}
	return trust.Signed[trust.Bundle]{Payload: s.Payload, Sig: s.Sig}
}

// blockFrom is a block of remoteMAC signed by core's owner key, as the relay checks it.
func blockFrom(t *testing.T, c *Core, v *trust.Verifier, now time.Time) error {
	t.Helper()
	id, err := c.ownerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	sc, err := trust.NewCommand(id, credTenant, http.MethodPost, "/api/devices/"+remoteMAC+"/block", nil, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.VerifyCommand(sc, nil, now)
	return err
}

func TestRecovery_CoOwnerTakesTheHouseholdOver(t *testing.T) {
	h := newMemberHousehold(t)
	other := newCore(t, nil) // a member with a control delegation
	other.gate.now = h.clock.now
	otherKeys := memberKeys(t, other)
	h.sign(t, map[string]any{"delegate": map[string]any{
		"ed25519": otherKeys.Ed25519, "x25519": otherKeys.X25519, "scope": "control", "label": "Moussa",
	}})
	// Before anyone is a co-owner, nobody can take over.
	if _, err := h.member.TakeOverHousehold(credTenant, h.bundle); err == nil || !strings.HasPrefix(err.Error(), codeNotCoOwner) {
		t.Fatalf("a takeover by a non co-owner = %v, want %s", err, codeNotCoOwner)
	}

	h.sign(t, map[string]any{"coOwner": map[string]any{"ed25519": h.keys.Ed25519, "x25519": h.keys.X25519, "label": "Awa"}})
	ov := remoteView(t, h.owner, credTenant, h.bundle)
	if len(ov.CoOwners) != 1 || ov.CoOwners[0].KeyID != h.keys.KeyID || ov.CoOwners[0].Label != "Awa" {
		t.Fatalf("the owner's view of the co-owners = %+v", ov.CoOwners)
	}
	if mv := remoteView(t, h.member, credTenant, h.bundle); mv.Member == nil || !mv.Member.CoOwner {
		t.Fatalf("the co-owner's view = %+v", mv.Member)
	}
	if mv := remoteView(t, other, credTenant, h.bundle); mv.Member == nil || mv.Member.State != memberActive || mv.Member.CoOwner {
		t.Fatalf("the other member's view = %+v", mv.Member)
	}
	now := h.clock.now()
	relay := relayVerifier(t, h.owner, h.bundle, now)
	if err := blockFrom(t, h.owner, relay, now); err != nil {
		t.Fatalf("the owner's block before = %v", err)
	}

	// The owner's phone is lost: the co-owner's phone takes over.
	raw, err := h.member.TakeOverHousehold(credTenant, h.bundle)
	if err != nil {
		t.Fatalf("TakeOverHousehold: %v", err)
	}
	got, next := decodeRecovery(t, raw)
	oldOwner := ownerKeys(t, h.owner)
	if got.TenantID != credTenant || got.Revoked != oldOwner.KeyID {
		t.Fatalf("answer = %+v", got)
	}
	if err := relay.AcceptBundle(signedOf(t, next)); err != nil {
		t.Fatalf("the relay refused the co-owner's succession: %v", err)
	}
	newOwner := ownerKeys(t, h.member)
	if relay.OwnerKeyID() != newOwner.KeyID {
		t.Fatalf("the relay pins %s, want %s", relay.OwnerKeyID(), newOwner.KeyID)
	}
	if err := blockFrom(t, h.member, relay, now); err != nil {
		t.Fatalf("the new owner's block = %v", err)
	}
	if err := blockFrom(t, h.owner, relay, now); !errors.Is(err, trust.ErrRevoked) {
		t.Fatalf("the old owner's block = %v, want ErrRevoked", err)
	}

	// The co-owner's phone is the owner now; it keeps signing on top.
	if v := remoteView(t, h.member, credTenant, next); !v.Owner || len(v.Relays) != 1 {
		t.Fatalf("the new owner's view = %+v", v)
	}
	if _, b := signBundle(t, h.member, credTenant, ptr(signedOf(t, next)), map[string]any{"ownerOnly": false}); b.Succession == nil || b.Version <= got.Bundle.Version {
		t.Fatalf("a later bundle drops the handover or goes back: v%d", b.Version)
	}
	// The other member follows the handover: its delegation still holds.
	if v := remoteView(t, other, credTenant, next); v.Member == nil || v.Member.State != memberActive || !v.Member.Control {
		t.Fatalf("the other member after the takeover = %+v", v.Member)
	}
	if _, err := other.SignMemberCommand(credTenant, next, remoteMAC, "block", ""); err != nil && !strings.HasPrefix(err.Error(), "not_entitled") {
		t.Fatalf("the other member's block after the takeover = %v", err)
	}
	// The old owner's core no longer reads the bundle as its own.
	if v := remoteView(t, h.owner, credTenant, next); v.Owner {
		t.Fatal("the old owner still owns the household")
	}
}

func ptr(s trust.Signed[trust.Bundle]) *signedAnswer {
	return &signedAnswer{Payload: s.Payload, Sig: s.Sig}
}

// A member that never held the previous bundle cannot tell a succession
// from a hub's invention: it stays where it was.
func TestRecovery_AMemberIgnoresAnOwnerChangeItCannotCheck(t *testing.T) {
	h := newMemberHousehold(t)
	h.delegate(t, "control")
	_ = remoteView(t, h.member, credTenant, h.bundle) // pins the owner, keeps the bundle
	hubCore := newCore(t, nil)
	hubCore.gate.now = h.clock.now
	// The hub's own "owner" signs a bundle delegating the member.
	forged := bundleWith(t, hubCore, credTenant, newRelay(t))
	ch, _ := json.Marshal(map[string]any{"delegate": map[string]any{"ed25519": h.keys.Ed25519, "x25519": h.keys.X25519, "scope": "control"}})
	forged, err := hubCore.SignTrustBundle(credTenant, forged, string(ch))
	if err != nil {
		t.Fatal(err)
	}
	if v := remoteView(t, h.member, credTenant, forged); v.Member == nil || v.Member.State != memberOtherOwner {
		t.Fatalf("a bundle from another owner without a handover = %+v", v.Member)
	}
	if _, err := h.member.TakeOverHousehold(credTenant, forged); err == nil || !strings.HasPrefix(err.Error(), codeNotCoOwner) {
		t.Fatalf("a takeover on another owner's bundle = %v", err)
	}
}

// lanRelay is the household's relay listening for recovery, on 127.0.0.1.
type lanRelay struct {
	srv   *httptest.Server
	state *agent.MemoryState
}

func newLANRelay(t *testing.T, ownerCore *Core, relay *trust.Identity, bundle string, password string, now func() time.Time) *lanRelay {
	t.Helper()
	st := &agent.MemoryState{Data: agent.StateData{
		AgentID: agentName(0), TenantID: credTenant, Token: "kga_test",
		Identity: relay.Marshal(), OwnerKey: ownerKeys(t, ownerCore).Ed25519,
		Bundle: signedOf(t, bundle),
	}}
	rec := agent.NewRecovery(agent.RecoveryConfig{
		State:    st,
		Password: func(context.Context) (string, error) { return password, nil },
		Clock:    now,
	})
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	return &lanRelay{srv: srv, state: st}
}

func (l *lanRelay) addr() string { return strings.TrimPrefix(l.srv.URL, "http://") }

func TestRecovery_AtHomeTheBoxPasswordHandsTheHouseholdOver(t *testing.T) {
	h := newMemberHousehold(t)
	h.delegate(t, "control")
	_ = remoteView(t, h.member, credTenant, h.bundle)
	now := h.clock.now()
	relay := relayVerifier(t, h.owner, h.bundle, now)
	lan := newLANRelay(t, h.owner, h.relay, h.bundle, "Sene-Gal-2026", h.clock.now)

	newPhone := newCore(t, nil)
	newPhone.gate.now = h.clock.now

	// A wrong password: refused by the relay, nothing signed.
	if _, err := newPhone.RecoverAtHome(lan.addr(), "guess"); err == nil || !strings.HasPrefix(err.Error(), codeRecoveryProof) {
		t.Fatalf("a wrong password = %v, want %s", err, codeRecoveryProof)
	}

	raw, err := newPhone.RecoverAtHome(lan.addr(), "Sene-Gal-2026")
	if err != nil {
		t.Fatalf("RecoverAtHome: %v", err)
	}
	got, next := decodeRecovery(t, raw)
	if got.TenantID != credTenant || got.Revoked != ownerKeys(t, h.owner).KeyID {
		t.Fatalf("answer = %+v", got)
	}
	if err := relay.AcceptBundle(signedOf(t, next)); err != nil {
		t.Fatalf("the relay refused the new phone's bundle: %v", err)
	}
	if err := blockFrom(t, newPhone, relay, now); err != nil {
		t.Fatalf("the new owner's block = %v", err)
	}
	if err := blockFrom(t, h.owner, relay, now); !errors.Is(err, trust.ErrRevoked) {
		t.Fatalf("the old owner's block = %v, want ErrRevoked", err)
	}
	if v := remoteView(t, newPhone, credTenant, next); !v.Owner {
		t.Fatal("the new phone does not own the household")
	}
	if v := remoteView(t, h.member, credTenant, next); v.Member == nil || v.Member.State != memberActive {
		t.Fatalf("the member after the recovery = %+v", v.Member)
	}
}

// The proof never goes anywhere but the home network: an Internet address
// (the hub's, or any other) is refused before a byte is sent.
func TestRecovery_NeverTowardsTheInternet(t *testing.T) {
	c := newCore(t, nil)
	c.recoveryClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("the core called %s", r.URL)
		return nil, nil
	})}
	for _, addr := range []string{"203.0.113.7", "hub.kergui.org", "8.8.8.8:8791", "https://203.0.113.7/", "", "[2001:db8::1]:8791"} {
		if _, err := c.RecoverAtHome(addr, "Sene-Gal-2026"); err == nil || !strings.HasPrefix(err.Error(), codeRecoveryNotLAN) {
			t.Fatalf("RecoverAtHome(%q) = %v, want %s", addr, err, codeRecoveryNotLAN)
		}
	}
	for in, want := range map[string]string{
		"192.168.1.20":             "http://192.168.1.20:8791",
		" 192.168.1.20:9000 ":      "http://192.168.1.20:9000",
		"http://10.0.0.5/":         "http://10.0.0.5:8791",
		"[fe80::1]:8791":           "http://[fe80::1]:8791",
		"http://172.16.0.3:8791/":  "http://172.16.0.3:8791",
		"127.0.0.1:41234":          "http://127.0.0.1:41234",
		"https://192.168.100.1:80": "http://192.168.100.1:80",
	} {
		if got, err := recoveryURL(in); err != nil || got != want {
			t.Errorf("recoveryURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRecovery_TheOwnersPhoneDoesNotRecoverItsOwnHousehold(t *testing.T) {
	h := newMemberHousehold(t)
	lan := newLANRelay(t, h.owner, h.relay, h.bundle, "Sene-Gal-2026", h.clock.now)
	if _, err := h.owner.RecoverAtHome(lan.addr(), "Sene-Gal-2026"); err == nil || !strings.HasPrefix(err.Error(), codeRecoveryAlready) {
		t.Fatalf("the owner recovering its own household = %v", err)
	}
}

func TestSignTrustBundle_CoOwnersAddedAndRemoved(t *testing.T) {
	h := newMemberHousehold(t)
	h.sign(t, map[string]any{"coOwner": map[string]any{"ed25519": h.keys.Ed25519, "x25519": h.keys.X25519, "label": "Awa"}})
	if v := remoteView(t, h.owner, credTenant, h.bundle); len(v.CoOwners) != 1 {
		t.Fatalf("co-owners = %+v", v.CoOwners)
	}
	h.sign(t, map[string]any{"removeCoOwner": h.keys.KeyID})
	if v := remoteView(t, h.owner, credTenant, h.bundle); len(v.CoOwners) != 0 {
		t.Fatalf("co-owners after removal = %+v", v.CoOwners)
	}
	if _, err := h.member.TakeOverHousehold(credTenant, h.bundle); err == nil || !strings.HasPrefix(err.Error(), codeNotCoOwner) {
		t.Fatalf("a removed co-owner's takeover = %v", err)
	}
	// The owner's own key, or a relay's, cannot be a co-owner.
	ok := ownerKeys(t, h.owner)
	ch, _ := json.Marshal(map[string]any{"coOwner": map[string]any{"ed25519": ok.Ed25519, "x25519": ok.X25519}})
	if _, err := h.owner.SignTrustBundle(credTenant, h.bundle, string(ch)); err == nil {
		t.Fatal("the owner's own key was signed in as a co-owner")
	}
}
