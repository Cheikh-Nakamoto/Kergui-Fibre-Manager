package mobilecore

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// « Récupérer à la maison » finds the relay by itself (#117, audit C4): the
// new phone asks the addresses of the home network for the relay protocol's
// challenge, and keeps the relays of the signed-in household, signed by the
// owner the hub knows, the most up to date first. A neighbour's relay, a
// relay not paired yet or a stranger's bundle are counted, never chosen.

type foundAnswer struct {
	Relays []struct {
		Address string `json:"address"`
		AgentID string `json:"agentId"`
		Version uint64 `json:"version"`
	} `json:"relays"`
	Others int `json:"others"`
}

func decodeFound(t *testing.T, raw string) foundAnswer {
	t.Helper()
	var a foundAnswer
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	return a
}

// lanRelayOf is a relay of tenant, paired with the owner key ownerKey,
// holding bundle, listening for recovery on 127.0.0.1.
func lanRelayOf(t *testing.T, tenant string, ownerKey []byte, relay *trust.Identity, bundle string) string {
	t.Helper()
	st := &agent.MemoryState{Data: agent.StateData{
		AgentID: agentName(0), TenantID: tenant, Token: "kga_test",
		Identity: relay.Marshal(), OwnerKey: ownerKey, Bundle: signedOf(t, bundle),
	}}
	rec := agent.NewRecovery(agent.RecoveryConfig{State: st, Password: func(context.Context) (string, error) { return "x", nil }})
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// closedAddr is an address on this machine where nothing listens.
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func TestFindRelays_TheHouseholdsRelayIsChosenAmongSeveralAnswers(t *testing.T) {
	h := newMemberHousehold(t)
	older := h.bundle
	h.sign(t, map[string]any{"ownerOnly": false}) // the relays follow it: v+1
	ownerKey := ownerKeys(t, h.owner).Ed25519

	// A neighbour's relay on the same Wi-Fi: another household.
	neighbour := newCore(t, nil)
	neighbour.gate.now = h.clock.now
	nRelay := newRelay(t)
	neighbourAddr := lanRelayOf(t, "T-voisin", ownerKeys(t, neighbour).Ed25519, nRelay, bundleWith(t, neighbour, "T-voisin", nRelay))
	// A relay claiming this household, its bundle signed by someone else.
	stranger := newCore(t, nil)
	stranger.gate.now = h.clock.now
	sRelay := newRelay(t)
	strangerAddr := lanRelayOf(t, credTenant, ownerKeys(t, stranger).Ed25519, sRelay, bundleWith(t, stranger, credTenant, sRelay))
	// A relay not paired yet.
	unpaired := httptest.NewServer(agent.NewRecovery(agent.RecoveryConfig{State: &agent.MemoryState{}}))
	t.Cleanup(unpaired.Close)
	// A web page that is no relay, and an address where nothing listens.
	page := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(page.Close)

	// The household's relay that missed the last bundle, listed first, and
	// the one up to date.
	behind := lanRelayOf(t, credTenant, ownerKey, h.relay, older)
	upToDate := lanRelayOf(t, credTenant, ownerKey, h.relay, h.bundle)

	newPhone := newCore(t, nil)
	newPhone.gate.now = h.clock.now
	newPhone.homeCandidatesFn = func(context.Context) []string {
		return []string{
			neighbourAddr, closedAddr(t), strangerAddr, strings.TrimPrefix(page.URL, "http://"),
			behind, strings.TrimPrefix(unpaired.URL, "http://"), upToDate,
		}
	}
	raw, err := newPhone.FindRelaysAtHome(credTenant, h.bundle)
	if err != nil {
		t.Fatalf("FindRelaysAtHome: %v", err)
	}
	got := decodeFound(t, raw)
	if len(got.Relays) != 2 || got.Relays[0].Address != upToDate || got.Relays[1].Address != behind {
		t.Fatalf("relays = %+v, want %s then %s", got.Relays, upToDate, behind)
	}
	if got.Relays[0].AgentID != agentName(0) || got.Relays[0].Version <= got.Relays[1].Version {
		t.Fatalf("the chosen relay = %+v", got.Relays[0])
	}
	if got.Others != 3 {
		t.Fatalf("others = %d, want 3 (the neighbour, the stranger, the unpaired relay)", got.Others)
	}

	// Without the hub's bundle the household is still the filter: the
	// neighbour is never chosen.
	raw, err = newPhone.FindRelaysAtHome(credTenant, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range decodeFound(t, raw).Relays {
		if r.Address == neighbourAddr {
			t.Fatalf("the neighbour's relay was chosen: %+v", r)
		}
	}
}

func TestFindRelays_NoRelayIsAnEmptyList(t *testing.T) {
	c := newCore(t, nil)
	page := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(page.Close)
	var mu sync.Mutex
	called := []string{}
	c.recoveryClient = &http.Client{Timeout: 2 * time.Second, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		called = append(called, r.URL.Host)
		mu.Unlock()
		return http.DefaultTransport.RoundTrip(r)
	})}
	// An Internet address is never asked: the challenge stays on the home network.
	c.homeCandidatesFn = func(context.Context) []string {
		return []string{"8.8.8.8", "hub.kergui.org", closedAddr(t), strings.TrimPrefix(page.URL, "http://")}
	}
	raw, err := c.FindRelaysAtHome(credTenant, "")
	if err != nil {
		t.Fatal(err)
	}
	if raw != `{"relays":[],"others":0}` {
		t.Fatalf("answer = %s", raw)
	}
	for _, host := range called {
		if strings.HasPrefix(host, "8.8.8.8") || strings.HasPrefix(host, "hub.") {
			t.Fatalf("the core asked %s", host)
		}
	}
}

func TestFindRelays_Refusals(t *testing.T) {
	h := newMemberHousehold(t)
	c := newCore(t, nil)
	c.homeCandidatesFn = func(context.Context) []string { return nil }
	if _, err := c.FindRelaysAtHome(" ", h.bundle); err == nil {
		t.Fatal("no household accepted")
	}
	if _, err := c.FindRelaysAtHome("T-autre", h.bundle); err == nil {
		t.Fatal("the hub's bundle of another household accepted")
	}
	if _, err := c.FindRelaysAtHome(credTenant, "{nope"); err == nil {
		t.Fatal("a malformed bundle accepted")
	}
}

// The addresses tried: the devices the app marked as relays first, then
// the box's other devices, then the rest of this phone's network; never this
// phone, never twice. Read on the demo box (the mock router).
func TestFindRelays_CandidatesComeFromTheBoxList(t *testing.T) {
	c := approvalDemo(t)
	status, body := call(t, c, "GET", "/api/devices", "")
	if status != http.StatusOK {
		t.Fatalf("GET /api/devices = %d %s", status, body)
	}
	var list []struct{ MAC, IP, Mark string }
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	var relayMAC, relayIP string
	for _, d := range list[1:] {
		if d.Mark == "" && d.IP != "" {
			relayMAC, relayIP = strings.ToLower(d.MAC), d.IP
			break
		}
	}
	if relayMAC == "" {
		t.Fatal("no demo device to mark as a relay")
	}
	c.relays.set(c.boxID(), []string{relayMAC})

	got := c.homeCandidates(context.Background())
	if len(got) == 0 || got[0] != relayIP {
		t.Fatalf("first candidate = %v, want the relay %s", first(got), relayIP)
	}
	seen := map[string]bool{}
	for _, a := range got {
		if seen[a] {
			t.Fatalf("%s tried twice", a)
		}
		seen[a] = true
	}
	if seen[demoPhoneIP] {
		t.Fatalf("this phone (%s) is tried", demoPhoneIP)
	}
	for _, d := range list {
		if d.IP != "" && d.IP != demoPhoneIP && !seen[d.IP] {
			t.Fatalf("the box's device %s is not tried", d.IP)
		}
	}
	// The listed devices come before the rest of the network.
	listed := 0
	for _, d := range list {
		if d.IP != "" && d.IP != demoPhoneIP {
			listed++
		}
	}
	for _, a := range got[:listed] {
		found := false
		for _, d := range list {
			found = found || d.IP == a
		}
		if !found {
			t.Fatalf("%s is tried before a listed device: %v", a, got[:listed])
		}
	}
	if !seen["192.168.1.1"] || !seen["192.168.1.254"] || seen["192.168.1.0"] || seen["192.168.1.255"] || len(got) != 253 {
		t.Fatalf("the network sweep = %d addresses", len(got))
	}
}

// Off any home network (no private address), only the box's list is tried.
func TestFindRelays_NoSweepOffTheHomeNetwork(t *testing.T) {
	for _, ip := range []string{"", "100.72.1.9", "127.0.0.1", "2001:db8::1"} {
		if got := sweep(ip); len(got) != 0 {
			t.Fatalf("sweep(%q) = %d addresses", ip, len(got))
		}
	}
	c := newCore(t, nil)
	c.self.set(func(string) domain.IP { return domain.IP{} })
	if got := c.homeCandidates(context.Background()); len(got) != 0 {
		t.Fatalf("a phone off the Wi-Fi, never pointed at a box, tries %v", got)
	}
}

func first(a []string) string {
	if len(a) == 0 {
		return ""
	}
	return a[0]
}
