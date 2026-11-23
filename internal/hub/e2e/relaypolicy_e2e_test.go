package e2e

// The relay policy from the owner's app (#36), end to end: the owner's phone
// core — a real mobilecore — turns auto-reject on with a Max token, signs
// the relay policy from its own database and gate, and the app PUTs it to
// the hub; the household's relay picks it up, checks the owner's signature
// and the token, and enforces the rule. The phone then hands the core the
// hub's GET router/auto-reject answer, and its rounds (the watch service's)
// stay off the box; once the relay stops, the hub names the phone and the
// next round acts.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/httpkit"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/logging"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// coreCall is one request to the phone's core, as the app's api client makes it.
func (p *phoneOwnerApp) coreCall(method, path, body string) map[string]any {
	p.t.Helper()
	res, err := p.core.Request(method, path, body)
	if err != nil {
		p.t.Fatal(err)
	}
	if res.Status != http.StatusOK {
		p.t.Fatalf("core %s %s = %d %s", method, path, res.Status, res.Body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Body), &out); err != nil {
		p.t.Fatalf("core %s %s: %v\n%s", method, path, err, res.Body)
	}
	return out
}

// currentPolicy is GET relay-policy as the app passes it to the core: ""
// when the household has none yet.
func (p *phoneOwnerApp) currentPolicy() string {
	p.t.Helper()
	code, body := p.call("GET", p.tenantPath("relay-policy"), nil)
	if code == http.StatusNotFound {
		return ""
	}
	if code != http.StatusOK {
		p.t.Fatalf("GET relay-policy = %d %v", code, body)
	}
	b, _ := json.Marshal(body)
	return string(b)
}

// publishPolicy is the app's relay-policy sync: the core signs from the
// hub's bundle and policy, and what it returns is PUT. It returns the
// version, 0 when the core had nothing new to say.
func (p *phoneOwnerApp) publishPolicy() uint64 {
	p.t.Helper()
	raw, err := p.core.SignRelayPolicy(p.tenantID, p.currentBundle(), p.currentPolicy())
	if err != nil {
		p.t.Fatalf("SignRelayPolicy: %v", err)
	}
	if raw == "" {
		return 0
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(raw), &body)
	if code, out := p.call("PUT", p.tenantPath("relay-policy"), body); code != http.StatusNoContent {
		p.t.Fatalf("PUT relay-policy = %d %v", code, out)
	}
	return uint64(body["version"].(float64))
}

// askEnforcer is the app before a round: the hub's answer, handed to the
// core as is.
func (p *phoneOwnerApp) askEnforcer() string {
	p.t.Helper()
	code, body := p.call("GET", p.tenantPath("router/auto-reject"), nil)
	if code != http.StatusOK {
		p.t.Fatalf("GET router/auto-reject = %d %v", code, body)
	}
	raw, _ := json.Marshal(body)
	return str(p.coreCall("PUT", "/api/autoreject/enforcer", string(raw)), "enforcer")
}

func TestE2E_TheOwnersAppPublishesThePolicyAndStandsDown(t *testing.T) {
	w := newWorldWith(t, fastTimings)
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

	// The owner's phone: logged in to the box, the hub's key and the Max
	// token handed to its core, auto-reject on (every device present accepted).
	owner := newPhoneOwnerApp(t, w.owner)
	if err := owner.core.Configure(w.boxURL, "zte_f6600p", w.boxPin); err != nil {
		t.Fatal(err)
	}
	owner.loginToBox(boxPass)
	if err := owner.core.SetPublicKeys(base64.StdEncoding.EncodeToString(licPub)); err != nil {
		t.Fatal(err)
	}
	owner.core.SetEntitlement(maxToken)
	if on := owner.coreCall("PUT", "/api/autoreject", `{"enabled":true}`); on["enabled"] != true {
		t.Fatalf("the phone could not turn the rule on: %v", on)
	}

	// Pairing from the app (#43, #44): bundle, QR, the relay, the same
	// digits, the sealed box password.
	w.owner.registerNetwork(w.boxPin)
	owner.signAndUpload(map[string]any{"routerPin": w.boxPin})
	inv, err := trust.ParseInvite(owner.showQR("pc"))
	if err != nil {
		t.Fatal(err)
	}
	state, res, err := agent.Enroll(ctx, w.hub.URL, inv, "PC du salon", "pc", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	listed := owner.agent(res.AgentID)
	if owner.ownerCode(listed) != res.Code {
		t.Fatal("the two pairing codes differ")
	}
	owner.confirm(listed)
	owner.uploadSealed()

	// The relay, with the hub's licensing key (KERGUI_LICENSING_PUBLIC_KEY).
	relay := newRouterAPIWith(t, w.boxURL, w.boxPin, &ruleClock{}, false)
	relayCtx, stopRelay := context.WithCancel(ctx)
	defer stopRelay()
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
	waitForDevices(t, w)

	// No policy yet: the relay claims nothing, the phone enforces.
	if who := owner.askEnforcer(); who != "phone" {
		t.Fatalf("enforcer before any policy = %q, want phone", who)
	}

	// The app publishes what the core signed; the relay takes the rule on.
	if v := owner.publishPolicy(); v != 1 {
		t.Fatalf("first policy published as v%d", v)
	}
	w.waitForEnforcer("relay")

	// The relay adopted the phone's accepted list, not whatever it found.
	accepted := map[string]bool{}
	for _, d := range owner.coreCall("GET", "/api/autoreject", "")["devices"].([]any) {
		if m := d.(map[string]any); m["state"] == "accepted" {
			accepted[str(m, "mac")] = true
		}
	}
	st, err := relay.autoReject.Status(ctx, w.boxURL)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Settings.Enabled {
		t.Fatal("the relay did not adopt the rule")
	}
	for mac := range accepted {
		found := false
		for _, d := range st.Devices {
			if d.MAC.String() == mac && d.State == domain.AutoRejectAccepted {
				found = true
			}
		}
		if !found {
			t.Fatalf("the relay did not accept %s, which the phone had accepted", mac)
		}
	}

	// Nothing changed: the next sync sends nothing.
	if v := owner.publishPolicy(); v != 0 {
		t.Fatalf("an unchanged rule was published again as v%d", v)
	}

	// Before its round the phone asks the hub, hands the answer to its core,
	// and the round (the watch service's) stays off the box.
	if who := owner.askEnforcer(); who != "relay" {
		t.Fatalf("the core did not take the hub's answer: %q", who)
	}
	logins := w.box.Logins()
	if run := owner.coreCall("POST", "/api/autoreject/run", ""); run["enforcer"] != "relay" {
		t.Fatalf("phone round while the relay enforces: %v", run)
	}
	if w.box.Logins() != logins {
		t.Fatal("the phone logged in to the box while the relay enforces the rule")
	}

	// The relay stops: the hub names the phone, and its next round acts.
	stopRelay()
	<-relayDone
	w.waitForEnforcer("phone")
	if who := owner.askEnforcer(); who != "phone" {
		t.Fatalf("after the relay stopped the core says %q", who)
	}
	if run := owner.coreCall("POST", "/api/autoreject/run", ""); run["enforcer"] != "phone" || run["entitled"] != true {
		t.Fatalf("phone round once the relay stopped: %v", run)
	}

	// Off on the phone: published, the relay would stop too.
	owner.coreCall("PUT", "/api/autoreject", `{"enabled":false}`)
	if v := owner.publishPolicy(); v != 2 {
		t.Fatalf("turning the rule off published v%d, want 2", v)
	}
}
