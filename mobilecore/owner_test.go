package mobilecore

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

type ownerKeysAnswer struct {
	KeyID   string `json:"keyId"`
	Ed25519 []byte `json:"ed25519"`
	X25519  []byte `json:"x25519"`
}

func ownerKeys(t *testing.T, c *Core) ownerKeysAnswer {
	t.Helper()
	raw, err := c.OwnerKeys()
	if err != nil {
		t.Fatalf("OwnerKeys: %v", err)
	}
	var k ownerKeysAnswer
	if err := json.Unmarshal([]byte(raw), &k); err != nil {
		t.Fatalf("decode owner keys: %v\n%s", err, raw)
	}
	return k
}

func TestOwnerKeys_CreatedOnceAndSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(dir, "test-vault-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	first := ownerKeys(t, c)
	if len(first.Ed25519) != 32 || len(first.X25519) != 32 || first.KeyID == "" {
		t.Fatalf("owner keys are incomplete: %+v", first)
	}
	if again := ownerKeys(t, c); again.KeyID != first.KeyID {
		t.Fatalf("a second call made new keys: %s then %s", first.KeyID, again.KeyID)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	// The app is closed and opened again: same database, same vault key.
	c2, err := Open(dir, "test-vault-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c2.Close() }()
	after := ownerKeys(t, c2)
	if after.KeyID != first.KeyID || !bytes.Equal(after.X25519, first.X25519) {
		t.Fatalf("the owner keys changed across a restart: %s then %s", first.KeyID, after.KeyID)
	}
}

func TestOwnerKeys_AreInTheVaultNotInTheClear(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(dir, "test-vault-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	ownerKeys(t, c)
	id, err := c.ownerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()

	// Another vault key cannot read them back.
	other, err := Open(dir, "another-vault-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if _, err := other.OwnerKeys(); err == nil {
		t.Fatal("the owner keys were readable with another vault key")
	}

	// And the private blob is nowhere in the answer JS receives.
	priv := id.Marshal()
	answer, _ := json.Marshal(ownerKeysView(id))
	for _, enc := range []string{base64.StdEncoding.EncodeToString(priv[1:33]), base64.StdEncoding.EncodeToString(priv[33:])} {
		if strings.Contains(string(answer), enc) {
			t.Fatal("the owner's private key reached the JS answer")
		}
	}
}

func TestOwnerKeys_TheJSAnswerHasPublicFieldsOnly(t *testing.T) {
	c := newCore(t, nil)
	raw, err := c.OwnerKeys()
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatal(err)
	}
	for k := range fields {
		switch k {
		case "keyId", "ed25519", "x25519":
		default:
			t.Errorf("unexpected field %q in the owner keys answer", k)
		}
	}
}

func TestPairingInvite_CarriesTheCodeAndTheOwnerFingerprint(t *testing.T) {
	c := newCore(t, nil)
	qr, err := c.PairingInvite("PAIRCODE-123")
	if err != nil {
		t.Fatal(err)
	}
	inv, err := trust.ParseInvite(qr)
	if err != nil {
		t.Fatalf("the QR does not parse: %v", err)
	}
	if inv.Code != "PAIRCODE-123" || inv.OwnerFingerprint != ownerKeys(t, c).KeyID {
		t.Fatalf("invite = %+v", inv)
	}
	if _, err := c.PairingInvite("  "); err == nil {
		t.Fatal("an empty pairing code made an invite")
	}
}

func TestPairingCode_MatchesTheRelaySideAndChangesWithASwappedKey(t *testing.T) {
	c := newCore(t, nil)
	owner := ownerKeys(t, c)
	relay, _ := trust.Generate(rand.Reader)
	pub := relay.Public()

	code, err := c.PairingCode(b64(pub.Ed25519), b64(pub.X25519))
	if err != nil {
		t.Fatal(err)
	}
	relaySide := trust.PairingCode(trust.PublicKeys{Ed25519: owner.Ed25519, X25519: owner.X25519}, pub)
	if code != relaySide || len(code) != 6 {
		t.Fatalf("owner screen %q, relay screen %q", code, relaySide)
	}

	// The hub hands the owner another key than the relay's own.
	swapped, _ := trust.Generate(rand.Reader)
	other, err := c.PairingCode(b64(swapped.Public().Ed25519), b64(pub.X25519))
	if err != nil {
		t.Fatal(err)
	}
	if other == relaySide {
		t.Fatal("a swapped relay key gave the same six digits")
	}

	if _, err := c.PairingCode("not base64!", b64(pub.X25519)); err == nil {
		t.Fatal("a malformed key gave a code")
	}
	if _, err := c.PairingCode(b64([]byte("short")), b64(pub.X25519)); err == nil {
		t.Fatal("a short key gave a code")
	}
}

type signedAnswer struct {
	Payload []byte `json:"payload"`
	Sig     []byte `json:"sig"`
	Version uint64 `json:"version"`
}

func signBundle(t *testing.T, c *Core, tenant string, current *signedAnswer, change map[string]any) (signedAnswer, trust.Bundle) {
	t.Helper()
	cur := ""
	if current != nil {
		b, _ := json.Marshal(current)
		cur = string(b)
	}
	ch, _ := json.Marshal(change)
	raw, err := c.SignTrustBundle(tenant, cur, string(ch))
	if err != nil {
		t.Fatalf("SignTrustBundle: %v", err)
	}
	var out signedAnswer
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	owner := ownerKeys(t, c)
	b, err := trust.Signed[trust.Bundle]{Payload: out.Payload, Sig: out.Sig}.Open(trust.DomainBundle, owner.Ed25519)
	if err != nil {
		t.Fatalf("the bundle is not signed by the owner key: %v", err)
	}
	if b.Version != out.Version {
		t.Fatalf("answer version %d, bundle version %d", out.Version, b.Version)
	}
	return out, b
}

func relayChange(id *trust.Identity, agentID, kind string) map[string]any {
	return map[string]any{"relay": map[string]any{
		"agentId": agentID, "kind": kind, "label": "PC du salon",
		"ed25519": id.Public().Ed25519, "x25519": id.Public().X25519,
	}}
}

func TestSignTrustBundle_FirstThenWithARelay_MonotonicVersions(t *testing.T) {
	c := newCore(t, nil)
	owner := ownerKeys(t, c)

	first, b1 := signBundle(t, c, "T1", nil, map[string]any{"routerPin": "ab12"})
	if b1.Version != 1 || b1.TenantID != "T1" || b1.Owner.KeyID() != owner.KeyID || len(b1.Relays) != 0 {
		t.Fatalf("first bundle = %+v", b1)
	}
	if b1.Network.RouterPin != "ab12" {
		t.Fatalf("the box pin is not in the bundle: %+v", b1.Network)
	}

	// A relay verifies it against the key it pinned from the QR.
	v := trust.NewVerifier("T1", owner.Ed25519, time.Now())
	if err := v.AcceptBundle(trust.Signed[trust.Bundle]{Payload: first.Payload, Sig: first.Sig}); err != nil {
		t.Fatalf("a relay refuses the first bundle: %v", err)
	}

	relay, _ := trust.Generate(rand.Reader)
	second, b2 := signBundle(t, c, "T1", &first, relayChange(relay, "A1", "pc"))
	if b2.Version != 2 || len(b2.Relays) != 1 || b2.Relays[0].AgentID != "A1" || b2.Relays[0].KeyID() != relay.KeyID() {
		t.Fatalf("second bundle = %+v", b2)
	}
	if b2.Network.RouterPin != "ab12" {
		t.Fatal("the second bundle lost what the first one said")
	}
	if err := v.AcceptBundle(trust.Signed[trust.Bundle]{Payload: second.Payload, Sig: second.Sig}); err != nil {
		t.Fatalf("a relay refuses the second bundle: %v", err)
	}

	// The same relay again replaces it rather than listing it twice.
	_, b3 := signBundle(t, c, "T1", &second, relayChange(relay, "A1", "pc"))
	if b3.Version != 3 || len(b3.Relays) != 1 {
		t.Fatalf("third bundle = %+v", b3)
	}
}

func TestSignTrustBundle_NeverGoesBackEvenIfTheHubDoes(t *testing.T) {
	c := newCore(t, nil)
	first, _ := signBundle(t, c, "T1", nil, nil)
	second, _ := signBundle(t, c, "T1", &first, nil)
	_ = second
	// The hub answers with the older bundle (or none at all): the next
	// version still goes up, so no relay may be walked back.
	_, b := signBundle(t, c, "T1", &first, nil)
	if b.Version != 3 {
		t.Fatalf("version after a replayed bundle = %d, want 3", b.Version)
	}
	_, b = signBundle(t, c, "T1", nil, nil)
	if b.Version != 4 {
		t.Fatalf("version after a missing bundle = %d, want 4", b.Version)
	}
}

func TestSignTrustBundle_VersionSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	c, _ := Open(dir, "test-vault-key", nil)
	signBundle(t, c, "T1", nil, nil)
	signBundle(t, c, "T1", nil, nil)
	_ = c.Close()
	c2, _ := Open(dir, "test-vault-key", nil)
	defer func() { _ = c2.Close() }()
	if _, b := signBundle(t, c2, "T1", nil, nil); b.Version != 3 {
		t.Fatalf("version after a restart = %d, want 3", b.Version)
	}
}

func TestSignTrustBundle_ABundleTheOwnerDidNotSignIsNotBuiltOn(t *testing.T) {
	c := newCore(t, nil)
	// The hub stores a bundle signed by its own key, listing its own relay.
	hubKey, _ := trust.Generate(rand.Reader)
	hubRelay, _ := trust.Generate(rand.Reader)
	forged, _ := trust.Sign(hubKey, trust.DomainBundle, trust.Bundle{
		TenantID: "T1", Version: 7, Owner: hubKey.Public(),
		Relays: []trust.Relay{{AgentID: "EVIL", Kind: "pc", Keys: hubRelay.Public()}},
	})
	_, b := signBundle(t, c, "T1", &signedAnswer{Payload: forged.Payload, Sig: forged.Sig, Version: 7}, nil)
	if len(b.Relays) != 0 {
		t.Fatalf("the owner signed the hub's relay in: %+v", b.Relays)
	}
	if b.Version <= 7 {
		t.Fatalf("version %d does not supersede the stored one", b.Version)
	}
}

func TestSignTrustBundle_RefusesMalformedInput(t *testing.T) {
	c := newCore(t, nil)
	if _, err := c.SignTrustBundle("", "", ""); err == nil {
		t.Fatal("signed a bundle without a household")
	}
	if _, err := c.SignTrustBundle("T1", "", `{"relay":{"agentId":"A1","ed25519":"AAAA","x25519":"AAAA"}}`); err == nil {
		t.Fatal("signed a relay with malformed keys in")
	}
	if _, err := c.SignTrustBundle("T1", "{", ""); err == nil {
		t.Fatal("accepted a current bundle that is not JSON")
	}
}

func TestDemoRelay_PinsTheOwnerAndShowsTheSameCode(t *testing.T) {
	c := newCore(t, nil)
	qr, err := c.PairingInvite("DEMO")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.DemoRelayRedeem(qr)
	if err != nil {
		t.Fatal(err)
	}
	var relay struct {
		AgentID string `json:"agentId"`
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		Ed25519 []byte `json:"ed25519"`
		X25519  []byte `json:"x25519"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal([]byte(raw), &relay); err != nil {
		t.Fatal(err)
	}
	owner, err := c.PairingCode(b64(relay.Ed25519), b64(relay.X25519))
	if err != nil {
		t.Fatal(err)
	}
	if relay.Code != owner || relay.AgentID == "" {
		t.Fatalf("demo relay %+v, owner code %s", relay, owner)
	}

	// An invite for someone else's key is refused, as a real relay would.
	other, _ := trust.Generate(rand.Reader)
	bad := trust.Invite{Code: "DEMO", OwnerFingerprint: other.KeyID()}.String()
	if _, err := c.DemoRelayRedeem(bad); !errors.Is(err, trust.ErrUnknownKey) {
		t.Fatalf("an invite for another owner = %v, want ErrUnknownKey", err)
	}
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
