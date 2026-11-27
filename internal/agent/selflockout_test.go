package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/controller/httpapi"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/discovery"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/vault"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/catalog"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/vendor"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/clock"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/logging"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// UX audit 2026-11-26, C1 (#113) through the relay: the owner's signed
// commands from afar and WhatsApp's « 1 » (the hub's narrow key) run on the
// core's use cases, whose guard never cuts the device running Kergui off
// the box. Here the relay could not tell its own address (no LANIP), so its
// own guards (ErrSelfHarm) have nothing to go by: the core's guard is what
// refuses, and its 409 self_lockout comes back to the app as it is. Against
// the F6600P mock, through the real API handler.

const (
	lockRelayPhone   = "02:4b:47:00:01:60" // the phone running the relay, on MonWiFi-2G
	lockRelayPhoneIP = "192.168.1.160"
	lockStranger     = "02:4b:47:00:01:61" // a device WhatsApp may block, on MonWiFi-5G
)

// movableSelf is the core's port.SelfLocator: "" is the phone off the Wi-Fi.
type movableSelf struct {
	mu sync.Mutex
	ip string
}

func (s *movableSelf) SelfIP(context.Context, string) domain.IP {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ip == "" {
		return domain.IP{}
	}
	return domain.MustIP(s.ip)
}

type lockoutWorld struct {
	t     *testing.T
	owner *trust.Identity
	hub   *trust.Identity // the hub's narrow WhatsApp key
	box   *mockrouter.F6600P
	self  *movableSelf
	clock *arClock
	agent *Agent
}

// guardedCoreAPI is the in-memory API as the phone's core builds it for its relay:
// the use cases with the self guard, over the box at boxURL.
func guardedCoreAPI(t *testing.T, boxURL string, self *movableSelf) http.Handler {
	t.Helper()
	factory := catalog.New()
	clk := clock.Real{}
	disco := discovery.New(factory, clk)
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	vlt := vault.New(sqlite.NewCredentialStore(db), "test-key")
	if err := vlt.Store(context.Background(), usecase.RouterID(boxURL), domain.Credentials{Username: "admin", Password: "admin"}); err != nil {
		t.Fatal(err)
	}
	log := logging.New(io.Discard, false)
	devices := sqlite.NewDeviceRepo(db)
	return httpapi.New(httpapi.Services{
		List:    usecase.NewListDevices(factory, vlt, devices, vendor.New(nil), disco, clk, log),
		Block:   usecase.NewBlockDevice(factory, vlt, disco, clk, log).WithSelf(self),
		Unblock: usecase.NewUnblockDevice(factory, vlt, disco, clk, log),
		WiFi:    usecase.NewWiFiAccess(factory, vlt, disco).WithSelf(self),
		Logger:  log,
	}, httpapi.Config{BaseURL: boxURL, AdapterID: "zte_f6600p", Version: "test"}).Handler()
}

func newLockoutWorld(t *testing.T) *lockoutWorld {
	t.Helper()
	gen := func() *trust.Identity {
		id, err := trust.Generate(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	relay := gen()
	w := &lockoutWorld{t: t, owner: gen(), hub: gen(), box: mockrouter.NewF6600P("admin", "admin"),
		self: &movableSelf{ip: lockRelayPhoneIP}, clock: &arClock{t: arT0}}
	w.box.Join(mockrouter.Station{MAC: lockRelayPhone, Hostname: "Telephone-relais", IP: lockRelayPhoneIP, AP: "DEV.WIFI.AP1"})
	w.box.Join(mockrouter.Station{MAC: lockStranger, Hostname: "Inconnu", IP: "192.168.1.161", AP: "DEV.WIFI.AP5"})
	boxSrv := httptest.NewServer(w.box.Handler())
	t.Cleanup(boxSrv.Close)

	b := trust.Bundle{
		TenantID: arTenant, Version: 1, IssuedAt: arT0, Owner: w.owner.Public(),
		Relays: []trust.Relay{{AgentID: arAgent, Kind: "phone", Keys: relay.Public()}},
	}
	if err := b.DelegateHubKey(w.hub.Public(), "WhatsApp"); err != nil {
		t.Fatal(err)
	}
	s, err := trust.Sign(w.owner, trust.DomainBundle, b)
	if err != nil {
		t.Fatal(err)
	}
	hub := &fakeHub{bundle: s}
	hubSrv := httptest.NewServer(hub)
	t.Cleanup(hubSrv.Close)

	ag, err := New(Config{
		HubURL: hubSrv.URL, Handler: guardedCoreAPI(t, boxSrv.URL, w.self),
		State: &MemoryState{Data: StateData{
			HubURL: hubSrv.URL, AgentID: arAgent, TenantID: arTenant, Token: "kga_test",
			Identity: relay.Marshal(), OwnerKey: w.owner.Public().SigningKey(),
		}},
		Clock: w.clock.now,
		Timings: Timings{Heartbeat: time.Second, LongPoll: time.Millisecond, SnapshotEvery: time.Second,
			HubTimeout: 5 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.agent = ag
	w.clock.set(w.clock.now().Add(2 * time.Second))
	ag.tick(context.Background()) // takes the lease and the owner's bundle
	return w
}

// send is the hub handing the relay a command signed by who.
func (w *lockoutWorld) send(who *trust.Identity, method, path, body string) (int, string) {
	w.t.Helper()
	var raw []byte
	if body != "" {
		raw = []byte(body)
	}
	sc, err := trust.NewCommand(who, arTenant, method, path, raw, w.clock.now(), 30*time.Second)
	if err != nil {
		w.t.Fatal(err)
	}
	cmd, _ := sc.Unverified()
	status, out := w.agent.run(context.Background(), &queuedCommand{ID: cmd.CmdID, Epoch: 1, Envelope: sc, Body: raw})
	return status, string(out)
}

func wantRelayLockout(t *testing.T, what string, status int, body, reason string) {
	t.Helper()
	var a struct{ Code, Reason string }
	_ = json.Unmarshal([]byte(body), &a)
	if status != http.StatusConflict || a.Code != httpapi.SelfLockoutCode || a.Reason != reason {
		t.Fatalf("%s = %d %s, want 409 self_lockout %s", what, status, body, reason)
	}
}

func TestSelfLockout_OwnerCommandsFromAfar(t *testing.T) {
	w := newLockoutWorld(t)

	status, body := w.send(w.owner, "POST", "/api/devices/"+lockRelayPhone+"/block", "")
	wantRelayLockout(t, "block the relay's phone", status, body, domain.SelfLockoutDevice)
	status, body = w.send(w.owner, "POST", "/api/devices/"+lockRelayPhone+"/wifi-block", "")
	wantRelayLockout(t, "cut the relay's phone off the Wi-Fi", status, body, domain.SelfLockoutDevice)
	if w.box.Blocked(lockRelayPhone) || w.box.WiFiWrites() != 0 {
		t.Fatalf("the phone reached the box: blocked %v, %d Wi-Fi writes", w.box.Blocked(lockRelayPhone), w.box.WiFiWrites())
	}

	status, body = w.send(w.owner, "PATCH", "/api/wifi/ssids/DEV.WIFI.AP1", `{"enabled":false}`)
	wantRelayLockout(t, "turn off the phone's network", status, body, domain.SelfLockoutNetwork)
	if status, body := w.send(w.owner, "PATCH", "/api/wifi/ssids/DEV.WIFI.AP5", `{"enabled":false}`); status != http.StatusOK {
		t.Fatalf("turn off the other network = %d %s", status, body)
	}
	// The phone is no longer on the box: the last network on still stays.
	w.self.mu.Lock()
	w.self.ip = ""
	w.self.mu.Unlock()
	status, body = w.send(w.owner, "PATCH", "/api/wifi/ssids/DEV.WIFI.AP1", `{"enabled":false}`)
	wantRelayLockout(t, "turn off the last network", status, body, domain.SelfLockoutLastNetwork)
	if s, _ := w.box.SSID("DEV.WIFI.AP1"); !s.Enabled {
		t.Fatal("the relay turned off the last network")
	}
}

func TestSelfLockout_WhatsAppNeverBlocksThePhone(t *testing.T) {
	w := newLockoutWorld(t)

	status, body := w.send(w.hub, "POST", "/api/devices/"+lockRelayPhone+"/block", "")
	wantRelayLockout(t, "WhatsApp « 1 » on the relay's phone", status, body, domain.SelfLockoutDevice)
	if w.box.Blocked(lockRelayPhone) {
		t.Fatal("WhatsApp blocked the phone running the relay")
	}
	// The same key still blocks a stranger: the guard spares one device only.
	if status, body := w.send(w.hub, "POST", "/api/devices/"+lockStranger+"/block", ""); status != http.StatusOK || !w.box.Blocked(lockStranger) {
		t.Fatalf("WhatsApp « 1 » on a stranger = %d %s, blocked %v", status, body, w.box.Blocked(lockStranger))
	}
}
