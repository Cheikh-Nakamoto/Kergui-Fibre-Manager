package mobilecore

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Issue #50: consumption, SSIDs, Wi-Fi access and cut / restore Internet
// through the relay. The owner's core signs after the paid-action gate
// (RFC-006); the relay — a real core on the F6600P mock — runs it with its
// guards: it never cuts itself or a protected device, and never turns off
// the Wi-Fi it is on.

type signedRequest struct {
	Payload []byte `json:"payload"`
	Sig     []byte `json:"sig"`
	Body    []byte `json:"body"`
	CmdID   string `json:"cmdId"`
	Method  string `json:"method"`
	Path    string `json:"path"`
}

func signRequest(t *testing.T, c *Core, bundle, method, path, body, box string) signedRequest {
	t.Helper()
	raw, err := c.SignRemoteRequest(credTenant, bundle, method, path, body, box)
	if err != nil {
		t.Fatalf("SignRemoteRequest(%s %s): %v", method, path, err)
	}
	var s signedRequest
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSignRemoteRequest_TierGating(t *testing.T) {
	c := newCore(t, nil)
	clk := &fakeClock{t: gateT0}
	c.gate.now = clk.now
	if err := c.SetPublicKeys(base64.StdEncoding.EncodeToString(testPub)); err != nil {
		t.Fatal(err)
	}
	bundle := bundleWith(t, c, credTenant, newRelay(t))
	mac := "ac:bb:cc:00:00:22"
	type req struct{ method, path, body string }
	free := []req{
		{"GET", "/api/usage", ""}, {"GET", "/api/wifi/ssids", ""}, {"GET", "/api/wifi/blocked", ""},
		{"POST", "/api/devices/" + mac + "/wifi-unblock", ""},
		{"POST", agent.RouteInternetRestore, `{"macs":["` + mac + `"]}`},
		{"PATCH", "/api/wifi/ssids/DEV.WIFI.AP5", `{"enabled":true}`},
	}
	basic := []req{{"POST", "/api/devices/" + mac + "/wifi-block", ""}, {"POST", agent.RouteInternetCut, ""}}
	maxi := []req{{"PATCH", "/api/wifi/ssids/DEV.WIFI.AP5", `{"enabled":false}`}, {"PATCH", "/api/wifi/ssids/DEV.WIFI.AP5", `{"name":"Salon"}`}}

	for _, r := range free {
		signRequest(t, c, bundle, r.method, r.path, r.body, mockSerial)
	}
	for _, r := range append(basic, maxi...) {
		if _, err := c.SignRemoteRequest(credTenant, bundle, r.method, r.path, r.body, mockSerial); !errors.Is(err, entitlement.ErrNotEntitled) || !strings.HasPrefix(err.Error(), "not_entitled") {
			t.Errorf("%s %s without a pass: %v", r.method, r.path, err)
		}
	}
	c.SetEntitlement(signToken(t, testPriv, entitlement.TierBasic, mockSerial, gateT0))
	for _, r := range basic {
		signRequest(t, c, bundle, r.method, r.path, r.body, mockSerial)
	}
	for _, r := range maxi {
		if _, err := c.SignRemoteRequest(credTenant, bundle, r.method, r.path, r.body, mockSerial); !errors.Is(err, entitlement.ErrNotEntitled) {
			t.Errorf("%s %s with Basic: %v", r.method, r.path, err)
		}
	}
	c.SetEntitlement(signToken(t, testPriv, entitlement.TierMax, mockSerial, gateT0))
	for _, r := range maxi {
		signRequest(t, c, bundle, r.method, r.path, r.body, mockSerial)
	}

	// Never signed: a route off the allow-list, another month, a password.
	for _, r := range []req{
		{"POST", "/api/login", ""}, {"POST", "/api/box/first-password", `{"newPassword":"Kergui2026"}`},
		{"GET", "/api/usage?period=2026-01", ""},
		{"GET", "/api/usage/devices/" + mac, ""}, {"POST", "/api/devices/" + mac + "/block", ""},
		{"PATCH", "/api/wifi/ssids/DEV.WIFI.AP5", `{"password":"12345678"}`},
		{"PATCH", "/api/wifi/ssids/DEV.WIFI.AP5", `{}`},
	} {
		if _, err := c.SignRemoteRequest(credTenant, bundle, r.method, r.path, r.body, mockSerial); err == nil {
			t.Errorf("%s %s %s was signed", r.method, r.path, r.body)
		}
	}
}

func TestSignRemoteRequest_AViewMemberReadsButNeverWrites(t *testing.T) {
	owner := newCore(t, nil)
	member := newCore(t, nil)
	keys := memberKeysOf(t, member)
	ch, _ := json.Marshal(map[string]any{"delegate": map[string]any{"ed25519": keys.Ed25519, "x25519": keys.X25519, "scope": "view", "label": "Awa"}})
	bundle, err := owner.SignTrustBundle(credTenant, bundleWith(t, owner, credTenant, newRelay(t)), string(ch))
	if err != nil {
		t.Fatal(err)
	}
	signRequest(t, member, bundle, "GET", "/api/usage", "", "")
	_, err = member.SignRemoteRequest(credTenant, bundle, "POST", "/api/devices/ac:bb:cc:00:00:22/wifi-unblock", "", "")
	if err == nil || !strings.HasPrefix(err.Error(), codeNotDelegated) {
		t.Fatalf("a view member's write = %v, want not_delegated", err)
	}
}

// memberKeysOf is the member's public keys, as MemberKeys gives them.
func memberKeysOf(t *testing.T, c *Core) struct{ Ed25519, X25519 []byte } {
	t.Helper()
	raw, err := c.MemberKeys()
	if err != nil {
		t.Fatal(err)
	}
	var k struct {
		Ed25519 string `json:"ed25519"`
		X25519  string `json:"x25519"`
	}
	_ = json.Unmarshal([]byte(raw), &k)
	dec := func(s string) []byte {
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding} {
			if b, err := enc.DecodeString(s); err == nil {
				return b
			}
		}
		t.Fatalf("undecodable key %q", s)
		return nil
	}
	return struct{ Ed25519, X25519 []byte }{dec(k.Ed25519), dec(k.X25519)}
}

// queueHub hands the relay the commands the test queues, and keeps the
// signed results.
type queueHub struct {
	mu      sync.Mutex
	bundle  json.RawMessage
	queue   []map[string]any
	results map[string]trust.SignedResult
}

func (h *queueHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case r.Method == "POST" && r.URL.Path == "/relay/v1/heartbeat":
		_ = json.NewEncoder(w).Encode(map[string]any{"lease": "active", "epoch": 1, "ttlSeconds": 60, "bundleVersion": 1})
	case r.Method == "GET" && r.URL.Path == "/relay/v1/trust":
		_, _ = w.Write(h.bundle)
	case r.Method == "GET" && r.URL.Path == "/relay/v1/commands":
		if len(h.queue) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next := h.queue[0]
		h.queue = h.queue[1:]
		_ = json.NewEncoder(w).Encode(next)
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/relay/v1/commands/"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/relay/v1/commands/"), "/result")
		var res trust.SignedResult
		_ = json.NewDecoder(r.Body).Decode(&res)
		h.results[id] = res
		w.WriteHeader(http.StatusNoContent)
	case r.Method == "POST" && r.URL.Path == "/relay/v1/snapshots":
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

const (
	relayPhoneMAC = "02:11:22:33:44:55"
	relayPhoneIP  = "192.168.1.60"
	guardedMAC    = "02:11:22:33:44:66"
)

type mockRelayWorld struct {
	t      *testing.T
	owner  *Core
	box    *mockrouter.F6600P
	hub    *queueHub
	bundle string
}

// newMockRelayWorld: the owner's core, and a relay core logged in to the
// F6600P mock, its phone on DEV.WIFI.AP1 (MonWiFi-2G) and a protected
// device on DEV.WIFI.AP5.
func newMockRelayWorld(t *testing.T) *mockRelayWorld {
	t.Helper()
	relayCore, _, box := gatedCore(t)
	box.Join(mockrouter.Station{MAC: relayPhoneMAC, IP: relayPhoneIP, Hostname: "relais", AP: "DEV.WIFI.AP1"})
	box.Join(mockrouter.Station{MAC: guardedMAC, IP: "192.168.1.61", Hostname: "camera", AP: "DEV.WIFI.AP5"})

	owner := newCore(t, nil)
	relayID, _ := trust.Generate(rand.Reader)
	ownerID, err := owner.ownerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	s, err := trust.Sign(ownerID, trust.DomainBundle, trust.Bundle{
		TenantID: credTenant, Version: 1, IssuedAt: time.Now().Add(-time.Minute), Owner: ownerID.Public(),
		Relays:        []trust.Relay{{AgentID: "A1", Kind: "phone", Keys: relayID.Public()}},
		ProtectedMACs: []string{guardedMAC},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, _ := json.Marshal(signedJSON{Payload: s.Payload, Sig: s.Sig})
	w := &mockRelayWorld{t: t, owner: owner, box: box, bundle: string(bundle),
		hub: &queueHub{bundle: bundle, results: map[string]trust.SignedResult{}}}
	hubSrv := httptest.NewServer(w.hub)
	t.Cleanup(hubSrv.Close)

	relayCore.mu.RLock()
	cfg := relayCore.routerCfg
	relayCore.mu.RUnlock()
	ag, err := agent.New(agent.Config{
		HubURL: hubSrv.URL, Handler: relayCore.relayHandler(cfg),
		State: &agent.MemoryState{Data: agent.StateData{
			HubURL: hubSrv.URL, AgentID: "A1", TenantID: credTenant, Token: "kga_test",
			Identity: relayID.Marshal(), OwnerKey: ownerID.Public().SigningKey(),
		}},
		Router: func(context.Context) (string, error) { return "pin", nil },
		LANIP:  relayPhoneIP,
		Endpoints: func(context.Context) ([]domain.EndpointDoc, error) {
			return []domain.EndpointDoc{{Operation: "block", Verified: true}, {Operation: "unblock", Verified: true}, {Operation: "wifi_ssid_set", Verified: true}, {Operation: "wifi_block", Verified: false}}, nil
		},
		Timings: agent.Timings{Heartbeat: 20 * time.Millisecond, LongPoll: 10 * time.Millisecond, SnapshotEvery: time.Hour, HubTimeout: 5 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = ag.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return w
}

// do signs a request with the owner's core (demo pass: the gate is
// TestSignRemoteRequest_TierGating's), queues it and waits for the relay's
// signed answer.
func (w *mockRelayWorld) do(method, path, body string) (int, map[string]any) {
	w.t.Helper()
	w.owner.gate.mu.Lock()
	w.owner.gate.demo = true
	w.owner.gate.mu.Unlock()
	s := signRequest(w.t, w.owner, w.bundle, method, path, body, "")
	w.hub.mu.Lock()
	w.hub.queue = append(w.hub.queue, map[string]any{"id": s.CmdID, "epoch": 1,
		"envelope": map[string]any{"payload": s.Payload, "sig": s.Sig}, "body": s.Body})
	w.hub.mu.Unlock()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		w.hub.mu.Lock()
		res, ok := w.hub.results[s.CmdID]
		w.hub.mu.Unlock()
		if ok {
			rawRes, _ := json.Marshal(res)
			out, err := w.owner.VerifyCommandResult(credTenant, s.CmdID, w.bundle, string(rawRes))
			if err != nil {
				w.t.Fatalf("VerifyCommandResult: %v", err)
			}
			var v struct {
				Status int            `json:"status"`
				Body   map[string]any `json:"body"`
			}
			_ = json.Unmarshal([]byte(out), &v)
			return v.Status, v.Body
		}
		time.Sleep(10 * time.Millisecond)
	}
	w.t.Fatalf("%s %s: no answer from the relay", method, path)
	return 0, nil
}

func TestRemote_OnTheMockTheRelayNeverCutsItselfNorItsSSID(t *testing.T) {
	w := newMockRelayWorld(t)

	// Cut Internet for the whole house: everyone but the relay and the
	// protected camera.
	status, out := w.do("POST", agent.RouteInternetCut, "")
	if status != http.StatusOK {
		t.Fatalf("cut = %d %v", status, out)
	}
	if w.box.Blocked(relayPhoneMAC) || w.box.Blocked(guardedMAC) {
		t.Fatal("the relay or the protected device was cut")
	}
	cut, _ := out["cut"].([]any)
	if len(cut) == 0 {
		t.Fatalf("nothing was cut: %v", out)
	}
	for _, m := range cut {
		if !w.box.Blocked(m.(string)) {
			t.Errorf("%v reported cut but not blocked on the box", m)
		}
	}
	// Restore gives them back.
	if status, out = w.do("POST", agent.RouteInternetRestore, ""); status != http.StatusOK {
		t.Fatalf("restore = %d %v", status, out)
	}
	for _, m := range cut {
		if w.box.Blocked(m.(string)) {
			t.Errorf("%v still blocked after the restore", m)
		}
	}

	// The relay's SSID (2.4 GHz) cannot be turned off, nor the protected
	// camera's (5 GHz).
	for _, id := range []string{"DEV.WIFI.AP1", "DEV.WIFI.AP5"} {
		if status, out = w.do("PATCH", "/api/wifi/ssids/"+id, `{"enabled":false}`); status != http.StatusForbidden {
			t.Fatalf("turning off %s = %d %v", id, status, out)
		}
		if st, _ := w.box.SSID(id); !st.Enabled {
			t.Fatalf("%s was turned off", id)
		}
	}
	// Once the camera leaves, the 5 GHz can be renamed and turned off.
	w.box.Leave(guardedMAC)
	w.box.Forget(guardedMAC)
	if status, out = w.do("PATCH", "/api/wifi/ssids/DEV.WIFI.AP5", `{"name":"Salon"}`); status != http.StatusOK {
		t.Fatalf("rename the 5 GHz = %d %v", status, out)
	}
	if st, _ := w.box.SSID("DEV.WIFI.AP5"); st.Name != "Salon" {
		t.Fatalf("5 GHz name = %q", st.Name)
	}

	// Wi-Fi access on this model is not validated on hardware: refused, 501.
	if status, out = w.do("POST", "/api/devices/ac:bb:cc:00:00:22/wifi-block", ""); status != http.StatusNotImplemented || out["code"] != "unverified" {
		t.Fatalf("wifi-block = %d %v, want 501 unverified", status, out)
	}

	// Consumption, current month, as the relay measures it.
	if status, out = w.do("GET", "/api/usage", ""); status != http.StatusOK {
		t.Fatalf("usage = %d %v", status, out)
	}
}
