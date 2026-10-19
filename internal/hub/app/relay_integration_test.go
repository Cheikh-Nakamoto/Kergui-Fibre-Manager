package app

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// These tests exercise the H1 relay path against a real Postgres, with a
// hand-written relay standing in for internal/agent: pairing, the lease, the
// long-polled queue, signed results and snapshots, and every refusal the hub
// is supposed to produce on its own.

const routerPin = "3f364506861835de605d766589afbd712b5ed154ad1ec10c07477a2c1633a1fc"

// household is an owner signed up on the hub, with their trust keys.
type household struct {
	t        *testing.T
	srv      *httptest.Server
	owner    client
	ownerKey *trust.Identity
	tenantID string
	userID   string
	bundleV  uint64
}

func signupHousehold(t *testing.T, srv *httptest.Server) *household {
	t.Helper()
	anon := client{t: t, base: srv.URL}
	run := time.Now().UnixNano()
	email := fmt.Sprintf("owner-%d@hub.test", run)
	const password = "Correct-Horse-9-Battery"

	code, signup := anon.do("POST", "/api/v1/auth/signup-owner", map[string]any{
		"email": email, "password": password,
		"tenantName": "Maison Diallo", "tenantSlug": fmt.Sprintf("diallo-%d", run),
	})
	if code != http.StatusCreated {
		t.Fatalf("signup-owner = %d %v", code, signup)
	}
	tenantID, userID := str(signup, "tenantId"), str(signup, "userId")
	if code, _ := anon.do("POST", "/api/v1/auth/verify-email", map[string]any{"token": verificationToken(t, userID)}); code != http.StatusNoContent {
		t.Fatalf("verify-email = %d", code)
	}
	code, login := anon.do("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": password})
	if code != http.StatusOK {
		t.Fatalf("login = %d %v", code, login)
	}
	key, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &household{
		t: t, srv: srv, tenantID: tenantID, userID: userID, ownerKey: key,
		owner: client{t: t, base: srv.URL, token: str(login, "accessToken")},
	}
}

func (h *household) path(suffix string) string {
	return "/api/v1/tenants/" + h.tenantID + "/" + suffix
}

// registerNetwork records the box the relays must be in front of.
func (h *household) registerNetwork(ssids []string) {
	h.t.Helper()
	code, body := h.owner.do("PUT", h.path("network"), map[string]any{
		"ssids": ssids, "bssids": []string{}, "routerPin": routerPin,
	})
	if code != http.StatusNoContent {
		h.t.Fatalf("PUT network = %d %v", code, body)
	}
}

// publishBundle signs and uploads a trust bundle naming the given relays.
func (h *household) publishBundle(relays ...trust.Relay) {
	h.t.Helper()
	h.bundleV++
	signed, err := trust.Sign(h.ownerKey, trust.DomainBundle, trust.Bundle{
		TenantID: h.tenantID,
		Version:  h.bundleV,
		IssuedAt: time.Now().UTC(),
		Owner:    h.ownerKey.Public(),
		Network:  trust.Network{SSIDs: []string{"MonWiFi-2G"}, RouterPin: routerPin},
		Relays:   relays,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	code, body := h.owner.do("PUT", h.path("trust-bundle"), map[string]any{
		"payload": signed.Payload, "sig": signed.Sig,
	})
	if code != http.StatusNoContent {
		h.t.Fatalf("PUT trust-bundle = %d %v", code, body)
	}
}

// pairingCode asks the hub for a one-shot code.
func (h *household) pairingCode(kind string) string {
	h.t.Helper()
	code, body := h.owner.do("POST", h.path("agents/pairing"), map[string]any{"kind": kind})
	if code != http.StatusCreated {
		h.t.Fatalf("POST agents/pairing = %d %v", code, body)
	}
	return str(body, "code")
}

// relay is a hand-written agent: it holds its own keys and token, and signs
// what it sends, exactly as internal/agent will.
type testRelay struct {
	t       *testing.T
	srv     *httptest.Server
	id      string
	tenant  string
	token   string
	keys    *trust.Identity
	epoch   int64
	callNum int
}

// enroll redeems a pairing code, the way a relay joins a household.
func (h *household) enroll(code string) *testRelay {
	h.t.Helper()
	keys, err := trust.Generate(rand.Reader)
	if err != nil {
		h.t.Fatal(err)
	}
	pub := keys.Public()
	anon := client{t: h.t, base: h.srv.URL}
	status, body := anon.do("POST", "/relay/v1/pair/redeem", map[string]any{
		"code": code, "name": "banc de test", "ed25519": pub.Ed25519, "x25519": pub.X25519,
	})
	if status != http.StatusCreated {
		h.t.Fatalf("pair/redeem = %d %v", status, body)
	}
	return &testRelay{
		t: h.t, srv: h.srv, id: str(body, "agentId"), tenant: str(body, "tenantId"),
		token: str(body, "token"), keys: keys,
	}
}

// trustRelay is how the owner names this relay in the bundle.
func (r *testRelay) trustRelay(kind string) trust.Relay {
	return trust.Relay{AgentID: r.id, Kind: kind, Keys: r.keys.Public()}
}

func (r *testRelay) do(method, path string, body any) (int, map[string]any) {
	r.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, r.srv.URL+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// heartbeat reports this relay's network and keeps its lease.
func (r *testRelay) heartbeat(pin, ssid string) (string, map[string]any) {
	r.t.Helper()
	status, body := r.do("POST", "/relay/v1/heartbeat", map[string]any{
		"wifi":           map[string]any{"ssid": ssid, "bssid": ""},
		"routerIdentity": map[string]any{"pin": pin},
		"lanIp":          "192.168.1.50",
		"version":        "test",
	})
	if status != http.StatusOK {
		r.t.Fatalf("heartbeat = %d %v", status, body)
	}
	if e, ok := body["epoch"].(float64); ok {
		r.epoch = int64(e)
	}
	lease, _ := body["lease"].(string)
	return lease, body
}

func (r *testRelay) release() {
	r.t.Helper()
	r.do("POST", "/relay/v1/heartbeat", map[string]any{
		"wifi":           map[string]any{"ssid": "MonWiFi-2G", "bssid": ""},
		"routerIdentity": map[string]any{"pin": routerPin},
		"lanIp":          "192.168.1.50",
		"version":        "test",
		"released":       true,
	})
}

// serveOnce long-polls for one command and answers it with the given status
// and body, signed. It returns the command's path, or "" if none arrived.
func (r *testRelay) serveOnce(wait time.Duration, status int, respBody string) string {
	r.t.Helper()
	code, body := r.do("GET", fmt.Sprintf("/relay/v1/commands?wait=%s", wait), nil)
	if code == http.StatusNoContent || code == http.StatusConflict {
		return ""
	}
	if code != http.StatusOK {
		r.t.Fatalf("GET commands = %d %v", code, body)
	}
	cmdID := str(body, "id")
	envelope, _ := json.Marshal(body["envelope"])
	var signed trust.SignedCommand
	if err := json.Unmarshal(envelope, &signed); err != nil {
		r.t.Fatal(err)
	}
	cmd, err := signed.Unverified()
	if err != nil {
		r.t.Fatal(err)
	}
	r.callNum++

	epoch := int64(0)
	if e, ok := body["epoch"].(float64); ok {
		epoch = int64(e)
	}
	result, err := trust.NewResult(r.keys, r.tenant, cmd.CmdID, epoch, status, []byte(respBody), time.Now())
	if err != nil {
		r.t.Fatal(err)
	}
	rc, rb := r.do("POST", "/relay/v1/commands/"+cmdID+"/result", map[string]any{
		"payload": result.Payload, "sig": result.Sig,
	})
	if rc != http.StatusNoContent {
		r.t.Fatalf("POST result = %d %v", rc, rb)
	}
	return cmd.Path
}

// pushDevices signs and pushes a device snapshot.
func (r *testRelay) pushDevices(body string) (int, map[string]any) {
	r.t.Helper()
	snap, err := trust.NewSnapshot(r.keys, r.tenant, "devices", r.id, r.epoch, []byte(body), time.Now())
	if err != nil {
		r.t.Fatal(err)
	}
	return r.do("POST", "/relay/v1/snapshots", map[string]any{"payload": snap.Payload, "sig": snap.Sig})
}

// blockCommand signs a block command the way the owner's app does.
func (h *household) blockCommand(mac string) map[string]any {
	h.t.Helper()
	signed, err := trust.NewCommand(h.ownerKey, h.tenantID, http.MethodPost,
		"/api/devices/"+mac+"/block", nil, time.Now(), 60*time.Second)
	if err != nil {
		h.t.Fatal(err)
	}
	return map[string]any{"payload": signed.Payload, "sig": signed.Sig}
}

// --- the tests ------------------------------------------------------------

// TestRelay_PairLeaseAndCommand is the H1 happy path, hub-side: pair a
// relay, let it take the lease, and run one signed command end to end.
func TestRelay_PairLeaseAndCommand(t *testing.T) {
	srv := newTestHub(t, WithCallerWait(5*time.Second))
	h := signupHousehold(t, srv)
	h.registerNetwork([]string{"MonWiFi-2G"})

	r := h.enroll(h.pairingCode("pc"))
	h.publishBundle(r.trustRelay("pc"))

	if lease, body := r.heartbeat(routerPin, "MonWiFi-2G"); lease != "active" {
		t.Fatalf("lease = %q, want active (%v)", lease, body)
	}

	// The relay pushes what it sees; the app reads it without waking the box.
	if code, body := r.pushDevices(`[{"MAC":"ac:bb:cc:00:00:22"}]`); code != http.StatusNoContent {
		t.Fatalf("push snapshot = %d %v", code, body)
	}
	code, devices := h.owner.do("GET", h.path("router/devices"), nil)
	if code != http.StatusOK {
		t.Fatalf("GET router/devices = %d %v", code, devices)
	}
	if str(devices, "agentId") != r.id || devices["asOf"] == nil {
		t.Errorf("snapshot = %v, want it attributed and dated", devices)
	}

	// One signed block, answered by the relay while the app waits.
	done := make(chan string, 1)
	go func() { done <- r.serveOnce(5*time.Second, 200, `{"status":"blocked"}`) }()

	code, out := h.owner.do("POST", h.path("router/devices/ac:bb:cc:00:00:22/block"), h.blockCommand("ac:bb:cc:00:00:22"))
	if code != http.StatusOK {
		t.Fatalf("POST block = %d %v", code, out)
	}
	if got := <-done; got != "/api/devices/ac:bb:cc:00:00:22/block" {
		t.Errorf("the relay was asked for %q", got)
	}
	if out["result"] == nil {
		t.Fatal("the response carries no signed result")
	}
	// The result must verify against the relay's key: that is what stops a
	// hub from reporting a block that never happened.
	raw, _ := json.Marshal(out["result"])
	var res trust.SignedResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if _, err := res.Open(trust.DomainResult, r.keys.Public().Ed25519); err != nil {
		t.Fatalf("the stored result is not the relay's: %v", err)
	}
}

// TestRelay_StandbyWhenTheNetworkDoesNotMatch: a relay in front of another
// box, or on another Wi-Fi, never gets the lease — so it never logs in.
func TestRelay_StandbyWhenTheNetworkDoesNotMatch(t *testing.T) {
	srv := newTestHub(t)
	h := signupHousehold(t, srv)
	h.registerNetwork([]string{"MonWiFi-2G"})
	r := h.enroll(h.pairingCode("pc"))
	h.publishBundle(r.trustRelay("pc"))

	for _, tc := range []struct{ name, pin, ssid string }{
		{"another box", "0000000000000000000000000000000000000000000000000000000000000000", "MonWiFi-2G"},
		{"another Wi-Fi", routerPin, "FreeWifi"},
		{"no box identity at all", "", "MonWiFi-2G"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lease, body := r.heartbeat(tc.pin, tc.ssid)
			if lease != "standby" {
				t.Fatalf("lease = %q, want standby", lease)
			}
			if body["reason"] == "" {
				t.Error("the standby carries no reason for the user")
			}
			// And it is refused the queue, not merely given nothing.
			if code, _ := r.do("GET", "/relay/v1/commands?wait=1s", nil); code != http.StatusConflict {
				t.Errorf("GET commands = %d, want 409 for a standby relay", code)
			}
		})
	}
}

// TestRelay_PairingCodeIsSingleUseAndExpires guards the one route a caller
// with no credentials can reach.
func TestRelay_PairingCodeIsSingleUse(t *testing.T) {
	srv := newTestHub(t)
	h := signupHousehold(t, srv)
	code := h.pairingCode("pc")

	h.enroll(code) // the first relay wins

	keys, _ := trust.Generate(rand.Reader)
	pub := keys.Public()
	anon := client{t: t, base: srv.URL}
	status, _ := anon.do("POST", "/relay/v1/pair/redeem", map[string]any{
		"code": code, "name": "second", "ed25519": pub.Ed25519, "x25519": pub.X25519,
	})
	if status != http.StatusNotFound {
		t.Fatalf("second redeem = %d, want 404", status)
	}
	// An unknown code is refused the same way, so probing tells nothing.
	status, _ = anon.do("POST", "/relay/v1/pair/redeem", map[string]any{
		"code": "ABCD-EFGH-IJKL-MNOP", "name": "x", "ed25519": pub.Ed25519, "x25519": pub.X25519,
	})
	if status != http.StatusNotFound {
		t.Fatalf("unknown code = %d, want 404", status)
	}
}

func TestRelay_PairingCodeExpires(t *testing.T) {
	// The hub's clock is moved forward instead of sleeping five minutes.
	clock := &testClock{now: time.Now()}
	srv := newTestHub(t, WithClock(clock.Now))
	h := signupHousehold(t, srv)
	code := h.pairingCode("pc")

	clock.advance(6 * time.Minute)

	keys, _ := trust.Generate(rand.Reader)
	pub := keys.Public()
	anon := client{t: t, base: srv.URL}
	status, _ := anon.do("POST", "/relay/v1/pair/redeem", map[string]any{
		"code": code, "name": "late", "ed25519": pub.Ed25519, "x25519": pub.X25519,
	})
	if status != http.StatusNotFound {
		t.Fatalf("redeem after 6 minutes = %d, want 404", status)
	}
}

// TestRelay_RevokedAgentLosesEverything: revoking is how an owner removes a
// phone from the household, so it must close every door at once.
func TestRelay_RevokedAgentLosesEverything(t *testing.T) {
	srv := newTestHub(t)
	h := signupHousehold(t, srv)
	h.registerNetwork([]string{"MonWiFi-2G"})
	r := h.enroll(h.pairingCode("pc"))
	h.publishBundle(r.trustRelay("pc"))
	r.heartbeat(routerPin, "MonWiFi-2G")

	if code, body := h.owner.do("DELETE", h.path("agents/"+r.id), nil); code != http.StatusNoContent {
		t.Fatalf("DELETE agent = %d %v", code, body)
	}
	for _, call := range []struct{ method, path string }{
		{"POST", "/relay/v1/heartbeat"},
		{"GET", "/relay/v1/commands?wait=1s"},
		{"GET", "/relay/v1/trust"},
		{"GET", "/relay/v1/credentials"},
	} {
		if code, _ := r.do(call.method, call.path, map[string]any{}); code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401 for a revoked agent", call.method, call.path, code)
		}
	}
}

// TestRelay_HouseholdsAreIsolated: another household's owner is a stranger
// here, even with a valid token of their own.
func TestRelay_HouseholdsAreIsolated(t *testing.T) {
	srv := newTestHub(t)
	mine := signupHousehold(t, srv)
	mine.registerNetwork([]string{"MonWiFi-2G"})
	theirs := signupHousehold(t, srv)

	stranger := client{t: t, base: srv.URL, token: theirs.owner.token}
	for _, call := range []struct {
		method, path string
		body         any
	}{
		{"GET", mine.path("agents"), nil},
		{"POST", mine.path("agents/pairing"), map[string]any{"kind": "pc"}},
		{"GET", mine.path("network"), nil},
		{"GET", mine.path("router/devices"), nil},
	} {
		if code, _ := stranger.do(call.method, call.path, call.body); code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", call.method, call.path, code)
		}
	}
}

// TestRelay_SealedCredentialsStayOpaque: the hub stores the box password as
// ciphertext and hands it only to the relay it was sealed for.
func TestRelay_SealedCredentialsStayOpaque(t *testing.T) {
	srv := newTestHub(t)
	h := signupHousehold(t, srv)
	h.registerNetwork([]string{"MonWiFi-2G"})
	r := h.enroll(h.pairingCode("pc"))
	h.publishBundle(r.trustRelay("pc"))

	blob, err := trust.SealCredentials(rand.Reader, h.ownerKey, r.keys.Public().X25519, trust.Credentials{
		TenantID: h.tenantID, AgentID: r.id, Username: "admin", Password: "box-pass", Version: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if code, body := h.owner.do("PUT", h.path("agents/"+r.id+"/credentials"), map[string]any{
		"blob": blob, "version": 1,
	}); code != http.StatusNoContent {
		t.Fatalf("PUT credentials = %d %v", code, body)
	}

	code, got := r.do("GET", "/relay/v1/credentials", nil)
	if code != http.StatusOK {
		t.Fatalf("GET credentials = %d %v", code, got)
	}
	raw, err := json.Marshal(got["blob"])
	if err != nil {
		t.Fatal(err)
	}
	var fetched []byte
	if err := json.Unmarshal(raw, &fetched); err != nil {
		t.Fatal(err)
	}
	creds, err := trust.OpenCredentials(r.keys, h.ownerKey.Public().X25519, fetched, h.tenantID, r.id)
	if err != nil {
		t.Fatalf("the relay cannot open its own credentials: %v", err)
	}
	if creds.Password.Reveal() != "box-pass" {
		t.Error("the credentials did not survive the round trip")
	}
	if bytes.Contains(fetched, []byte("box-pass")) {
		t.Error("the stored blob contains the password in clear")
	}
}

// TestRelay_HubRefusesWhatTheRelayWould is the hub's defence in depth: it
// does not have to carry a command the relay would certainly refuse.
func TestRelay_HubRefusesBadCommands(t *testing.T) {
	srv := newTestHub(t, WithCallerWait(time.Second))
	h := signupHousehold(t, srv)
	h.registerNetwork([]string{"MonWiFi-2G"})
	r := h.enroll(h.pairingCode("pc"))
	h.publishBundle(r.trustRelay("pc"))

	stranger, _ := trust.Generate(rand.Reader)
	strangerCmd, _ := trust.NewCommand(stranger, h.tenantID, http.MethodPost,
		"/api/devices/ac:bb:cc:00:00:22/block", nil, time.Now(), time.Minute)
	otherRoute, _ := trust.NewCommand(h.ownerKey, h.tenantID, http.MethodPost,
		"/api/devices/ac:bb:cc:00:00:99/block", nil, time.Now(), time.Minute)
	expired, _ := trust.NewCommand(h.ownerKey, h.tenantID, http.MethodPost,
		"/api/devices/ac:bb:cc:00:00:22/block", nil, time.Now().Add(-2*time.Minute), time.Minute)

	tests := []struct {
		name string
		body map[string]any
		want int
	}{
		{"no signature at all", map[string]any{"payload": []byte(`{}`), "sig": []byte{}}, http.StatusForbidden},
		{"signed by a key the bundle does not know",
			map[string]any{"payload": strangerCmd.Payload, "sig": strangerCmd.Sig}, http.StatusForbidden},
		{"signed for another device than the route",
			map[string]any{"payload": otherRoute.Payload, "sig": otherRoute.Sig}, http.StatusForbidden},
		{"already expired",
			map[string]any{"payload": expired.Payload, "sig": expired.Sig}, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, body := h.owner.do("POST", h.path("router/devices/ac:bb:cc:00:00:22/block"), tc.body)
			if code != tc.want {
				t.Fatalf("POST block = %d %v, want %d", code, body, tc.want)
			}
		})
	}
}

// TestRelay_NobodyAnswersInTime: the app is handed the command id instead of
// holding a request open from a phone.
func TestRelay_NobodyAnswersInTime(t *testing.T) {
	srv := newTestHub(t, WithCallerWait(500*time.Millisecond))
	h := signupHousehold(t, srv)
	h.registerNetwork([]string{"MonWiFi-2G"})
	r := h.enroll(h.pairingCode("pc"))
	h.publishBundle(r.trustRelay("pc"))
	r.heartbeat(routerPin, "MonWiFi-2G") // active, but never polls

	code, out := h.owner.do("POST", h.path("router/devices/ac:bb:cc:00:00:22/block"), h.blockCommand("ac:bb:cc:00:00:22"))
	if code != http.StatusAccepted {
		t.Fatalf("POST block = %d %v, want 202", code, out)
	}
	cmdID := str(out, "commandId")
	if cmdID == "" {
		t.Fatal("202 without a command id leaves the app nothing to poll")
	}
	// The relay turns up late; the result is then readable by id.
	if path := r.serveOnce(5*time.Second, 200, `{"status":"blocked"}`); path == "" {
		t.Fatal("the late relay got no command")
	}
	code, status := h.owner.do("GET", h.path("router/commands/"+cmdID), nil)
	if code != http.StatusOK || str(status, "status") != "done" {
		t.Fatalf("GET command = %d %v, want a done command", code, status)
	}
}

// TestRelay_OnlyTheLeaseHolderIsServed: the box allows one admin session, so
// a second relay must be given nothing to run.
func TestRelay_OnlyTheLeaseHolderIsServed(t *testing.T) {
	srv := newTestHub(t, WithCallerWait(2*time.Second))
	h := signupHousehold(t, srv)
	h.registerNetwork([]string{"MonWiFi-2G"})

	first := h.enroll(h.pairingCode("pc"))
	second := h.enroll(h.pairingCode("pc"))
	h.publishBundle(first.trustRelay("pc"), second.trustRelay("pc"))

	if lease, _ := first.heartbeat(routerPin, "MonWiFi-2G"); lease != "active" {
		t.Fatal("the first relay did not take the lease")
	}
	if lease, _ := second.heartbeat(routerPin, "MonWiFi-2G"); lease != "standby" {
		t.Fatal("two relays were told they are active")
	}
	if code, _ := second.do("GET", "/relay/v1/commands?wait=1s", nil); code != http.StatusConflict {
		t.Errorf("the standby relay was served the queue: %d", code)
	}

	// A result from the relay that does not hold the lease is refused, even
	// for a command it somehow knows the id of.
	done := make(chan string, 1)
	go func() { done <- first.serveOnce(3*time.Second, 200, `{"status":"blocked"}`) }()
	h.owner.do("POST", h.path("router/devices/ac:bb:cc:00:00:22/block"), h.blockCommand("ac:bb:cc:00:00:22"))
	<-done
}

// TestRelay_CleanHandoverKeepsOneSession: when the holder releases, the
// other relay takes over without the two ever overlapping.
func TestRelay_CleanHandover(t *testing.T) {
	srv := newTestHub(t)
	h := signupHousehold(t, srv)
	h.registerNetwork([]string{"MonWiFi-2G"})
	first := h.enroll(h.pairingCode("pc"))
	second := h.enroll(h.pairingCode("pc"))
	h.publishBundle(first.trustRelay("pc"), second.trustRelay("pc"))

	if lease, _ := first.heartbeat(routerPin, "MonWiFi-2G"); lease != "active" {
		t.Fatal("the first relay did not take the lease")
	}
	if lease, _ := second.heartbeat(routerPin, "MonWiFi-2G"); lease != "standby" {
		t.Fatal("the second relay should be standing by")
	}

	first.release()

	if lease, body := second.heartbeat(routerPin, "MonWiFi-2G"); lease != "active" {
		t.Fatalf("the second relay did not take over after a clean release: %v", body)
	}
	if lease, _ := first.heartbeat(routerPin, "MonWiFi-2G"); lease != "standby" {
		t.Fatal("the released relay took the lease back while the other holds it")
	}
}

// --- helpers --------------------------------------------------------------

// testClock is a clock the test moves by hand.
type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

func (c *testClock) advance(d time.Duration) { c.now = c.now.Add(d) }
