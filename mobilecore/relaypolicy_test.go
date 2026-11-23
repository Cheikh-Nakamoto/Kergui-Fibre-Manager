package mobilecore

import (
	"encoding/json"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// The relay policy (#36): the owner's core signs the auto-reject rule as it
// stands in its own database (on/off, since, the accepted MACs) and the
// entitlement token it holds, for the household's relays. JS only carries
// it to PUT …/relay-policy. Nothing new to say is "": the app sends nothing.

// signPolicy is SignRelayPolicy, decoded; ok is false for "".
func signPolicy(t *testing.T, c *Core, tenant string, bundle signedAnswer, current *signedAnswer) (signedAnswer, bool) {
	t.Helper()
	b, _ := json.Marshal(bundle)
	cur := ""
	if current != nil {
		raw, _ := json.Marshal(current)
		cur = string(raw)
	}
	raw, err := c.SignRelayPolicy(tenant, string(b), cur)
	if err != nil {
		t.Fatalf("SignRelayPolicy: %v", err)
	}
	if raw == "" {
		return signedAnswer{}, false
	}
	var out signedAnswer
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode policy: %v\n%s", err, raw)
	}
	return out, true
}

// openPolicy is what a relay does with it: the owner's key it pinned.
func openPolicy(t *testing.T, c *Core, tenant string, p signedAnswer) trust.Policy {
	t.Helper()
	v := trust.NewVerifier(tenant, ownerKeys(t, c).Ed25519, time.Now())
	got, err := v.AcceptPolicy(trust.Signed[trust.Policy]{Payload: p.Payload, Sig: p.Sig})
	if err != nil {
		t.Fatalf("a relay refuses the policy: %v", err)
	}
	if got.Version != p.Version {
		t.Fatalf("answer version %d, policy version %d", p.Version, got.Version)
	}
	return got
}

func acceptedMACs(t *testing.T, c *Core) []string {
	t.Helper()
	var out []string
	for _, d := range autoRejectState(t, c).Devices {
		if d.State == "accepted" {
			out = append(out, d.MAC)
		}
	}
	sort.Strings(out)
	return out
}

func sameStrings(a, b []string) bool {
	a, b = append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSignRelayPolicy_TheRuleAndTheTokenAsTheCoreHoldsThem(t *testing.T) {
	c, _, box := autoRejectCore(t)
	maxToken := signToken(t, testPriv, entitlement.TierMax, mockSerial, gateT0)
	c.SetEntitlement(maxToken)
	if status, body := setAutoReject(t, c, true); status != http.StatusOK {
		t.Fatalf("turn on = %d %s", status, body)
	}
	bundle, _ := signBundle(t, c, "T1", nil, nil)

	first, ok := signPolicy(t, c, "T1", bundle, nil)
	if !ok {
		t.Fatal("nothing was signed for a household without a policy")
	}
	p := openPolicy(t, c, "T1", first)
	if p.Version != 1 || p.TenantID != "T1" || !p.AutoReject.Enabled || p.Entitlement != maxToken {
		t.Fatalf("first policy = %+v", p)
	}
	if p.AutoReject.Since.IsZero() {
		t.Fatal("the policy does not say since when the rule is on")
	}
	if want := acceptedMACs(t, c); len(want) == 0 || !sameStrings(p.AutoReject.Accepted, want) {
		t.Fatalf("accepted = %v, want the core's %v", p.AutoReject.Accepted, want)
	}

	// The hub holds it: the same rule and token again is nothing to send.
	if _, ok := signPolicy(t, c, "T1", bundle, &first); ok {
		t.Fatal("an unchanged rule was signed again")
	}

	// A device accepted on the phone reaches the relay's list.
	const visitor = "02:00:00:00:00:d1"
	box.Join(mockrouter.Station{MAC: visitor, IP: "192.168.1.241"})
	runAutoReject(t, c)
	if status, body := call(t, c, "POST", "/api/devices/"+visitor+"/accept", ""); status != http.StatusOK {
		t.Fatalf("accept = %d %s", status, body)
	}
	second, ok := signPolicy(t, c, "T1", bundle, &first)
	if !ok {
		t.Fatal("an accepted device did not move the policy")
	}
	p2 := openPolicy(t, c, "T1", second)
	if p2.Version != 2 || !sameStrings(p2.AutoReject.Accepted, acceptedMACs(t, c)) {
		t.Fatalf("second policy = %+v", p2)
	}

	// A new token (renewed pass) moves it too.
	renewed := signToken(t, testPriv, entitlement.TierMax, mockSerial, gateT0.Add(time.Hour))
	c.SetEntitlement(renewed)
	third, ok := signPolicy(t, c, "T1", bundle, &second)
	if !ok || openPolicy(t, c, "T1", third).Entitlement != renewed {
		t.Fatal("a new token did not reach the policy")
	}

	// Off: the relay is told to stop.
	if status, body := setAutoReject(t, c, false); status != http.StatusOK {
		t.Fatalf("turn off = %d %s", status, body)
	}
	fourth, ok := signPolicy(t, c, "T1", bundle, &third)
	if !ok || openPolicy(t, c, "T1", fourth).AutoReject.Enabled {
		t.Fatal("turning the rule off did not reach the policy")
	}
}

func TestSignRelayPolicy_OnlyTheOwnersPhoneSignsIt(t *testing.T) {
	c, _, _ := autoRejectCore(t)
	c.SetEntitlement(signToken(t, testPriv, entitlement.TierMax, mockSerial, gateT0))
	if status, body := setAutoReject(t, c, true); status != http.StatusOK {
		t.Fatalf("turn on = %d %s", status, body)
	}

	// No bundle: no relay to tell, nothing signed.
	if raw, err := c.SignRelayPolicy("T1", "", ""); err != nil || raw != "" {
		t.Fatalf("without a bundle = %q, %v", raw, err)
	}

	// Another phone's bundle (a member, or the owner's other phone): this
	// phone is not the owner, and its signature would be refused anyway.
	other := newCore(t, nil)
	theirs, _ := signBundle(t, other, "T1", nil, nil)
	if _, ok := signPolicy(t, c, "T1", theirs, nil); ok {
		t.Fatal("a phone that is not the owner signed a policy")
	}

	// The owner's bundle for another household neither.
	mine, _ := signBundle(t, c, "T2", nil, nil)
	if _, ok := signPolicy(t, c, "T1", mine, nil); ok {
		t.Fatal("a policy was signed on another household's bundle")
	}
}

func TestSignRelayPolicy_VersionNeverGoesBack(t *testing.T) {
	c, _, _ := autoRejectCore(t)
	c.SetEntitlement(signToken(t, testPriv, entitlement.TierMax, mockSerial, gateT0))
	if status, body := setAutoReject(t, c, true); status != http.StatusOK {
		t.Fatalf("turn on = %d %s", status, body)
	}
	bundle, _ := signBundle(t, c, "T1", nil, nil)
	first, _ := signPolicy(t, c, "T1", bundle, nil)

	// The upload was lost: the hub has none. The next one is still above.
	second, ok := signPolicy(t, c, "T1", bundle, nil)
	if !ok || second.Version != 2 {
		t.Fatalf("after a lost upload: %+v %v, want v2", second, ok)
	}

	// The hub serves the older one while the rule has moved since: signed
	// again, above both. (Served while it still says the same, it is
	// nothing new: the relay applies the same rule.)
	if status, body := setAutoReject(t, c, false); status != http.StatusOK {
		t.Fatalf("turn off = %d %s", status, body)
	}
	third, ok := signPolicy(t, c, "T1", bundle, &first)
	if !ok || third.Version != 3 {
		t.Fatalf("after a replayed policy: %+v %v, want v3", third, ok)
	}

	// The hub holds a higher version someone else signed (a former owner
	// key): this phone signs above it, and never calls it its own.
	other, _, _ := autoRejectCore(t)
	otherBundle, _ := signBundle(t, other, "T1", nil, nil)
	other.SetEntitlement(signToken(t, testPriv, entitlement.TierMax, mockSerial, gateT0))
	var foreign signedAnswer
	for i := 0; i < 10; i++ {
		foreign, _ = signPolicy(t, other, "T1", otherBundle, nil)
	}
	next, ok := signPolicy(t, c, "T1", bundle, &foreign)
	if !ok || next.Version != foreign.Version+1 {
		t.Fatalf("above a foreign v%d: %+v %v", foreign.Version, next, ok)
	}
}

func TestSignRelayPolicy_NeedsAHousehold(t *testing.T) {
	c := newCore(t, nil)
	if _, err := c.SignRelayPolicy("  ", "", ""); err == nil {
		t.Fatal("a policy without a household was accepted")
	}
}

// TestSignRelayPolicy_NeverInTheDemo: the demo box's rule is not the
// household's; a signed-in owner exploring the demo publishes nothing.
func TestSignRelayPolicy_NeverInTheDemo(t *testing.T) {
	c := newCore(t, nil)
	if err := c.StartDemo(); err != nil {
		t.Fatal(err)
	}
	if status, body := call(t, c, "POST", "/api/login", `{"username":"admin","password":"admin"}`); status != http.StatusOK {
		t.Fatalf("login = %d %s", status, body)
	}
	if status, body := setAutoReject(t, c, true); status != http.StatusOK {
		t.Fatalf("turn on in the demo = %d %s", status, body)
	}
	bundle, _ := signBundle(t, c, "T1", nil, nil)
	if _, ok := signPolicy(t, c, "T1", bundle, nil); ok {
		t.Fatal("the demo's rule was signed for the household")
	}
}
