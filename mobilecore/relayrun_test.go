package mobilecore

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/httpkit"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// The phone relay's loop in the core (#45). The whole path against the real
// hub is internal/hub/e2e (TestE2E_PhoneRelay…); these pin what is the
// phone's own: refusals, the status the service shows, the Wi-Fi check on
// the owner's signed network, and the sockets bound to the home Wi-Fi.

// fakeNet is RelayNetwork as the Kotlin side implements it.
type fakeNet struct {
	ssid, bssid string
	refuse      atomic.Bool
	binds       atomic.Int32
}

func (n *fakeNet) SSID() string  { return n.ssid }
func (n *fakeNet) BSSID() string { return n.bssid }
func (n *fakeNet) LANIP() string { return "192.168.1.60" }
func (n *fakeNet) BindSocket(int) bool {
	n.binds.Add(1)
	return !n.refuse.Load()
}

// relayHub grants whatever lease it is told and serves the owner's bundle.
type relayHub struct {
	mu     sync.Mutex
	bundle trust.Signed[trust.Bundle]
	lease  string
	beats  []map[string]any
}

func (h *relayHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch r.Method + " " + r.URL.Path {
	case "POST /relay/v1/heartbeat":
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		h.beats = append(h.beats, req)
		_ = json.NewEncoder(w).Encode(map[string]any{"lease": h.lease, "epoch": 1, "ttlSeconds": 60,
			"bundleVersion": 1, "reason": "another relay holds the lease"})
	case "GET /relay/v1/trust":
		_ = json.NewEncoder(w).Encode(h.bundle)
	case "GET /relay/v1/commands":
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	case "POST /relay/v1/snapshots":
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (h *relayHub) released() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, b := range h.beats {
		if r, _ := b["released"].(bool); r {
			return true
		}
	}
	return false
}

type relayBench struct {
	core *Core
	hub  *relayHub
	// boxCalls counts the HTTP requests that reached the box: each would
	// be a login on a real one. The certificate probe is a bare TLS
	// handshake and does not count.
	boxCalls atomic.Int32
}

// newRelayBench is a paired phone pointed at a box whose certificate the
// owner signed, with the household's Wi-Fi "MonWiFi-2G".
func newRelayBench(t *testing.T, lease string) *relayBench {
	t.Helper()
	b := &relayBench{core: newCore(t, nil), hub: &relayHub{lease: lease}}
	box := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		b.boxCalls.Add(1)
		http.Error(w, "no", http.StatusNotFound)
	}))
	t.Cleanup(box.Close)
	pin := httpkit.CertFingerprint(box.Certificate().Raw)
	if err := b.core.Configure(box.URL, "zte_f6600p", pin); err != nil {
		t.Fatal(err)
	}
	owner, _ := trust.Generate(rand.Reader)
	relay, _ := trust.Generate(rand.Reader)
	s, err := trust.Sign(owner, trust.DomainBundle, trust.Bundle{
		TenantID: "T1", Version: 1, IssuedAt: time.Now(), Owner: owner.Public(),
		Network: trust.Network{SSIDs: []string{"MonWiFi-2G"}, RouterPin: pin},
		Relays:  []trust.Relay{{AgentID: "A1", Kind: "dedicated", Keys: relay.Public()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b.hub.bundle = trust.Signed[trust.Bundle]{Payload: s.Payload, Sig: s.Sig}
	hub := httptest.NewServer(b.hub)
	t.Cleanup(hub.Close)
	if err := b.core.saveRelay(context.Background(), agent.StateData{
		HubURL: hub.URL, AgentID: "A1", TenantID: "T1", Token: "kga_test",
		Identity: relay.Marshal(), OwnerKey: owner.Public().SigningKey(),
	}); err != nil {
		t.Fatal(err)
	}
	return b
}

func status(t *testing.T, c *Core) relayStatusJSON {
	t.Helper()
	var st relayStatusJSON
	if err := json.Unmarshal([]byte(c.RelayStatus()), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func waitForState(t *testing.T, c *Core, want string) relayStatusJSON {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st := status(t, c); st.State == want {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("relay state = %+v, want %s", status(t, c), want)
	return relayStatusJSON{}
}

func TestRelayStart_Refusals(t *testing.T) {
	c := newCore(t, nil)
	if st := status(t, c); st.Running || st.State != "stopped" {
		t.Fatalf("before start: %+v", st)
	}
	if err := c.RelayStart(nil); err == nil {
		t.Fatal("started without a box to relay for")
	}
	if err := c.Configure("https://192.168.1.1", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := c.RelayStart(nil); err == nil || !strings.Contains(err.Error(), "enroll") {
		t.Fatalf("RelayStart before pairing = %v, want not enrolled", err)
	}
	if err := c.StartDemo(); err != nil {
		t.Fatal(err)
	}
	if err := c.RelayStart(nil); err != errRelayDemo {
		t.Fatalf("RelayStart in the demo = %v, want %v", err, errRelayDemo)
	}
	if c.RelayRunning() {
		t.Fatal("a refused start left a relay running")
	}
}

func TestRelay_OnTheHomeWiFiStandsByWhenTheHubSaysSo(t *testing.T) {
	b := newRelayBench(t, "standby")
	net := &fakeNet{ssid: "MonWiFi-2G"}
	if err := b.core.RelayStart(net); err != nil {
		t.Fatal(err)
	}
	st := waitForState(t, b.core, agent.StatusStandby)
	if !st.Running || st.Check != agent.CheckSignature || st.Reason == "" {
		t.Fatalf("status = %+v, want standby on the signature with the hub's reason", st)
	}
	if b.boxCalls.Load() != 0 {
		t.Fatal("a standby relay reached the box")
	}
	if net.binds.Load() == 0 {
		t.Fatal("the sockets were not bound to the home Wi-Fi")
	}
	b.core.RelayStop()
	if st := status(t, b.core); st.Running || st.State != "stopped" {
		t.Fatalf("after stop: %+v", st)
	}
	if b.core.bindNet.Load() != nil {
		t.Fatal("the sockets stay bound after the relay stopped")
	}
}

// TestRelay_WrongWiFiNeverReachesTheBox: the hub grants the lease, the
// phone is on a neighbour's Wi-Fi by the owner's signed network.
func TestRelay_WrongWiFiNeverReachesTheBox(t *testing.T) {
	b := newRelayBench(t, "active")
	if err := b.core.RelayStart(&fakeNet{ssid: "Voisin"}); err != nil {
		t.Fatal(err)
	}
	st := waitForState(t, b.core, agent.StatusWrongNetwork)
	if !strings.Contains(st.Reason, "Wi-Fi") {
		t.Fatalf("status = %+v, want the reason", st)
	}
	time.Sleep(100 * time.Millisecond)
	if n := b.boxCalls.Load(); n != 0 {
		t.Fatalf("a relay on the wrong Wi-Fi reached the box %d times", n)
	}
	if !b.hub.released() {
		t.Fatal("the lease the hub granted was not handed back")
	}
}

// TestRelay_LocationRefusedChecksTheBoxAndSaysSo: no SSID, no BSSID.
func TestRelay_LocationRefusedChecksTheBoxAndSaysSo(t *testing.T) {
	b := newRelayBench(t, "active")
	if err := b.core.RelayStart(&fakeNet{}); err != nil {
		t.Fatal(err)
	}
	st := waitForState(t, b.core, agent.StatusActive)
	if st.Check != agent.CheckBoxIdentity || !strings.Contains(st.Reason, "location") {
		t.Fatalf("status = %+v, want the box identity alone, said", st)
	}
	b.core.RelayStop()
	if !b.hub.released() {
		t.Fatal("stopping did not hand the lease back")
	}
}

// TestDialControl_BindsToTheHomeWiFiOnlyWhileRelaying: every socket of the
// core goes through RelayNetwork.BindSocket while the relay runs, and a
// refusal (not on the home Wi-Fi) is a refused connection, never a fallback
// to mobile data.
func TestDialControl_BindsToTheHomeWiFiOnlyWhileRelaying(t *testing.T) {
	c := newCore(t, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	dial := func() error {
		conn, err := (&net.Dialer{Control: c.dialControl}).Dial("tcp", addr)
		if err == nil {
			_ = conn.Close()
		}
		return err
	}
	if err := dial(); err != nil {
		t.Fatalf("no relay: %v", err)
	}
	n := &fakeNet{}
	var rn RelayNetwork = n
	c.bindNet.Store(&rn)
	if err := dial(); err != nil || n.binds.Load() != 1 {
		t.Fatalf("bound dial = %v, binds %d", err, n.binds.Load())
	}
	n.refuse.Store(true)
	if err := dial(); err == nil {
		t.Fatal("a socket the phone refused to bind still connected")
	}
	if err := c.Configure("https://192.168.1.1", "", ""); err != nil {
		t.Fatal(err)
	}
	if c.routerCfg.Opts.DialControl == nil {
		t.Fatal("Configure does not bind the box's sockets")
	}
}

// TestRelayFindBox_PicksTheBoxTheOwnerSigned: a relay phone never went
// through the connection screen; it finds the box among the addresses the
// service suggests by the certificate pin in the owner's bundle.
func TestRelayFindBox_PicksTheBoxTheOwnerSigned(t *testing.T) {
	c := newCore(t, nil)
	box := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer box.Close()
	// httptest's TLS servers all share one certificate: "another box" is a
	// server that presents none.
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer plain.Close()
	other := struct{ URL string }{"https://" + strings.TrimPrefix(plain.URL, "http://")}
	pin := httpkit.CertFingerprint(box.Certificate().Raw)

	if _, err := c.RelayFindBox(box.URL); err == nil {
		t.Fatal("found a box before pairing")
	}
	owner, _ := trust.Generate(rand.Reader)
	relay, _ := trust.Generate(rand.Reader)
	s, _ := trust.Sign(owner, trust.DomainBundle, trust.Bundle{TenantID: "T1", Version: 1, IssuedAt: time.Now(),
		Owner: owner.Public(), Network: trust.Network{RouterPin: pin}})
	if err := c.saveRelay(context.Background(), agent.StateData{HubURL: "http://hub", AgentID: "A1", TenantID: "T1",
		Token: "kga", Identity: relay.Marshal(), OwnerKey: owner.Public().SigningKey(),
		Bundle: trust.Signed[trust.Bundle]{Payload: s.Payload, Sig: s.Sig}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RelayFindBox("https://127.0.0.1:1, " + other.URL); err == nil {
		t.Fatal("accepted a box the owner did not sign")
	}
	got, err := c.RelayFindBox("https://127.0.0.1:1, " + other.URL + "," + box.URL)
	if err != nil || got != box.URL {
		t.Fatalf("RelayFindBox = %q, %v; want %s", got, err, box.URL)
	}
	if c.routerCfg.BaseURL != box.URL || c.routerCfg.Opts.PinSHA256 != pin {
		t.Fatalf("the core is not pointed at the box: %+v", c.routerCfg)
	}
}
