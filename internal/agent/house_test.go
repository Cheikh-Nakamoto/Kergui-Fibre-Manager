package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Issue #50: consumption, SSIDs and cut / restore through the relay. The
// guards hold whatever the owner signed: the relay, the household's other
// relays and the protected devices are never cut, the Wi-Fi network the
// relay is on is never turned off, and a write the box model has not had
// validated on hardware is refused with 501.

const (
	houseOther     = "02:00:00:00:00:98" // another relay of the household
	houseProtected = "02:00:00:00:00:97"
	houseKid       = "ac:bb:cc:00:00:22"
	houseTV        = "ac:bb:cc:00:00:33"
)

// houseBox is a small stateful box API: the device list with each device's
// SSID, the SSIDs, and the blocks.
type houseBox struct {
	mu      sync.Mutex
	blocked map[string]bool
	enabled map[string]bool
	names   map[string]string
	writes  []string
}

func newHouseBox() *houseBox {
	return &houseBox{
		blocked: map[string]bool{houseTV: true},
		enabled: map[string]bool{"DEV.WIFI.AP1": true, "DEV.WIFI.AP5": true},
		names:   map[string]string{"DEV.WIFI.AP1": "MonWiFi-2G", "DEV.WIFI.AP5": "MonWiFi-5G"},
	}
}

func (b *houseBox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := r.URL.Path
	switch {
	case r.Method == "GET" && p == "/api/devices":
		type dev struct {
			MAC, IP, SSID string
			Blocked       bool
		}
		_ = json.NewEncoder(w).Encode([]dev{
			{arOwnMAC, arLANIP, "MonWiFi-2G", false},
			{houseOther, "192.168.1.51", "MonWiFi-2G", false},
			{houseProtected, "192.168.1.52", "MonWiFi-2G", false},
			{houseKid, "192.168.1.22", "MonWiFi-5G", b.blocked[houseKid]},
			{houseTV, "192.168.1.33", "MonWiFi-5G", b.blocked[houseTV]},
		})
	case r.Method == "GET" && p == "/api/wifi/ssids":
		out := []domain.WiFiSSID{}
		for _, id := range []string{"DEV.WIFI.AP1", "DEV.WIFI.AP5"} {
			out = append(out, domain.WiFiSSID{ID: id, Alias: "SSID" + id[len(id)-1:], Name: b.names[id], Enabled: b.enabled[id]})
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == "PATCH" && strings.HasPrefix(p, "/api/wifi/ssids/"):
		id := strings.TrimPrefix(p, "/api/wifi/ssids/")
		var ch domain.WiFiSSIDChange
		_ = json.NewDecoder(r.Body).Decode(&ch)
		if ch.Enabled != nil {
			b.enabled[id] = *ch.Enabled
		}
		if ch.Name != nil {
			b.names[id] = *ch.Name
		}
		b.writes = append(b.writes, "PATCH "+id)
		_, _ = w.Write([]byte(`{}`))
	case r.Method == "POST" && strings.HasSuffix(p, "/block"):
		mac := strings.TrimSuffix(strings.TrimPrefix(p, "/api/devices/"), "/block")
		b.blocked[mac] = true
		b.writes = append(b.writes, "block "+mac)
		_, _ = w.Write([]byte(`{}`))
	case r.Method == "POST" && strings.HasSuffix(p, "/unblock"):
		mac := strings.TrimSuffix(strings.TrimPrefix(p, "/api/devices/"), "/unblock")
		delete(b.blocked, mac)
		b.writes = append(b.writes, "unblock "+mac)
		_, _ = w.Write([]byte(`{}`))
	case r.Method == "POST" && strings.HasSuffix(p, "/wifi-block"):
		b.writes = append(b.writes, "wifi-block")
		_, _ = w.Write([]byte(`{}`))
	case r.Method == "POST" && p == "/api/usage/sample":
		b.writes = append(b.writes, "sample")
		_, _ = w.Write([]byte(`{}`))
	case r.Method == "GET" && p == "/api/usage":
		_, _ = w.Write([]byte(`{"period":"2026-11","devices":[]}`))
	default:
		_, _ = io.Copy(io.Discard, r.Body)
		http.NotFound(w, r)
	}
}

func (b *houseBox) log() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.writes...)
}

type houseWorld struct {
	t     *testing.T
	owner *trust.Identity
	box   *houseBox
	agent *Agent
	clock *arClock
	ssid  string // the Wi-Fi the relay's phone reports
}

func newHouseWorld(t *testing.T, endpoints Endpoints) *houseWorld {
	t.Helper()
	owner, _ := trust.Generate(rand.Reader)
	relay, _ := trust.Generate(rand.Reader)
	other, _ := trust.Generate(rand.Reader)
	w := &houseWorld{t: t, owner: owner, box: newHouseBox(), clock: &arClock{t: arT0}}
	hub := &fakeHub{}
	s, err := trust.Sign(owner, trust.DomainBundle, trust.Bundle{
		TenantID: arTenant, Version: 1, IssuedAt: arT0, Owner: owner.Public(),
		Relays: []trust.Relay{
			{AgentID: arAgent, Kind: "phone", Keys: relay.Public()},
			{AgentID: "agent-2", Kind: "pc", Keys: other.Public(), MACs: []string{houseOther}},
		},
		ProtectedMACs: []string{houseProtected},
	})
	if err != nil {
		t.Fatal(err)
	}
	hub.bundle = s
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)
	ag, err := New(Config{
		HubURL: srv.URL, Handler: w.box,
		State: &MemoryState{Data: StateData{
			HubURL: srv.URL, AgentID: arAgent, TenantID: arTenant, Token: "kga_test",
			Identity: relay.Marshal(), OwnerKey: owner.Public().SigningKey(),
		}},
		WiFi:      func(context.Context) (string, string) { return w.ssid, "" },
		LANIP:     arLANIP,
		Clock:     w.clock.now,
		Endpoints: endpoints,
		Timings:   Timings{Heartbeat: time.Second, LongPoll: time.Millisecond, SnapshotEvery: time.Hour, HubTimeout: 5 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	ag.tick(context.Background())
	w.agent = ag
	return w
}

func (w *houseWorld) send(method, path, body string) (int, map[string]any) {
	w.t.Helper()
	var b []byte
	if body != "" {
		b = []byte(body)
	}
	sc, err := trust.NewCommand(w.owner, arTenant, method, path, b, w.clock.now(), 30*time.Second)
	if err != nil {
		w.t.Fatal(err)
	}
	cmd, _ := sc.Unverified()
	status, raw := w.agent.run(context.Background(), &queuedCommand{ID: cmd.CmdID, Epoch: 1, Envelope: sc, Body: b})
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return status, out
}

func strs(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			out = append(out, x.(string))
		}
	}
	return out
}

func TestHouse_CutNeverCutsARelayOrAProtectedDevice(t *testing.T) {
	w := newHouseWorld(t, nil)
	status, out := w.send("POST", RouteInternetCut, "")
	if status != http.StatusOK {
		t.Fatalf("cut = %d %v", status, out)
	}
	if got := strs(out["cut"]); len(got) != 1 || got[0] != houseKid {
		t.Fatalf("cut %v, want only %s (the TV was already blocked)", got, houseKid)
	}
	spared := strings.Join(strs(out["spared"]), " ")
	for _, m := range []string{arOwnMAC, houseOther, houseProtected} {
		if !strings.Contains(spared, m) {
			t.Errorf("%s not spared: %v", m, spared)
		}
	}
	for _, wr := range w.box.log() {
		for _, m := range []string{arOwnMAC, houseOther, houseProtected} {
			if strings.Contains(wr, m) {
				t.Errorf("the box received %q", wr)
			}
		}
	}

	// Restore gives back what the cut took, and leaves the TV blocked.
	status, out = w.send("POST", RouteInternetRestore, "")
	if status != http.StatusOK || strings.Join(strs(out["restored"]), " ") != houseKid {
		t.Fatalf("restore = %d %v", status, out)
	}
	if !w.box.blocked[houseTV] {
		t.Error("the restore lifted a block someone chose")
	}
	// A restore naming the devices (the app's memory, after a relay restart).
	w.send("POST", RouteInternetCut, "")
	w.agent.lastCut = nil
	if status, out = w.send("POST", RouteInternetRestore, `{"macs":["`+houseKid+`"]}`); status != http.StatusOK || len(strs(out["restored"])) != 1 {
		t.Fatalf("named restore = %d %v", status, out)
	}
}

func TestHouse_CutRefusedWhenTheRelayCannotTellWhichDeviceItIs(t *testing.T) {
	w := newHouseWorld(t, nil)
	w.agent.cfg.LANIP, w.agent.ownMAC = "", ""
	status, out := w.send("POST", RouteInternetCut, "")
	if status != http.StatusForbidden || !strings.Contains(out["error"].(string), "relay itself") {
		t.Fatalf("cut without knowing itself = %d %v", status, out)
	}
	if len(w.box.log()) != 0 {
		t.Fatalf("the box was touched: %v", w.box.log())
	}
}

func TestHouse_TheRelaysSSIDIsNeverTurnedOff(t *testing.T) {
	w := newHouseWorld(t, nil)
	// The relay's own device is on MonWiFi-2G (device list), even when the
	// phone cannot read its SSID (location refused).
	status, out := w.send("PATCH", "/api/wifi/ssids/DEV.WIFI.AP1", `{"enabled":false}`)
	if status != http.StatusForbidden || !strings.Contains(out["error"].(string), "relay") {
		t.Fatalf("turning off the relay's SSID = %d %v", status, out)
	}
	status, _ = w.send("PATCH", "/api/wifi/ssids/DEV.WIFI.AP1", `{"name":"Autre"}`)
	if status != http.StatusForbidden {
		t.Fatalf("renaming the relay's SSID = %d", status)
	}
	// The phone reports the 5 GHz network: that one is refused too.
	w.ssid = "MonWiFi-5G"
	if status, _ = w.send("PATCH", "/api/wifi/ssids/DEV.WIFI.AP5", `{"enabled":false}`); status != http.StatusForbidden {
		t.Fatalf("turning off the SSID the phone reports = %d", status)
	}
	for _, wr := range w.box.log() {
		if strings.HasPrefix(wr, "PATCH") {
			t.Fatalf("the box received %q", wr)
		}
	}
	// Turning a network on is never refused; a password never goes remote.
	if status, _ = w.send("PATCH", "/api/wifi/ssids/DEV.WIFI.AP1", `{"enabled":true}`); status != http.StatusOK {
		t.Fatalf("turning on = %d", status)
	}
	if status, _ = w.send("PATCH", "/api/wifi/ssids/DEV.WIFI.AP5", `{"password":"x12345678"}`); status != http.StatusForbidden {
		t.Fatalf("a password change = %d", status)
	}
}

func TestHouse_AnSSIDNobodyGuardedIsOnItCanBeSwitchedAndRenamed(t *testing.T) {
	w := newHouseWorld(t, nil)
	if status, out := w.send("PATCH", "/api/wifi/ssids/DEV.WIFI.AP5", `{"enabled":false}`); status != http.StatusOK {
		t.Fatalf("turning off the 5 GHz = %d %v", status, out)
	}
	if status, _ := w.send("PATCH", "/api/wifi/ssids/DEV.WIFI.AP5", `{"name":"Salon"}`); status != http.StatusOK {
		t.Fatalf("rename = %d", status)
	}
	if w.box.enabled["DEV.WIFI.AP5"] || w.box.names["DEV.WIFI.AP5"] != "Salon" {
		t.Fatal("the box did not change")
	}
}

func TestHouse_UnverifiedEndpointsAreRefusedWith501(t *testing.T) {
	eps := []domain.EndpointDoc{
		{Operation: "block", Verified: true},
		{Operation: "wifi_block", Verified: false},
		{Operation: "wifi_ssid_set", Verified: false},
		{Operation: "wifi_blocked", Verified: false},
	}
	w := newHouseWorld(t, func(context.Context) ([]domain.EndpointDoc, error) { return eps, nil })
	status, out := w.send("POST", "/api/devices/"+houseKid+"/wifi-block", "")
	if status != http.StatusNotImplemented || out["code"] != "unverified" {
		t.Fatalf("an unverified wifi-block = %d %v", status, out)
	}
	if status, out = w.send("PATCH", "/api/wifi/ssids/DEV.WIFI.AP5", `{"enabled":true}`); status != http.StatusNotImplemented {
		t.Fatalf("an unverified SSID write = %d %v", status, out)
	}
	if status, _ = w.send("POST", "/api/devices/"+houseKid+"/block", ""); status != http.StatusOK {
		t.Fatalf("a verified block = %d", status)
	}
	if status, _ = w.send("GET", "/api/wifi/ssids", ""); status != http.StatusOK {
		t.Fatalf("a read = %d", status)
	}
	// The deny list of an unverified Wi-Fi access control: the app shows
	// « Couper l'accès » disabled rather than a list it cannot act on.
	if status, out = w.send("GET", "/api/wifi/blocked", ""); status != http.StatusNotImplemented || out["code"] != "unverified" {
		t.Fatalf("an unverified wifi/blocked read = %d %v", status, out)
	}
	for _, wr := range w.box.log() {
		if wr == "wifi-block" || strings.HasPrefix(wr, "PATCH") {
			t.Fatalf("the box received %q", wr)
		}
	}

	// Endpoints that cannot be read: writes wait, reads go on.
	w2 := newHouseWorld(t, func(context.Context) ([]domain.EndpointDoc, error) { return nil, errors.New("box away") })
	if status, _ := w2.send("POST", "/api/devices/"+houseKid+"/block", ""); status != http.StatusNotImplemented {
		t.Fatalf("a write with unknown endpoints = %d", status)
	}
}

func TestHouse_UsageIsSampledBeforeItIsRead(t *testing.T) {
	w := newHouseWorld(t, nil)
	if status, out := w.send("GET", "/api/usage", ""); status != http.StatusOK || out["period"] != "2026-11" {
		t.Fatalf("usage = %d %v", status, out)
	}
	if l := w.box.log(); len(l) != 1 || l[0] != "sample" {
		t.Fatalf("box log %v, want one sample", l)
	}
}
