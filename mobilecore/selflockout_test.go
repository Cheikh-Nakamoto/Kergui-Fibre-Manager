package mobilecore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// UX audit 2026-11-26, C1 (#113): the core never cuts this phone off the box.
// The demo's « ce téléphone » is the phone, as on a real box: blocking it,
// cutting its Wi-Fi access or turning off the network it is on answers 409
// self_lockout with its reason, and nothing reaches the box. The relay this
// phone may run executes the owner's and WhatsApp's commands on the same use
// cases, so it refuses them too.

type lockoutAnswer struct {
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

func wantCoreLockout(t *testing.T, what string, status int, body, reason string) {
	t.Helper()
	var a lockoutAnswer
	_ = json.Unmarshal([]byte(body), &a)
	if status != http.StatusConflict || a.Code != "self_lockout" || a.Reason != reason {
		t.Fatalf("%s = %d %s, want 409 self_lockout %s", what, status, body, reason)
	}
}

func demoSSID(t *testing.T, c *Core, id string) mockrouter.SSIDState {
	t.Helper()
	c.mu.RLock()
	box := c.demoBox
	c.mu.RUnlock()
	s, ok := box.SSID(id)
	if !ok {
		t.Fatalf("no SSID %s on the demo box", id)
	}
	return s
}

func TestSelfLockout_ThisPhoneInTheDemo(t *testing.T) {
	c := approvalDemo(t)
	if d := markedList(t, c)[DemoPhoneMAC]; d.Mark != MarkSelf {
		t.Fatalf("the demo's phone = %+v, want marked Self", d)
	}

	status, body := call(t, c, "POST", "/api/devices/"+DemoPhoneMAC+"/block", "")
	wantCoreLockout(t, "block this phone", status, body, domain.SelfLockoutDevice)
	status, body = call(t, c, "POST", "/api/devices/"+DemoPhoneMAC+"/wifi-block", "")
	wantCoreLockout(t, "cut this phone's Wi-Fi access", status, body, domain.SelfLockoutDevice)
	if d := devices(t, c)[DemoPhoneMAC]; d.Blocked {
		t.Fatal("this phone was blocked")
	}
	status, body = call(t, c, "GET", "/api/wifi/blocked", "")
	if status != http.StatusOK || strings.Contains(strings.ToLower(body), DemoPhoneMAC) {
		t.Fatalf("Wi-Fi deny list = %d %s, want it without this phone", status, body)
	}

	// The network this phone is on (MonWiFi-2G) stays on; the other goes off,
	// and the last one on stays on even once the phone has left it.
	status, body = call(t, c, "PATCH", "/api/wifi/ssids/DEV.WIFI.AP1", `{"enabled":false}`)
	wantCoreLockout(t, "turn off this phone's network", status, body, domain.SelfLockoutNetwork)
	if !demoSSID(t, c, "DEV.WIFI.AP1").Enabled {
		t.Fatal("this phone's network was turned off")
	}
	if status, body := call(t, c, "PATCH", "/api/wifi/ssids/DEV.WIFI.AP5", `{"enabled":false}`); status != http.StatusOK {
		t.Fatalf("turn off the other network = %d %s", status, body)
	}
	c.self.setDemo(c.DemoURL(), domain.IP{})
	status, body = call(t, c, "PATCH", "/api/wifi/ssids/DEV.WIFI.AP1", `{"enabled":false}`)
	wantCoreLockout(t, "turn off the last network", status, body, domain.SelfLockoutLastNetwork)

	// Another device is blocked as usual.
	if status, body := call(t, c, "POST", "/api/devices/"+DemoVisitorMAC+"/block", ""); status == http.StatusConflict {
		t.Fatalf("block another device = %d %s", status, body)
	}
}

// TestSelfLockout_TheRelayOfThisPhone: the handler the relay runs the
// household's signed commands on — the owner's from afar, WhatsApp's « 1 » —
// refuses this phone as the app does.
func TestSelfLockout_TheRelayOfThisPhone(t *testing.T) {
	c, _, box := gatedCore(t)
	const phone, phoneIP = "fa:00:00:00:01:13", "192.168.1.113"
	box.Join(mockrouter.Station{MAC: phone, Hostname: "Telephone-relais", IP: phoneIP, AP: "DEV.WIFI.AP1"})
	c.self.set(func(string) domain.IP { return domain.MustIP(phoneIP) })
	c.mu.RLock()
	relay := c.relayHandler(c.routerCfg)
	c.mu.RUnlock()

	run := func(method, path, body string) (int, string) {
		req := httptest.NewRequestWithContext(t.Context(), method, "http://relay.local"+path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		relay.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	status, body := run("POST", "/api/devices/"+phone+"/block", "")
	wantCoreLockout(t, "relay: block this phone", status, body, domain.SelfLockoutDevice)
	status, body = run("POST", "/api/devices/"+phone+"/wifi-block", "")
	wantCoreLockout(t, "relay: cut this phone's Wi-Fi access", status, body, domain.SelfLockoutDevice)
	status, body = run("PATCH", "/api/wifi/ssids/DEV.WIFI.AP1", `{"enabled":false}`)
	wantCoreLockout(t, "relay: turn off this phone's network", status, body, domain.SelfLockoutNetwork)
	if box.Blocked(phone) || box.WiFiWrites() != 0 {
		t.Fatalf("the relay reached the box for this phone: blocked %v, %d Wi-Fi writes", box.Blocked(phone), box.WiFiWrites())
	}
	if s, _ := box.SSID("DEV.WIFI.AP1"); !s.Enabled {
		t.Fatal("the relay turned off this phone's network")
	}
}

// TestSelfLockout_AutoRejectAndPurgeSpareThisPhone: auto-reject never refuses
// this phone, however long it waits, and the purge never blocks it.
func TestSelfLockout_AutoRejectAndPurgeSpareThisPhone(t *testing.T) {
	c := approvalDemo(t)
	if status, body := setAutoReject(t, c, true); status != http.StatusOK {
		t.Fatalf("turn auto-reject on = %d %s", status, body)
	}
	// The phone comes back with a new private MAC at its address: a device
	// nobody accepted, 2 hours on the box.
	c.mu.RLock()
	box := c.demoBox
	c.mu.RUnlock()
	const rotated = "fa:4b:47:00:01:13"
	box.Forget(DemoPhoneMAC)
	box.Join(mockrouter.Station{MAC: rotated, Hostname: demoPhoneName, IP: demoPhoneIP})
	runAutoReject(t, c)
	c.clock.set(func() time.Time { return time.Now().Add(2 * time.Hour) })
	if run := runAutoReject(t, c); len(run.Refused) != 0 {
		t.Fatalf("auto-reject refused %+v, want nobody (this phone is the only newcomer)", run.Refused)
	}
	status, body := call(t, c, "GET", "/api/wifi/blocked", "")
	if status != http.StatusOK || strings.Contains(strings.ToLower(body), rotated) {
		t.Fatalf("Wi-Fi deny list = %d %s, want it without this phone", status, body)
	}

	// A Wi-Fi refusal of the 5 GHz network names this phone (a rule left
	// from before): the purge does not complete it with a firewall rule.
	box.SetWiFiPolicy("DEV.WIFI.AP5", "Ban")
	box.AddWiFiRule(rotated, "DEV.WIFI.AP5", rotated+" AP5")
	devices(t, c) // the inventory knows the phone, seen now
	c.mu.RLock()
	cfg := c.routerCfg
	c.mu.RUnlock()
	in := usecase.MACFilterInput{BaseURL: cfg.BaseURL, AdapterID: cfg.AdapterID, Opts: cfg.Opts}
	if _, err := c.svc.Purge.Run(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if box.Blocked(rotated) {
		t.Fatal("the purge blocked this phone")
	}
}
