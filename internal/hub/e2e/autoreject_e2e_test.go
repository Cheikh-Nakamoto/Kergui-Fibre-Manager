package e2e

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/httpkit"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/logging"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// Auto-reject on the relay agent with enforcer election (RFC-006, #36),
// end to end: the owner's phone turns the rule on, the relay gets the rule
// and the Max token through the hub and enforces it, the phone learns from
// the hub that the relay does and stands down; the relay stops, and the
// phone takes over.

// mockSerial is the serial the captured F6600P reports: the box a pass binds to.
const mockSerial = "ZTEG00000001"

// ruleClock is the auto-reject rule's own clock, moved forward instead of
// waiting the fixed 15 minutes.
type ruleClock struct {
	mu  sync.Mutex
	off time.Duration
}

func (c *ruleClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.off)
}

func (c *ruleClock) advance(d time.Duration) {
	c.mu.Lock()
	c.off += d
	c.mu.Unlock()
}

// publishPolicy signs the relay policy as the owner's app does.
func (o *ownerApp) publishPolicy(version uint64, p trust.AutoRejectPolicy, token string) {
	o.t.Helper()
	signed, err := trust.Sign(o.keys, trust.DomainPolicy, trust.Policy{
		TenantID: o.tenantID, Version: version, IssuedAt: time.Now().UTC(), AutoReject: p, Entitlement: token,
	})
	if err != nil {
		o.t.Fatal(err)
	}
	if code, body := o.call("PUT", o.tenantPath("relay-policy"), map[string]any{
		"payload": signed.Payload, "sig": signed.Sig,
	}); code != http.StatusNoContent {
		o.t.Fatalf("PUT relay-policy = %d %v", code, body)
	}
}

// enforcer is what the phone asks the hub before each round.
func (o *ownerApp) enforcer() (who string, age time.Duration) {
	o.t.Helper()
	code, body := o.call("GET", o.tenantPath("router/auto-reject"), nil)
	if code != http.StatusOK {
		o.t.Fatalf("GET router/auto-reject = %d %v", code, body)
	}
	secs, _ := body["relayHeartbeatAgeSeconds"].(float64)
	return str(body, "enforcer"), time.Duration(secs) * time.Second
}

func (w *world) waitForEnforcer(want string) time.Duration {
	w.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if who, age := w.owner.enforcer(); who == want {
			return age
		}
		time.Sleep(50 * time.Millisecond)
	}
	who, _ := w.owner.enforcer()
	w.t.Fatalf("the hub never named %q as the enforcer (still %q)", want, who)
	return 0
}

// phoneRound is one watch-service round: the phone passes the relay's last
// heartbeat, on its own clock, as the hub reported it.
func phoneRound(t *testing.T, phone routerAPI, clk *ruleClock, relayAge *time.Duration) map[string]any {
	t.Helper()
	body := []byte{}
	if relayAge != nil {
		body, _ = json.Marshal(map[string]any{"relayHeartbeatAt": clk.Now().Add(-*relayAge).UTC()})
	}
	req := httptest.NewRequest(http.MethodPost, "/api/autoreject/run", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	phone.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("phone round = %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func refusedMACs(run map[string]any) map[string]bool {
	out := map[string]bool{}
	list, _ := run["refused"].([]any)
	for _, r := range list {
		if m, ok := r.(map[string]any); ok {
			out[str(m, "mac")] = true
		}
	}
	return out
}

// waitTracked waits until rule has seen mac (pending, its clock started).
func waitTracked(t *testing.T, rule *usecase.AutoReject, baseURL, mac string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		st, err := rule.Status(context.Background(), baseURL)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range st.Devices {
			if d.MAC.String() == mac && d.State == domain.AutoRejectPending {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the relay never started the clock of %s", mac)
}

func TestE2E_AutoRejectRelayEnforcesThenPhoneTakesOver(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	licPub, licPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	licKeys, err := entitlement.NewKeySet(licPub)
	if err != nil {
		t.Fatal(err)
	}
	maxToken, err := entitlement.Sign(licPriv, entitlement.Claims{Account: "acc-e2e", BoxID: mockSerial,
		Tier: entitlement.TierMax, IssuedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(7 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}

	// The phone, on the box's Wi-Fi: it turns the rule on, which accepts
	// every device present.
	phoneClock := &ruleClock{}
	phone := newRouterAPIWith(t, w.boxURL, w.boxPin, phoneClock, true)
	if err := phone.vault.Store(ctx, usecase.RouterID(w.boxURL), domain.Credentials{Username: boxUser, Password: boxPass}); err != nil {
		t.Fatal(err)
	}
	st, err := phone.autoReject.Enable(ctx, phone.target)
	if err != nil {
		t.Fatalf("the phone could not turn the rule on: %v", err)
	}
	accepted := make([]string, 0, len(st.Devices))
	for _, d := range st.Devices {
		accepted = append(accepted, d.MAC.String())
	}

	// The household's PC relay, with the hub's licensing key.
	w.owner.registerNetwork(w.boxPin)
	w.owner.publishBundle(w.boxPin)
	relayCtx, stopRelay := context.WithCancel(ctx)
	defer stopRelay()
	state, res, err := agent.Enroll(ctx, w.hub.URL, w.owner.invite("pc"), "banc de test", "pc", http.DefaultClient)
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if res.Code != trust.PairingCode(w.owner.keys.Public(), relayPublic(t, state)) {
		t.Fatal("the two pairing codes differ")
	}
	relayClock := &ruleClock{}
	relay := newRouterAPIWith(t, w.boxURL, w.boxPin, relayClock, false)
	ag, err := agent.New(agent.Config{
		HubURL:  w.hub.URL,
		Handler: relay.handler,
		State:   &agent.MemoryState{Data: state},
		Router: func(ctx context.Context) (string, error) {
			return httpkit.ProbeFingerprint(ctx, w.boxURL, 5*time.Second)
		},
		WiFi:    func(context.Context) (string, string) { return "MonWiFi-2G", "" },
		LANIP:   "192.168.1.50",
		Version: "e2e",
		Credentials: func(ctx context.Context, c domain.Credentials) error {
			return relay.vault.Store(ctx, usecase.RouterID(w.boxURL), c)
		},
		AutoReject: agent.NewAutoRejecter(relay.autoReject, relay.target),
		Licensing:  licKeys,
		BoxSerial:  agent.InspectSerial(relay.inspect, usecase.InspectInput{BaseURL: w.boxURL, AdapterID: "zte_f6600p", Opts: relay.target.Opts}),
		Log:        logging.New(relayLogWriter(), true),
		Timings:    w.timings,
	})
	if err != nil {
		t.Fatal(err)
	}
	relayDone := make(chan struct{})
	go func() { defer close(relayDone); _ = ag.Run(relayCtx) }()
	w.owner.publishBundle(w.boxPin, trustRelay(state, "pc", t))
	w.owner.sealCredentials(state.AgentID, relayPublic(t, state).X25519)
	waitForDevices(t, w)

	// Without the owner's policy the relay claims nothing: the phone enforces.
	if who, _ := w.owner.enforcer(); who != "phone" {
		t.Fatalf("enforcer before any policy = %q, want phone", who)
	}

	// The owner's app hands the rule and the Max token to the relay.
	w.owner.publishPolicy(1, trust.AutoRejectPolicy{Enabled: true, Since: st.Settings.Since, Accepted: accepted}, maxToken)
	age := w.waitForEnforcer("relay")

	// The phone asks the hub, learns the relay enforces, and stays off the box.
	logins := w.box.Logins()
	if run := phoneRound(t, phone, phoneClock, &age); run["enforcer"] != "relay" || len(refusedMACs(run)) != 0 {
		t.Fatalf("phone round while the relay enforces: %v", run)
	}
	if w.box.Logins() != logins {
		t.Fatal("the phone logged in to the box while the relay enforces the rule")
	}

	// A stranger joins. The relay starts its clock; 15 minutes later (on
	// the rule's clock) it cuts its internet (a firewall rule, RFC-007 D5).
	const stranger, stranger2 = "02:00:00:00:00:e1", "02:00:00:00:00:e2"
	// The box starts with the household's own firewall rules (the captured
	// list): what counts is what the relay adds.
	blockedBefore := map[string]bool{}
	for _, mac := range accepted {
		blockedBefore[mac] = w.box.Blocked(mac)
	}
	w.box.Join(mockrouter.Station{MAC: stranger, Hostname: "Inconnu", IP: "192.168.1.231"})
	waitTracked(t, relay.autoReject, w.boxURL, stranger)
	relayClock.advance(domain.AutoRejectDelay + time.Minute)
	deadline := time.Now().Add(15 * time.Second)
	for !w.box.Blocked(stranger) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !w.box.Blocked(stranger) || w.box.WiFiBlocked(stranger) {
		t.Fatal("the relay did not cut the stranger's internet, or refused it at the Wi-Fi")
	}
	for _, mac := range accepted {
		if w.box.Blocked(mac) != blockedBefore[mac] || w.box.WiFiBlocked(mac) {
			t.Fatalf("the relay refused %s, which the phone had accepted", mac)
		}
	}

	// The relay stops (the PC is turned off): the hub hands the rule back
	// to the phone, which enforces it.
	stopRelay()
	<-relayDone
	w.waitForEnforcer("phone")

	w.box.Join(mockrouter.Station{MAC: stranger2, Hostname: "Inconnu 2", IP: "192.168.1.232"})
	if run := phoneRound(t, phone, phoneClock, nil); run["enforcer"] != "phone" {
		t.Fatalf("phone round once the relay stopped: %v", run)
	}
	phoneClock.advance(domain.AutoRejectDelay + time.Minute)
	run := phoneRound(t, phone, phoneClock, nil)
	if !refusedMACs(run)[stranger2] || !w.box.Blocked(stranger2) {
		t.Fatalf("the phone did not take over: %v", run)
	}
}
