package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// RFC-005 design rule 2 on the relay's side (#45): the relay reads the
// network the owner signed in the bundle and stands down on its own when it
// is not there, whatever the hub says. Without the location permission a
// phone reports no Wi-Fi, and only the box identity is checked — and said.

const netPin = "3f364506861835de605d766589afbd712b5ed154ad1ec10c07477a2c1633a1fc"

func signedNetwork() trust.Network {
	return trust.Network{SSIDs: []string{"MonWiFi-2G", "MonWiFi-5G"}, BSSIDs: []string{"aa:bb:cc:dd:ee:01"}, RouterPin: netPin}
}

func TestCheckNetwork(t *testing.T) {
	tests := []struct {
		name  string
		net   trust.Network
		seen  Seen
		ok    bool
		check string
	}{
		{"the home Wi-Fi, the home box", signedNetwork(), Seen{SSID: "MonWiFi-2G", BSSID: "aa:bb:cc:dd:ee:01", RouterPin: netPin}, true, CheckSignature},
		{"the pin compares like the hub's", signedNetwork(), Seen{SSID: "monwifi-5g", RouterPin: " " + strings.ToUpper(netPin)}, true, CheckSignature},
		{"location refused: no Wi-Fi, only the box", signedNetwork(), Seen{RouterPin: netPin}, true, CheckBoxIdentity},
		{"no signature registered: only the box", trust.Network{RouterPin: netPin}, Seen{SSID: "MonWiFi-2G", RouterPin: netPin}, true, CheckBoxIdentity},
		{"a neighbour's Wi-Fi", signedNetwork(), Seen{SSID: "Voisin", RouterPin: netPin}, false, ""},
		{"an access point nobody registered", signedNetwork(), Seen{SSID: "MonWiFi-2G", BSSID: "ff:ff:ff:ff:ff:ff", RouterPin: netPin}, false, ""},
		{"another box", signedNetwork(), Seen{SSID: "MonWiFi-2G", RouterPin: "0000"}, false, ""},
		{"the box does not answer", signedNetwork(), Seen{SSID: "MonWiFi-2G"}, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckNetwork(tc.net, tc.seen)
			if got.OK != tc.ok || (tc.ok && got.Check != tc.check) {
				t.Fatalf("CheckNetwork = %+v, want ok=%v check=%q", got, tc.ok, tc.check)
			}
			if got.Reason == "" && got.Check != CheckSignature {
				t.Error("anything short of the full signature must say why: the app shows it")
			}
		})
	}
}

// netHub always grants the lease: the relay must stand down on its own.
type netHub struct {
	mu     sync.Mutex
	bundle trust.Signed[trust.Bundle]
	beats  []heartbeatRequest
	lease  string
	reason string
	down   bool
}

func (h *netHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.down {
		http.Error(w, "down", http.StatusBadGateway)
		return
	}
	switch r.Method + " " + r.URL.Path {
	case "POST /relay/v1/heartbeat":
		var req heartbeatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		h.beats = append(h.beats, req)
		lease := h.lease
		if lease == "" {
			lease = leaseActive
		}
		_ = json.NewEncoder(w).Encode(heartbeatResponse{Lease: lease, Epoch: 1, TTLSeconds: 60, BundleVersion: 1, Reason: h.reason})
	case "GET /relay/v1/trust":
		_ = json.NewEncoder(w).Encode(h.bundle)
	case "GET /relay/v1/commands":
		w.WriteHeader(http.StatusNoContent)
	case "POST /relay/v1/snapshots":
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (h *netHub) last() heartbeatRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.beats[len(h.beats)-1]
}

type netWorld struct {
	hub   *netHub
	agent *Agent
	// box counts the calls to the relay's API: every one of them is a
	// login to the box, which a relay on the wrong network must never do.
	box  atomic.Int32
	wifi atomic.Value // [2]string
	pin  atomic.Value // string
}

func newNetWorld(t *testing.T) *netWorld {
	t.Helper()
	owner, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	relay, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := trust.Sign(owner, trust.DomainBundle, trust.Bundle{
		TenantID: arTenant, Version: 1, IssuedAt: arT0, Owner: owner.Public(), Network: signedNetwork(),
		Relays: []trust.Relay{{AgentID: arAgent, Kind: "dedicated", Keys: relay.Public()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w := &netWorld{hub: &netHub{bundle: trust.Signed[trust.Bundle]{Payload: s.Payload, Sig: s.Sig}}}
	w.wifi.Store([2]string{"MonWiFi-2G", "aa:bb:cc:dd:ee:01"})
	w.pin.Store(netPin)
	srv := httptest.NewServer(w.hub)
	t.Cleanup(srv.Close)
	api := devicesAPI()
	clock := &arClock{t: arT0}
	ag, err := New(Config{
		HubURL: srv.URL,
		Handler: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			w.box.Add(1)
			api.ServeHTTP(rw, r)
		}),
		State: &MemoryState{Data: StateData{
			HubURL: srv.URL, AgentID: arAgent, TenantID: arTenant, Token: "kga_test",
			Identity: relay.Marshal(), OwnerKey: owner.Public().SigningKey(),
		}},
		Router: func(context.Context) (string, error) { return w.pin.Load().(string), nil },
		WiFi: func(context.Context) (string, string) {
			v := w.wifi.Load().([2]string)
			return v[0], v[1]
		},
		LANIP:   arLANIP,
		Clock:   clock.now,
		Timings: Timings{Heartbeat: time.Second, LongPoll: time.Millisecond, SnapshotEvery: time.Minute, HubTimeout: 5 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.agent = ag
	return w
}

func (w *netWorld) tick() { w.agent.tick(context.Background()) }

func TestAgent_OnTheHomeWiFiItWorksAndSaysTheSignatureMatched(t *testing.T) {
	w := newNetWorld(t)
	w.tick()
	st := w.agent.Status()
	if st.State != StatusActive || st.Check != CheckSignature {
		t.Fatalf("Status = %+v, want active on the Wi-Fi signature", st)
	}
	if w.box.Load() == 0 {
		t.Fatal("the active relay did not read the box")
	}
	if b := w.hub.last(); b.Released || b.WiFi.SSID != "MonWiFi-2G" || b.RouterIdentity.Pin != netPin {
		t.Fatalf("heartbeat = %+v, want the Wi-Fi and the pin, not released", b)
	}
}

// TestAgent_WrongWiFiStandsDownAndNeverTouchesTheBox: the hub grants the
// lease (it may not know the signature), but the relay reads the one the
// owner signed, hands the lease back, and never logs in.
func TestAgent_WrongWiFiStandsDownAndNeverTouchesTheBox(t *testing.T) {
	w := newNetWorld(t)
	w.wifi.Store([2]string{"Voisin", "ff:ff:ff:ff:ff:ff"})
	for i := 0; i < 3; i++ {
		w.tick()
	}
	if n := w.box.Load(); n != 0 {
		t.Fatalf("a relay on the wrong Wi-Fi called the box %d times", n)
	}
	st := w.agent.Status()
	if st.State != StatusWrongNetwork || !strings.Contains(st.Reason, "Wi-Fi") {
		t.Fatalf("Status = %+v, want wrong-network with a reason", st)
	}
	// After the first round, every heartbeat says it stands down, so the
	// hub never grants it the lease again.
	if b := w.hub.last(); !b.Released {
		t.Fatalf("heartbeat = %+v, want released", b)
	}
}

func TestAgent_LeavingTheHomeWiFiStopsTheWork(t *testing.T) {
	w := newNetWorld(t)
	w.tick()
	if w.agent.Status().State != StatusActive {
		t.Fatalf("not active on the home Wi-Fi: %+v", w.agent.Status())
	}
	before := w.box.Load()
	w.wifi.Store([2]string{"FreeWifi", ""})
	w.tick()
	w.tick()
	if w.box.Load() != before {
		t.Fatalf("the relay kept calling the box on another Wi-Fi (%d → %d)", before, w.box.Load())
	}
	if st := w.agent.Status(); st.State != StatusWrongNetwork {
		t.Fatalf("Status = %+v, want wrong-network", st)
	}
	if !w.hub.last().Released {
		t.Fatal("the lease was not handed back")
	}
}

// TestAgent_LocationRefusedChecksTheBoxOnlyAndSaysSo: no SSID, no BSSID.
func TestAgent_LocationRefusedChecksTheBoxOnlyAndSaysSo(t *testing.T) {
	w := newNetWorld(t)
	w.wifi.Store([2]string{"", ""})
	w.tick()
	st := w.agent.Status()
	if st.State != StatusActive || st.Check != CheckBoxIdentity || st.Reason == "" {
		t.Fatalf("Status = %+v, want active on the box identity alone, with a reason", st)
	}
}

func TestAgent_AnotherBoxStandsDown(t *testing.T) {
	w := newNetWorld(t)
	w.pin.Store("0000")
	w.tick()
	w.tick()
	if w.box.Load() != 0 {
		t.Fatal("a relay in front of another box called it")
	}
	if st := w.agent.Status(); st.State != StatusWrongNetwork {
		t.Fatalf("Status = %+v", st)
	}
}

func TestAgent_StatusFollowsTheHub(t *testing.T) {
	w := newNetWorld(t)
	if st := w.agent.Status(); st.State != StatusStarting {
		t.Fatalf("before the first round: %+v", st)
	}
	w.hub.mu.Lock()
	w.hub.lease, w.hub.reason = leaseStandby, "another relay holds the lease"
	w.hub.mu.Unlock()
	w.tick()
	if st := w.agent.Status(); st.State != StatusStandby || st.Reason != "another relay holds the lease" {
		t.Fatalf("Status = %+v, want standby with the hub's reason", st)
	}
	if w.box.Load() != 0 {
		t.Fatal("a standby relay called the box")
	}
	w.hub.mu.Lock()
	w.hub.down = true
	w.hub.mu.Unlock()
	w.tick()
	if st := w.agent.Status(); st.State != StatusOffline {
		t.Fatalf("Status = %+v, want offline", st)
	}
}
