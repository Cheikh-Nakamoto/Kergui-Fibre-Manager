package mobilecore

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
)

// The closed Wi-Fi in the phone's core (RFC-007 D7, D8; #107): the routes on
// the F6600P mock and a fake clock, the tiers (a lapsed pass still opens,
// never closes), the approvals written on the box while closed, the
// « déconnecté du Wi-Fi » flag of a block, the re-close at the deadline and
// the demo. Every test ends with no write that read a list the wrong way.

const (
	lockPhone    = "02:aa:cc:00:01:01" // this phone, at testPhoneIP
	lockLaptop   = "02:aa:cc:00:01:02"
	lockStranger = "02:aa:cc:00:01:03"
	lockRelay    = "02:aa:cc:00:01:04"
	lockAP1      = "DEV.WIFI.AP1"
	lockAP5      = "DEV.WIFI.AP5"
)

type lockStatus struct {
	State     string   `json:"state"`
	Intended  bool     `json:"intended"`
	Since     string   `json:"since"`
	Allowed   int      `json:"allowed"`
	Places    int      `json:"places"`
	Known     bool     `json:"places_known"`
	FoundOpen bool     `json:"found_open"`
	Cut       []string `json:"cut"`
	Household []string `json:"household_aps"`
	Window    struct {
		Until       string `json:"until"`
		MinutesLeft int    `json:"minutes_left"`
		CanExtend   bool   `json:"can_extend"`
	} `json:"window"`
}

type lockPreview struct {
	Devices []string `json:"devices"`
	Always  []string `json:"always"`
	Relays  []string `json:"relays"`
	Self    string   `json:"self"`
	Private bool     `json:"self_private"`
	Places  int      `json:"places"`
	Closed  bool     `json:"closed"`
	Full    *struct {
		Limit int `json:"limit"`
		Used  int `json:"used"`
	} `json:"full"`
}

// lockCore is the gated core on the F6600P mock, Max for the box, its clock
// the fake one, this phone and an approved laptop on the box.
func lockCore(t *testing.T) (*Core, *fakeClock, *mockrouter.F6600P) {
	t.Helper()
	c, clk, box := autoRejectCore(t)
	box.Join(mockrouter.Station{MAC: lockPhone, Hostname: "Mon-Telephone", IP: testPhoneIP, AP: lockAP1})
	box.Join(mockrouter.Station{MAC: lockLaptop, Hostname: "Laptop", IP: "192.168.1.30", AP: lockAP5})
	devices(t, c) // the first listing: everyone there is approved
	c.SetEntitlement(signToken(t, testPriv, entitlement.TierMax, mockSerial, gateT0))
	return c, clk, box
}

func lockState(t *testing.T, c *Core) lockStatus {
	t.Helper()
	status, body := call(t, c, "GET", "/api/wifi/lock", "")
	if status != http.StatusOK {
		t.Fatalf("GET /api/wifi/lock = %d %s", status, body)
	}
	var out lockStatus
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	return out
}

func decodePreview(t *testing.T, body string) lockPreview {
	t.Helper()
	var out lockPreview
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	return out
}

func closeWiFi(t *testing.T, c *Core) lockPreview {
	t.Helper()
	status, body := call(t, c, "POST", "/api/wifi/lock/close", "")
	if status != http.StatusOK {
		t.Fatalf("POST /api/wifi/lock/close = %d %s", status, body)
	}
	return decodePreview(t, body)
}

func admitted(box *mockrouter.F6600P, mac string) bool {
	return box.Admits(lockAP1, mac) && box.Admits(lockAP5, mac)
}

func noHazard(t *testing.T, box *mockrouter.F6600P) {
	t.Helper()
	if h := box.WiFiHazards(); len(h) != 0 {
		t.Fatalf("a write read a list the wrong way: %v", h)
	}
}

func wantRefused(t *testing.T, what string, status int, body string, feature entitlement.Feature) {
	t.Helper()
	if !refused(t, status, body) || !strings.Contains(body, `"feature": "`+string(feature)+`"`) {
		t.Fatalf("%s = %d %s, want refused for %s", what, status, body, feature)
	}
}

// Close (after its preview), the add window and its extension, « Refermer
// maintenant », then open: what the box and the button say at each step.
func TestWiFiLock_Routes(t *testing.T) {
	c, clk, box := lockCore(t)
	if st := lockState(t, c); st.State != "open" || st.Intended || !st.Known {
		t.Fatalf("status before = %+v", st)
	}

	status, body := call(t, c, "POST", "/api/wifi/lock/close", `{"preview":true}`)
	if status != http.StatusOK {
		t.Fatalf("preview = %d %s", status, body)
	}
	p := decodePreview(t, body)
	if p.Closed || p.Self != lockPhone || !p.Private || !slices.Contains(p.Devices, lockLaptop) || !slices.Contains(p.Always, lockPhone) {
		t.Fatalf("preview = %+v", p)
	}
	if box.WiFiPolicy(lockAP1) != "Disabled" || box.WiFiWrites() != 0 {
		t.Fatal("the preview wrote to the box")
	}

	if p := closeWiFi(t, c); !p.Closed {
		t.Fatalf("close = %+v", p)
	}
	if !admitted(box, lockPhone) || !admitted(box, lockLaptop) || admitted(box, lockStranger) {
		t.Fatal("the closed Wi-Fi admits the wrong devices")
	}
	st := lockState(t, c)
	if st.State != "closed" || !st.Intended || st.Since != gateT0.Format(time.RFC3339) || st.Allowed < 2 {
		t.Fatalf("status closed = %+v", st)
	}

	status, body = call(t, c, "POST", "/api/wifi/lock/window", `{"action":"open"}`)
	if status != http.StatusOK || !strings.Contains(body, `"minutes_left": 5`) {
		t.Fatalf("window open = %d %s", status, body)
	}
	if !box.Admits(lockAP1, lockStranger) {
		t.Fatal("a newcomer cannot join during the add window")
	}
	clk.set(gateT0.Add(2 * time.Minute))
	status, body = call(t, c, "POST", "/api/wifi/lock/window", `{"action":"extend"}`)
	if status != http.StatusOK || !strings.Contains(body, `"minutes_left": 8`) {
		t.Fatalf("extend = %d %s", status, body)
	}
	if st := lockState(t, c); st.State != "window" || st.Window.MinutesLeft != 8 || !st.Window.CanExtend {
		t.Fatalf("status in the window = %+v", st)
	}
	if status, body := call(t, c, "POST", "/api/wifi/lock/window", `{"action":"close"}`); status != http.StatusOK {
		t.Fatalf("close now = %d %s", status, body)
	}
	if st := lockState(t, c); st.State != "closed" || st.Window.Until != "" {
		t.Fatalf("status after « Refermer maintenant » = %+v", st)
	}
	if status, _ := call(t, c, "POST", "/api/wifi/lock/window", `{"action":"jump"}`); status != http.StatusBadRequest {
		t.Fatalf("unknown action = %d, want 400", status)
	}

	if status, body := call(t, c, "POST", "/api/wifi/lock/open", ""); status != http.StatusOK {
		t.Fatalf("open = %d %s", status, body)
	}
	if st := lockState(t, c); st.State != "open" || st.Intended {
		t.Fatalf("status after open = %+v", st)
	}
	if !admitted(box, lockStranger) {
		t.Fatal("the open Wi-Fi refuses a newcomer")
	}
	noHazard(t, box)
}

// RFC-007 D8: closing, the add window and its extension need Max; opening
// needs nothing, even with a pass that lapsed while the Wi-Fi was closed.
// The preview, a read, is free.
func TestWiFiLock_Tiers(t *testing.T) {
	c, clk, box := lockCore(t)
	closeWiFi(t, c)

	// The pass lapsed while the Wi-Fi was closed: nothing changes on the box.
	clk.set(gateT0.Add(10*gateDay + entitlement.OfflineGrace))
	if st := lockState(t, c); st.State != "closed" {
		t.Fatalf("status with a lapsed pass = %+v", st)
	}
	status, body := call(t, c, "POST", "/api/wifi/lock/window", `{"action":"open"}`)
	wantRefused(t, "window with a lapsed pass", status, body, entitlement.FeatureWiFiLock)
	if box.WiFiPolicy(lockAP1) != "Allow" {
		t.Fatal("a refused window reached the box")
	}
	if status, body := call(t, c, "POST", "/api/wifi/lock/open", ""); status != http.StatusOK {
		t.Fatalf("open with a lapsed pass = %d %s", status, body)
	}
	if box.WiFiPolicy(lockAP1) != "Disabled" {
		t.Fatal("not opened")
	}
	status, body = call(t, c, "POST", "/api/wifi/lock/close", "")
	wantRefused(t, "close with a lapsed pass", status, body, entitlement.FeatureWiFiLock)
	if !strings.Contains(body, `"required": "max"`) || box.WiFiPolicy(lockAP1) != "Disabled" {
		t.Fatalf("close with a lapsed pass = %d %s, policy %s", status, body, box.WiFiPolicy(lockAP1))
	}
	if status, body := call(t, c, "POST", "/api/wifi/lock/close", `{"preview":true}`); status != http.StatusOK {
		t.Fatalf("preview with a lapsed pass = %d %s", status, body)
	}

	// Basic does not close either; no answer carries a price or a link.
	c.SetEntitlement(signToken(t, testPriv, entitlement.TierBasic, mockSerial, clk.now()))
	status, body = call(t, c, "POST", "/api/wifi/lock/close", "")
	wantRefused(t, "close with Basic", status, body, entitlement.FeatureWiFiLock)
	for _, w := range []string{"http", "€", "FCFA", "prix", "price"} {
		if strings.Contains(body, w) {
			t.Fatalf("the refusal mentions %q: %s", w, body)
		}
	}
	noHazard(t, box)
}

// While the Wi-Fi is closed, Autoriser (PUT /api/approvals/{mac}) admits the
// device on the box, free, and « À vérifier » (DELETE) refuses it again, on
// Basic; never this phone. While open, neither writes on the box.
func TestWiFiLock_ApprovalsWhileClosed(t *testing.T) {
	c, clk, box := lockCore(t)

	writes := box.WiFiWrites()
	if status, body := call(t, c, "DELETE", "/api/approvals/"+lockLaptop, ""); status != http.StatusOK {
		t.Fatalf("withdraw while open = %d %s", status, body)
	}
	if status, body := call(t, c, "PUT", "/api/approvals/"+lockLaptop, ""); status != http.StatusOK {
		t.Fatalf("approve while open = %d %s", status, body)
	}
	if box.WiFiWrites() != writes {
		t.Fatal("an approval wrote to the box while the Wi-Fi is open")
	}

	closeWiFi(t, c)
	box.Join(mockrouter.Station{MAC: lockStranger, Hostname: "Tablette", IP: "192.168.1.31", AP: lockAP1})
	if admitted(box, lockStranger) {
		t.Fatal("a newcomer joined the closed Wi-Fi")
	}
	// The pass lapsed: authorising is free.
	clk.set(gateT0.Add(10*gateDay + entitlement.OfflineGrace))
	if status, body := call(t, c, "PUT", "/api/approvals/"+lockStranger, ""); status != http.StatusOK {
		t.Fatalf("authorise while closed = %d %s", status, body)
	}
	if !admitted(box, lockStranger) {
		t.Fatal("an authorised device is refused by the closed Wi-Fi")
	}
	status, body := call(t, c, "DELETE", "/api/approvals/"+lockStranger, "")
	wantRefused(t, "« À vérifier » with a lapsed pass", status, body, entitlement.FeatureCutWiFiAccess)
	if !admitted(box, lockStranger) || !approvalsOf(t, c)[lockStranger] {
		t.Fatal("a refused « À vérifier » changed something")
	}

	c.SetEntitlement(signToken(t, testPriv, entitlement.TierBasic, mockSerial, clk.now()))
	if status, body := call(t, c, "DELETE", "/api/approvals/"+lockStranger, ""); status != http.StatusOK {
		t.Fatalf("« À vérifier » with Basic = %d %s", status, body)
	}
	if admitted(box, lockStranger) || approvalsOf(t, c)[lockStranger] {
		t.Fatal("« À vérifier » did not refuse the device")
	}
	status, body = call(t, c, "DELETE", "/api/approvals/"+lockPhone, "")
	wantCoreLockout(t, "« À vérifier » on this phone while closed", status, body, domain.SelfLockoutDevice)
	if !admitted(box, lockPhone) {
		t.Fatal("this phone was refused")
	}
	noHazard(t, box)
}

func approvalsOf(t *testing.T, c *Core) map[string]bool {
	t.Helper()
	status, body := call(t, c, "GET", "/api/approvals", "")
	if status != http.StatusOK {
		t.Fatalf("GET /api/approvals = %d %s", status, body)
	}
	var out struct {
		MACs []string `json:"macs"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	set := map[string]bool{}
	for _, m := range out.MACs {
		set[m] = true
	}
	return set
}

// « Le déconnecter aussi du Wi-Fi » on a block (D5): the firewall rule and
// the Wi-Fi refusal, on Basic; the flag is in the status; Débloquer removes
// both, free. While closed, a plain block takes the device off the list.
func TestWiFiLock_BlockWithTheWiFiFlag(t *testing.T) {
	c, _, box := lockCore(t)
	c.SetEntitlement("")
	status, body := call(t, c, "POST", "/api/devices/"+lockLaptop+"/block", `{"cut_wifi":true}`)
	wantRefused(t, "block and cut without a pass", status, body, entitlement.FeatureBlock)

	c.SetEntitlement(signToken(t, testPriv, entitlement.TierBasic, mockSerial, gateT0))
	if status, body := call(t, c, "POST", "/api/devices/"+lockLaptop+"/block", `{"cut_wifi":true}`); status != http.StatusOK {
		t.Fatalf("block and cut = %d %s", status, body)
	}
	if !box.Blocked(lockLaptop) || !box.WiFiBlocked(lockLaptop) {
		t.Fatalf("blocked %v, refused at the Wi-Fi %v; want both", box.Blocked(lockLaptop), box.WiFiBlocked(lockLaptop))
	}
	if st := lockState(t, c); !slices.Equal(st.Cut, []string{lockLaptop}) {
		t.Fatalf("cut = %v", st.Cut)
	}
	if status, body := call(t, c, "POST", "/api/devices/"+lockLaptop+"/block", `{"cut_wifi":`); status != http.StatusBadRequest {
		t.Fatalf("a broken body = %d %s, want 400", status, body)
	}

	c.SetEntitlement("")
	if status, body := call(t, c, "POST", "/api/devices/"+lockLaptop+"/unblock", ""); status != http.StatusOK {
		t.Fatalf("unblock = %d %s", status, body)
	}
	if box.Blocked(lockLaptop) || box.WiFiBlocked(lockLaptop) || len(lockState(t, c).Cut) != 0 {
		t.Fatal("unblock left a rule or the flag")
	}

	// Closed: a plain block takes the device off the allow list too.
	c.SetEntitlement(signToken(t, testPriv, entitlement.TierMax, mockSerial, gateT0))
	if status, body := call(t, c, "PUT", "/api/approvals/"+lockLaptop, ""); status != http.StatusOK {
		t.Fatalf("approve = %d %s", status, body)
	}
	closeWiFi(t, c)
	if !admitted(box, lockLaptop) {
		t.Fatal("the approved laptop is refused")
	}
	if status, body := call(t, c, "POST", "/api/devices/"+lockLaptop+"/block", ""); status != http.StatusOK {
		t.Fatalf("block while closed = %d %s", status, body)
	}
	if !box.Blocked(lockLaptop) || admitted(box, lockLaptop) {
		t.Fatal("a device blocked while closed is still admitted")
	}
	noHazard(t, box)
}

// The add window's deadline is in the database: the next device listing
// (the watch service's round, the app back in the foreground) closes the
// Wi-Fi again, even from a core opened after the app was killed.
func TestWiFiLock_DeadlineClosedAgainByTheDeviceList(t *testing.T) {
	box := mockrouter.NewF6600P("admin", "s3cret")
	srv := httptest.NewServer(box.Handler())
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	clk := &fakeClock{t: gateT0}
	open := func() *Core {
		c, err := Open(dir, "test-vault-key", nil)
		if err != nil {
			t.Fatal(err)
		}
		c.gate.now = clk.now
		c.clock.set(clk.now)
		c.self.set(func(string) domain.IP { return domain.MustIP(testPhoneIP) })
		if err := c.SetPublicKeys(base64.StdEncoding.EncodeToString(testPub)); err != nil {
			t.Fatal(err)
		}
		if err := c.Configure(srv.URL, "", ""); err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := open()
	if status, body := call(t, c, "POST", "/api/login", `{"username":"admin","password":"s3cret"}`); status != http.StatusOK {
		t.Fatalf("login = %d %s", status, body)
	}
	box.Join(mockrouter.Station{MAC: lockPhone, IP: testPhoneIP, AP: lockAP1})
	c.SetEntitlement(signToken(t, testPriv, entitlement.TierMax, mockSerial, gateT0))
	closeWiFi(t, c)
	if status, body := call(t, c, "POST", "/api/wifi/lock/window", `{"action":"open"}`); status != http.StatusOK {
		t.Fatalf("window = %d %s", status, body)
	}
	clk.set(gateT0.Add(4 * time.Minute))
	devices(t, c)
	if box.WiFiPolicy(lockAP1) != "Disabled" {
		t.Fatal("closed before the deadline")
	}
	_ = c.Close() // the app is killed

	clk.set(gateT0.Add(6 * time.Minute))
	c = open()
	t.Cleanup(func() { _ = c.Close() })
	devices(t, c) // no pass needed: it ends what the household asked
	if box.WiFiPolicy(lockAP1) != "Allow" || box.WiFiPolicy(lockAP5) != "Allow" {
		t.Fatalf("modes after the deadline = %s %s, want closed again", box.WiFiPolicy(lockAP1), box.WiFiPolicy(lockAP5))
	}
	if st := lockState(t, c); st.State != "closed" || st.Window.Until != "" {
		t.Fatalf("status = %+v", st)
	}
	noHazard(t, box)
}

// The relays the household marked (PUT /api/relay-devices) are always in the
// allow list; with neither this phone nor a relay known, a close answers 409
// self_lockout allow_list and writes nothing, and the preview says no relay
// is known.
func TestWiFiLock_RelayDevices(t *testing.T) {
	c, _, box := lockCore(t)
	c.self.set(func(string) domain.IP { return domain.IP{} }) // this phone not found on the box

	status, body := call(t, c, "POST", "/api/wifi/lock/close", "")
	wantCoreLockout(t, "close with neither this phone nor a relay", status, body, domain.SelfLockoutAllowList)
	if box.WiFiWrites() != 0 {
		t.Fatal("a refused close wrote to the box")
	}

	if status, body := call(t, c, "PUT", "/api/relay-devices", `{"macs":["`+strings.ToUpper(lockRelay)+`","nope"]}`); status != http.StatusOK || !strings.Contains(body, lockRelay) {
		t.Fatalf("PUT /api/relay-devices = %d %s", status, body)
	}
	if status, body := call(t, c, "GET", "/api/relay-devices", ""); status != http.StatusOK || !strings.Contains(body, `"macs":["`+lockRelay+`"]`) {
		t.Fatalf("GET /api/relay-devices = %d %s", status, body)
	}
	status, body = call(t, c, "POST", "/api/wifi/lock/close", `{"preview":true}`)
	if p := decodePreview(t, body); status != http.StatusOK || !slices.Equal(p.Relays, []string{lockRelay}) || !slices.Contains(p.Always, lockRelay) || p.Self != "" {
		t.Fatalf("preview = %d %+v", status, p)
	}
	closeWiFi(t, c)
	if !admitted(box, lockRelay) {
		t.Fatal("the relay is refused by the closed Wi-Fi")
	}
	noHazard(t, box)
}

// The demo closes its Wi-Fi with no box: this phone and the owner's other
// phone stay in, a visitor is refused, the add window lets it in, and
// opening needs nothing.
func TestWiFiLock_Demo(t *testing.T) {
	c := approvalDemo(t)
	if st := lockState(t, c); st.State != "open" {
		t.Fatalf("demo status = %+v", st)
	}
	p := closeWiFi(t, c)
	if !slices.Contains(p.Always, DemoPhoneMAC) || !slices.Contains(p.Always, DemoOwnerPhoneMAC) {
		t.Fatalf("demo close = %+v", p)
	}
	c.mu.RLock()
	box := c.demoBox
	c.mu.RUnlock()
	if st := lockState(t, c); st.State != "closed" {
		t.Fatalf("demo status after close = %+v", st)
	}
	if admitted(box, DemoVisitorMAC) || !admitted(box, DemoPhoneMAC) {
		t.Fatal("the demo's closed Wi-Fi admits the wrong devices")
	}
	if status, body := call(t, c, "POST", "/api/wifi/lock/window", `{"action":"open"}`); status != http.StatusOK {
		t.Fatalf("demo window = %d %s", status, body)
	}
	if st := lockState(t, c); st.State != "window" || st.Window.MinutesLeft != 5 {
		t.Fatalf("demo status in the window = %+v", st)
	}
	if !admitted(box, DemoVisitorMAC) {
		t.Fatal("the demo's window refuses the visitor")
	}
	for _, a := range []string{"extend", "close"} {
		if status, body := call(t, c, "POST", "/api/wifi/lock/window", `{"action":"`+a+`"}`); status != http.StatusOK {
			t.Fatalf("demo window %s = %d %s", a, status, body)
		}
	}
	if status, body := call(t, c, "POST", "/api/wifi/lock/open", ""); status != http.StatusOK {
		t.Fatalf("demo open = %d %s", status, body)
	}
	if st := lockState(t, c); st.State != "open" {
		t.Fatalf("demo status after open = %+v", st)
	}
	noHazard(t, box)
}
