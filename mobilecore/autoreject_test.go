package mobilecore

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
)

// The phone running the app, as the box sees it in these tests.
const testPhoneIP = "192.168.1.250"

type autoRejectRun struct {
	Enabled  bool `json:"enabled"`
	Entitled bool `json:"entitled"`
	Refused  []struct {
		MAC  string `json:"mac"`
		Name string `json:"name"`
	} `json:"refused"`
	Pending int `json:"pending"`
}

type autoRejectStatus struct {
	Enabled bool `json:"enabled"`
	Devices []struct {
		MAC   string `json:"mac"`
		State string `json:"state"`
	} `json:"devices"`
}

// autoRejectCore is the gated core on the F6600P mock, its rule clock the
// fake clock, the phone at testPhoneIP.
func autoRejectCore(t *testing.T) (*Core, *fakeClock, *mockrouter.F6600P) {
	t.Helper()
	c, clk, box := gatedCore(t)
	c.clock.set(clk.now)
	c.self.set(func(string) domain.IP { return domain.MustIP(testPhoneIP) })
	return c, clk, box
}

func runAutoReject(t *testing.T, c *Core) autoRejectRun {
	t.Helper()
	status, body := call(t, c, "POST", "/api/autoreject/run", "")
	if status != http.StatusOK {
		t.Fatalf("POST /api/autoreject/run = %d %s", status, body)
	}
	var out autoRejectRun
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode run: %v\n%s", err, body)
	}
	return out
}

func autoRejectState(t *testing.T, c *Core) autoRejectStatus {
	t.Helper()
	status, body := call(t, c, "GET", "/api/autoreject", "")
	if status != http.StatusOK {
		t.Fatalf("GET /api/autoreject = %d %s", status, body)
	}
	var out autoRejectStatus
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode status: %v\n%s", err, body)
	}
	return out
}

func setAutoReject(t *testing.T, c *Core, on bool) (int, string) {
	t.Helper()
	body := `{"enabled":false}`
	if on {
		body = `{"enabled":true}`
	}
	return call(t, c, "PUT", "/api/autoreject", body)
}

func journalHas(t *testing.T, c *Core, sub string) int {
	t.Helper()
	n := 0
	for _, e := range decodeJournal(t, c, 0, 0) {
		if strings.Contains(e.Msg, sub) {
			n++
		}
	}
	return n
}

// TestAutoRejectOnTheMockBox (#35): with Max the rule is turned on, the
// devices present are accepted, a stranger is refused at the Wi-Fi access
// control 15 min after it is first seen (not at 14), once, with a journal
// line, while an approved one, a renamed one and the phone itself stay.
func TestAutoRejectOnTheMockBox(t *testing.T) {
	c, clk, box := autoRejectCore(t)
	present := aDevice(t, c)

	// Without Max, turning it on is refused and nothing is kept.
	if status, body := setAutoReject(t, c, true); !refused(t, status, body) || !strings.Contains(body, `"feature": "auto_reject"`) {
		t.Fatalf("turn on without a token = %d %s", status, body)
	}
	c.SetEntitlement(signToken(t, testPriv, entitlement.TierBasic, mockSerial, gateT0))
	if status, body := setAutoReject(t, c, true); !refused(t, status, body) {
		t.Fatalf("turn on with Basic = %d %s", status, body)
	}
	if autoRejectState(t, c).Enabled {
		t.Fatal("a refused activation was kept")
	}

	c.SetEntitlement(signToken(t, testPriv, entitlement.TierMax, mockSerial, gateT0))
	if status, body := setAutoReject(t, c, true); status != http.StatusOK {
		t.Fatalf("turn on with Max = %d %s", status, body)
	}
	st := autoRejectState(t, c)
	if !st.Enabled || len(st.Devices) == 0 {
		t.Fatalf("status = %+v, want on with the present devices", st)
	}
	for _, d := range st.Devices {
		if d.State != "accepted" {
			t.Fatalf("present device %s is %s, want accepted", d.MAC, d.State)
		}
	}

	const (
		strangerMAC = "02:00:00:00:00:a1"
		approvedMAC = "02:00:00:00:00:a2"
		renamedMAC  = "02:00:00:00:00:a3"
		phoneMAC    = "02:00:00:00:00:a4" // the phone, with a fresh random MAC
	)
	box.Join(mockrouter.Station{MAC: strangerMAC, Hostname: "Inconnu", IP: "192.168.1.201"})
	box.Join(mockrouter.Station{MAC: approvedMAC, IP: "192.168.1.202"})
	box.Join(mockrouter.Station{MAC: renamedMAC, IP: "192.168.1.203"})
	box.Join(mockrouter.Station{MAC: phoneMAC, IP: testPhoneIP})
	if run := runAutoReject(t, c); !run.Enabled || !run.Entitled || len(run.Refused) != 0 {
		t.Fatalf("first sight = %+v", run)
	}

	// "Accepter" on one notification, a name for another.
	if status, body := call(t, c, "POST", "/api/devices/"+approvedMAC+"/accept", ""); status != http.StatusOK {
		t.Fatalf("accept = %d %s", status, body)
	}
	devices(t, c) // the sheet's list puts it in the inventory
	if status, body := call(t, c, "PATCH", "/api/devices/"+renamedMAC, `{"custom_name":"Tablette"}`); status != http.StatusOK {
		t.Fatalf("rename = %d %s", status, body)
	}

	clk.set(gateT0.Add(14 * time.Minute))
	if run := runAutoReject(t, c); len(run.Refused) != 0 || box.WiFiBlocked(strangerMAC) {
		t.Fatalf("refused at 14 min: %+v", run)
	}

	clk.set(gateT0.Add(15 * time.Minute))
	run := runAutoReject(t, c)
	if len(run.Refused) != 1 || run.Refused[0].MAC != strangerMAC || run.Refused[0].Name != "Inconnu" {
		t.Fatalf("at 15 min: %+v, want the stranger only", run)
	}
	if !box.WiFiBlocked(strangerMAC) || !wifiDenied(t, c)[strangerMAC] {
		t.Fatal("the stranger is not refused at the box's Wi-Fi access control")
	}
	for _, mac := range []string{approvedMAC, renamedMAC, phoneMAC, present} {
		if box.WiFiBlocked(mac) {
			t.Fatalf("%s was refused", mac)
		}
	}
	if journalHas(t, c, "auto : non accepté") != 1 {
		t.Fatal("the refusal is not in the journal")
	}

	clk.set(gateT0.Add(time.Hour))
	if run := runAutoReject(t, c); len(run.Refused) != 0 {
		t.Fatalf("refused twice: %+v", run)
	}
	if journalHas(t, c, "auto : non accepté") != 1 {
		t.Fatal("a second refusal reached the journal")
	}

	// Off (free): nothing more is refused.
	c.SetEntitlement("")
	if status, body := setAutoReject(t, c, false); status != http.StatusOK {
		t.Fatalf("turn off without a token = %d %s", status, body)
	}
	box.Join(mockrouter.Station{MAC: "02:00:00:00:00:a5", IP: "192.168.1.205"})
	runAutoReject(t, c)
	clk.set(gateT0.Add(2 * time.Hour))
	if run := runAutoReject(t, c); run.Enabled || len(run.Refused) != 0 || box.WiFiBlocked("02:00:00:00:00:a5") {
		t.Fatalf("turned off, yet: %+v", run)
	}
}

// TestAutoRejectStopsWhenMaxLapses (#35): past the pass and its grace, a
// round refuses nothing (and says why), and what it refused before is given
// back for free.
func TestAutoRejectStopsWhenMaxLapses(t *testing.T) {
	c, clk, box := autoRejectCore(t)
	c.SetEntitlement(signToken(t, testPriv, entitlement.TierMax, mockSerial, gateT0))
	if status, body := setAutoReject(t, c, true); status != http.StatusOK {
		t.Fatalf("turn on = %d %s", status, body)
	}
	const before, after = "02:00:00:00:00:b1", "02:00:00:00:00:b2"
	box.Join(mockrouter.Station{MAC: before, IP: "192.168.1.211"})
	runAutoReject(t, c)
	clk.set(gateT0.Add(15 * time.Minute))
	if run := runAutoReject(t, c); len(run.Refused) != 1 {
		t.Fatalf("with Max: %+v", run)
	}

	// The token is valid 10 days, plus 3 of grace.
	lapsed := gateT0.Add(14 * gateDay)
	clk.set(lapsed)
	box.Join(mockrouter.Station{MAC: after, IP: "192.168.1.212"})
	runAutoReject(t, c)
	clk.set(lapsed.Add(time.Hour))
	run := runAutoReject(t, c)
	if run.Entitled || len(run.Refused) != 0 || box.WiFiBlocked(after) {
		t.Fatalf("after the pass lapsed: %+v, refused at the box %v", run, box.WiFiBlocked(after))
	}

	// "Rendre l'accès" is free, and accepts it for good.
	if status, body := call(t, c, "POST", "/api/devices/"+before+"/wifi-unblock", ""); status != http.StatusOK {
		t.Fatalf("restore after the pass lapsed = %d %s", status, body)
	}
	if box.WiFiBlocked(before) {
		t.Fatal("still refused after restore")
	}
	for _, d := range autoRejectState(t, c).Devices {
		if d.MAC == before && d.State != "accepted" {
			t.Fatalf("restored device is %s, want accepted", d.State)
		}
	}
}

// TestAutoRejectInTheDemo: the demo opens every tier, so the rule can be
// turned on and off there.
func TestAutoRejectInTheDemo(t *testing.T) {
	c := newCore(t, nil)
	if err := c.StartDemo(); err != nil {
		t.Fatal(err)
	}
	if status, body := call(t, c, "POST", "/api/login", `{"username":"admin","password":"admin"}`); status != http.StatusOK {
		t.Fatalf("login = %d %s", status, body)
	}
	if status, body := setAutoReject(t, c, true); status != http.StatusOK {
		t.Fatalf("turn on in the demo = %d %s", status, body)
	}
	if st := autoRejectState(t, c); !st.Enabled || len(st.Devices) != 5 {
		t.Fatalf("demo status = %+v, want on with its 3 devices, « ce téléphone » and the owner's other phone", st)
	}
	if run := runAutoReject(t, c); !run.Entitled || len(run.Refused) != 0 {
		t.Fatalf("demo round = %+v", run)
	}
	if status, body := setAutoReject(t, c, false); status != http.StatusOK {
		t.Fatalf("turn off = %d %s", status, body)
	}
}

// runAutoRejectWithRelay is one watch-service round when the app has learned
// from the hub when the household's relay agent last sent a heartbeat.
func runAutoRejectWithRelay(t *testing.T, c *Core, last time.Time) (autoRejectRun, string) {
	t.Helper()
	status, body := call(t, c, "POST", "/api/autoreject/run",
		`{"relayHeartbeatAt":"`+last.UTC().Format(time.RFC3339)+`"}`)
	if status != http.StatusOK {
		t.Fatalf("POST /api/autoreject/run = %d %s", status, body)
	}
	var out autoRejectRun
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode run: %v\n%s", err, body)
	}
	var enf struct {
		Enforcer string `json:"enforcer"`
	}
	_ = json.Unmarshal([]byte(body), &enf)
	return out, enf.Enforcer
}

// TestAutoRejectStandsDownForTheRelay (#36): while the relay agent's last
// hub heartbeat is under 2 minutes old, the phone's round refuses nothing —
// the relay does — and once the relay has gone quiet the phone takes over.
func TestAutoRejectStandsDownForTheRelay(t *testing.T) {
	c, clk, box := autoRejectCore(t)
	c.SetEntitlement(signToken(t, testPriv, entitlement.TierMax, mockSerial, gateT0))
	if status, body := setAutoReject(t, c, true); status != http.StatusOK {
		t.Fatalf("turn on = %d %s", status, body)
	}
	const strangerMAC = "02:00:00:00:00:c1"
	box.Join(mockrouter.Station{MAC: strangerMAC, IP: "192.168.1.221"})
	if _, who := runAutoRejectWithRelay(t, c, time.Time{}); who != "phone" {
		t.Fatalf("no relay yet: enforcer = %q, want phone", who)
	}

	clk.set(gateT0.Add(20 * time.Minute)) // the stranger is due
	relayBeat := clk.now().Add(-90 * time.Second)
	run, who := runAutoRejectWithRelay(t, c, relayBeat)
	if who != "relay" || len(run.Refused) != 0 || box.WiFiBlocked(strangerMAC) {
		t.Fatalf("relay heard 90 s ago: enforcer %q, %+v — the phone must stand down", who, run)
	}
	if !run.Enabled {
		t.Fatalf("standing down is not turning off: %+v", run)
	}

	clk.set(clk.now().Add(time.Minute)) // the same heartbeat is now 2 min 30 old
	run, who = runAutoRejectWithRelay(t, c, relayBeat)
	if who != "phone" || len(run.Refused) != 1 || !box.WiFiBlocked(strangerMAC) {
		t.Fatalf("relay silent: enforcer %q, %+v — the phone must take over", who, run)
	}
}

// tellEnforcer is the app handing the core the hub's GET
// router/auto-reject answer, which the watch service's rounds then go by.
func tellEnforcer(t *testing.T, c *Core, answer string) string {
	t.Helper()
	status, body := call(t, c, "PUT", "/api/autoreject/enforcer", answer)
	if status != http.StatusOK {
		t.Fatalf("PUT /api/autoreject/enforcer %s = %d %s", answer, status, body)
	}
	var out struct {
		Enforcer string `json:"enforcer"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	return out.Enforcer
}

func runEnforcer(t *testing.T, c *Core) (autoRejectRun, string) {
	t.Helper()
	status, body := call(t, c, "POST", "/api/autoreject/run", "")
	if status != http.StatusOK {
		t.Fatalf("POST /api/autoreject/run = %d %s", status, body)
	}
	var out autoRejectRun
	var enf struct {
		Enforcer string `json:"enforcer"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	_ = json.Unmarshal([]byte(body), &enf)
	return out, enf.Enforcer
}

// TestAutoRejectGoesByTheHubsLastAnswer (#36): the app hands the core what
// the hub answered; every round after it — the Android watch service's,
// which never calls the hub, included — stands down while that relay
// heartbeat is under 2 minutes old on the core's own clock, and the phone
// takes over at once when the hub names the phone, or by itself once the
// answer is too old (the app closed, the hub out of reach).
func TestAutoRejectGoesByTheHubsLastAnswer(t *testing.T) {
	c, clk, box := autoRejectCore(t)
	c.SetEntitlement(signToken(t, testPriv, entitlement.TierMax, mockSerial, gateT0))
	if status, body := setAutoReject(t, c, true); status != http.StatusOK {
		t.Fatalf("turn on = %d %s", status, body)
	}
	const s1, s2 = "02:00:00:00:00:e1", "02:00:00:00:00:e2"
	box.Join(mockrouter.Station{MAC: s1, IP: "192.168.1.231"})
	if _, who := runEnforcer(t, c); who != "phone" {
		t.Fatalf("before any answer: %q, want phone", who)
	}

	clk.set(gateT0.Add(20 * time.Minute)) // s1 is due
	if who := tellEnforcer(t, c, `{"enforcer":"relay","agentId":"A1","relayHeartbeatAgeSeconds":30}`); who != "relay" {
		t.Fatalf("the core did not take the hub's answer: %q", who)
	}
	logins := box.Logins()
	if run, who := runEnforcer(t, c); who != "relay" || len(run.Refused) != 0 || box.WiFiBlocked(s1) {
		t.Fatalf("relay heard 30 s ago: %q %+v — the phone must stand down", who, run)
	}
	if box.Logins() != logins {
		t.Fatal("the phone logged in to the box while the relay enforces")
	}

	// 1 min 40 later the same answer is 2 min 10 old: the phone takes over.
	clk.set(clk.now().Add(100 * time.Second))
	if run, who := runEnforcer(t, c); who != "phone" || len(run.Refused) != 1 || !box.WiFiBlocked(s1) {
		t.Fatalf("answer too old: %q %+v — the phone must take over", who, run)
	}

	// The hub names the phone (the relay shut down): at once.
	box.Join(mockrouter.Station{MAC: s2, IP: "192.168.1.232"})
	runEnforcer(t, c)
	clk.set(clk.now().Add(20 * time.Minute))
	tellEnforcer(t, c, `{"enforcer":"relay","relayHeartbeatAgeSeconds":0}`)
	if who := tellEnforcer(t, c, `{"enforcer":"phone"}`); who != "phone" {
		t.Fatalf("after the hub named the phone: %q", who)
	}
	if run, who := runEnforcer(t, c); who != "phone" || len(run.Refused) != 1 || !box.WiFiBlocked(s2) {
		t.Fatalf("the hub names the phone: %q %+v", who, run)
	}

	// Anything else is refused and changes nothing.
	for _, bad := range []string{`{"enforcer":"hub"}`, `{"enforcer":"relay"}`, `{"enforcer":"relay","relayHeartbeatAgeSeconds":-5}`, `nope`} {
		if status, body := call(t, c, "PUT", "/api/autoreject/enforcer", bad); status != http.StatusBadRequest {
			t.Errorf("PUT enforcer %s = %d %s, want 400", bad, status, body)
		}
	}
}
