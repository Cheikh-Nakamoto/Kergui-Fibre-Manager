package app

import (
	"encoding/base64"
	"net/http"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Auto-reject on the relay (#36), the hub's part against a real Postgres: it
// carries the owner-signed relay policy to the relay, and tells the phones
// whether the relay enforces the rule so they stand down.

// publishPolicy signs a relay policy as the owner's app does and uploads it.
func (h *household) publishPolicy(version uint64, enabled bool) (int, map[string]any) {
	h.t.Helper()
	signed, err := trust.Sign(h.ownerKey, trust.DomainPolicy, trust.Policy{
		TenantID: h.tenantID, Version: version, IssuedAt: time.Now().UTC(),
		AutoReject:  trust.AutoRejectPolicy{Enabled: enabled, Since: time.Now().UTC()},
		Entitlement: "a.token",
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return h.owner.do("PUT", h.path("relay-policy"), map[string]any{"payload": signed.Payload, "sig": signed.Sig})
}

// heartbeatClaiming is a heartbeat that says whether the relay enforces the
// rule.
func (r *testRelay) heartbeatClaiming(autoReject bool) map[string]any {
	r.t.Helper()
	status, body := r.do("POST", "/relay/v1/heartbeat", map[string]any{
		"wifi":           map[string]any{"ssid": "MonWiFi-2G", "bssid": ""},
		"routerIdentity": map[string]any{"pin": routerPin},
		"lanIp":          "192.168.1.50",
		"version":        "test",
		"autoReject":     autoReject,
	})
	if status != http.StatusOK {
		r.t.Fatalf("heartbeat = %d %v", status, body)
	}
	return body
}

func (h *household) enforcer() map[string]any {
	h.t.Helper()
	code, body := h.owner.do("GET", h.path("router/auto-reject"), nil)
	if code != http.StatusOK {
		h.t.Fatalf("GET router/auto-reject = %d %v", code, body)
	}
	return body
}

func TestAutoReject_RelayPolicyTravelsToTheRelay(t *testing.T) {
	srv := newTestHub(t)
	h := signupHousehold(t, srv)
	h.registerNetwork([]string{"MonWiFi-2G"})
	r := h.enroll(h.pairingCode("pc"))
	h.publishBundle(r.trustRelay("pc"))

	if body := r.heartbeatClaiming(false); body["policyVersion"] != float64(0) {
		t.Fatalf("policyVersion before any policy = %v", body["policyVersion"])
	}
	if code, _ := r.do("GET", "/relay/v1/policy", nil); code != http.StatusNotFound {
		t.Fatalf("GET policy before any = %d, want 404", code)
	}

	if code, body := h.publishPolicy(1, true); code != http.StatusNoContent {
		t.Fatalf("PUT relay-policy = %d %v", code, body)
	}
	// The heartbeat says the version moved; the relay fetches the bytes the
	// owner signed, untouched.
	if body := r.heartbeatClaiming(false); body["policyVersion"] != float64(1) {
		t.Fatalf("policyVersion = %v, want 1", body["policyVersion"])
	}
	code, body := r.do("GET", "/relay/v1/policy", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /relay/v1/policy = %d %v", code, body)
	}
	raw := func(k string) []byte { s, _ := body[k].(string); b, _ := decodeStd(s); return b }
	signed := trust.Signed[trust.Policy]{Payload: raw("payload"), Sig: raw("sig")}
	p, err := signed.Open(trust.DomainPolicy, h.ownerKey.Public().SigningKey())
	if err != nil {
		t.Fatalf("the relay does not get the owner's signature: %v", err)
	}
	if !p.AutoReject.Enabled || p.Entitlement != "a.token" {
		t.Fatalf("policy = %+v", p)
	}

	// No going back: a replayed or equal version is refused.
	if code, _ := h.publishPolicy(1, false); code != http.StatusConflict {
		t.Fatalf("PUT the same version = %d, want 409", code)
	}
	if code, _ := h.publishPolicy(2, false); code != http.StatusNoContent {
		t.Fatalf("PUT version 2 = %d", code)
	}

	// Another household's policy, and an unsigned one, are refused.
	other := signupHousehold(t, srv)
	signed2, _ := trust.Sign(h.ownerKey, trust.DomainPolicy, trust.Policy{TenantID: other.tenantID, Version: 5})
	if code, _ := h.owner.do("PUT", h.path("relay-policy"), map[string]any{"payload": signed2.Payload, "sig": signed2.Sig}); code != http.StatusBadRequest {
		t.Fatalf("PUT another household's policy = %d, want 400", code)
	}
	if code, _ := h.owner.do("PUT", h.path("relay-policy"), map[string]any{"payload": signed2.Payload}); code == http.StatusNoContent {
		t.Fatal("an unsigned policy was stored")
	}
	// The other household cannot read this one's policy.
	if code, _ := other.owner.do("GET", h.path("relay-policy"), nil); code == http.StatusOK {
		t.Fatal("another household read this household's relay policy")
	}
}

// TestAutoReject_PhoneLearnsWhoEnforces: the phone stands down while the
// lease holder said it enforces the rule and was heard from in the last two
// minutes; it takes over when the relay goes quiet, has no pass, or leaves.
func TestAutoReject_PhoneLearnsWhoEnforces(t *testing.T) {
	clock := &movingClock{now: time.Now()}
	srv := newTestHub(t, WithClock(clock.Now))
	h := signupHousehold(t, srv)
	h.registerNetwork([]string{"MonWiFi-2G"})
	r := h.enroll(h.pairingCode("pc"))
	h.publishBundle(r.trustRelay("pc"))

	if e := h.enforcer(); e["enforcer"] != "phone" || e["agentId"] != nil {
		t.Fatalf("no relay yet: %v", e)
	}

	// A relay holding the lease without a valid Max pass leaves it to the phone.
	if lease := r.heartbeatClaiming(false)["lease"]; lease != "active" {
		t.Fatalf("lease = %v", lease)
	}
	if e := h.enforcer(); e["enforcer"] != "phone" {
		t.Fatalf("relay without a pass: %v", e)
	}

	r.heartbeatClaiming(true)
	clock.advance(90 * time.Second)
	e := h.enforcer()
	if e["enforcer"] != "relay" || e["agentId"] != r.id || e["relayHeartbeatAgeSeconds"] != float64(90) || e["relayHeartbeatAt"] == nil {
		t.Fatalf("relay heard 90 s ago: %v", e)
	}

	// Silent for two minutes (it crashed, the PC went to sleep): the phone.
	clock.advance(30 * time.Second)
	if e := h.enforcer(); e["enforcer"] != "phone" {
		t.Fatalf("relay silent for 2 min: %v", e)
	}

	// Back, then shut down cleanly: the phone takes over at once.
	r.heartbeatClaiming(true)
	if e := h.enforcer(); e["enforcer"] != "relay" {
		t.Fatalf("relay back: %v", e)
	}
	r.release()
	if e := h.enforcer(); e["enforcer"] != "phone" {
		t.Fatalf("relay released the lease: %v", e)
	}

	// A standby relay that claims the rule does not count: it is not on
	// the box. Only the lease holder does.
	second := h.enroll(h.pairingCode("phone"))
	h.publishBundle(r.trustRelay("pc"), second.trustRelay("phone"))
	r.heartbeatClaiming(false) // takes the lease back, without a pass
	if lease := second.heartbeatClaiming(true)["lease"]; lease != "standby" {
		t.Fatalf("second relay lease = %v, want standby", lease)
	}
	if e := h.enforcer(); e["enforcer"] != "phone" {
		t.Fatalf("a standby relay was taken as the enforcer: %v", e)
	}

	// Another household sees nothing of this one.
	other := signupHousehold(t, srv)
	if code, _ := other.owner.do("GET", h.path("router/auto-reject"), nil); code == http.StatusOK {
		t.Fatal("another household read who enforces this one's rule")
	}
}

func decodeStd(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
