// This test is the point of the F6600P mock: the real adapter must pass
// against it, end to end, without hardware. It lives in an external test
// package because the adapter cannot be imported from mockrouter itself
// (internal/adapter/router/ztef6600p's own tests use this package).
package mockrouter_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/discovery"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/catalog"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/ztef6600p"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/clock"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

const (
	mockUser = "admin"
	mockPass = "s3cret"
	// Phone-Salon, blocked in the captured MAC-filter fixture.
	blockedMAC = "ac:bb:cc:00:00:11"
	// Laptop-Bureau, present and not blocked.
	freeMAC = "ac:bb:cc:00:00:22"
)

func newMock(t *testing.T) (*mockrouter.F6600P, string) {
	t.Helper()
	m := mockrouter.NewF6600P(mockUser, mockPass)
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)
	return m, srv.URL
}

func login(t *testing.T, url string) *ztef6600p.Gateway {
	t.Helper()
	gw, err := ztef6600p.New(url, port.RouterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Login(context.Background(), domain.Credentials{Username: mockUser, Password: mockPass}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	return gw
}

// loggedInClient performs the login handshake by hand and keeps the session
// cookie, so a test can send a request the adapter would never send.
func loggedInClient(t *testing.T, base string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Jar: jar}
	get(t, c, base+"/?_type=loginData&_tag=login_entry")
	// The box hashes the password with the login token it just served.
	sum := sha256.Sum256([]byte(mockPass + "12345678"))
	form := url.Values{
		"action":        {"login"},
		"Username":      {mockUser},
		"Password":      {hex.EncodeToString(sum[:])},
		"_sessionTOKEN": {"mock-session-token"},
	}
	resp, err := c.PostForm(base+"/?_type=loginData&_tag=login_entry", form)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "loginErrMsg") {
		t.Fatalf("hand-made login failed: %s", body)
	}
	return c
}

func get(t *testing.T, c *http.Client, target string) string {
	t.Helper()
	resp, err := c.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func postRaw(t *testing.T, c *http.Client, target, body, check string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if check != "" {
		req.Header.Set("Check", check)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return string(out)
}

func mustMAC(t *testing.T, s string) domain.MAC {
	t.Helper()
	m, err := domain.ParseMAC(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestF6600P_Discovery guards the trap that makes this mock subtle: "f660" is
// a prefix of "f6600p" and the F660 adapter is registered first, so a page
// naming the model in plain text would be detected as a zte_f660.
func TestF6600P_Discovery(t *testing.T) {
	_, url := newMock(t)
	d := discovery.New(catalog.New(), clock.Real{})
	info, err := d.Discover(context.Background(), url, port.RouterOptions{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if info.AdapterID != ztef6600p.ID {
		t.Errorf("AdapterID = %q, want %q", info.AdapterID, ztef6600p.ID)
	}
}

func TestF6600P_LoginAndReads(t *testing.T) {
	m, url := newMock(t)
	gw := login(t, url)
	ctx := context.Background()

	info, err := gw.RouterInfo(ctx)
	if err != nil {
		t.Fatalf("RouterInfo: %v", err)
	}
	if info.Model != "F6600P" || info.Vendor != "ZTE" {
		t.Errorf("RouterInfo = %+v, want ZTE F6600P", info)
	}

	devs, err := gw.Devices(ctx)
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if len(devs) != 3 {
		t.Fatalf("got %d devices, want the 3 of the captured fixture", len(devs))
	}
	for _, d := range devs {
		switch d.MAC.String() {
		case blockedMAC:
			if !d.Blocked {
				t.Errorf("%s should be blocked: it is in the captured MAC filter", blockedMAC)
			}
		case freeMAC:
			if d.Blocked {
				t.Errorf("%s should not be blocked", freeMAC)
			}
		}
	}
	if m.Logins() != 1 {
		t.Errorf("Logins() = %d, want 1 (the reads must reuse the session)", m.Logins())
	}
}

func TestF6600P_LoginRejectsWrongPassword(t *testing.T) {
	_, url := newMock(t)
	gw, err := ztef6600p.New(url, port.RouterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	err = gw.Login(context.Background(), domain.Credentials{Username: mockUser, Password: "wrong"})
	if err == nil {
		t.Fatal("login with a wrong password must fail")
	}
}

func TestF6600P_BlockAndUnblock(t *testing.T) {
	m, url := newMock(t)
	gw := login(t, url)
	ctx := context.Background()
	mac := mustMAC(t, freeMAC)

	if err := gw.Block(ctx, mac); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if !m.Blocked(freeMAC) {
		t.Fatal("the mock does not show the device as blocked")
	}
	// The adapter reads the write back; a second block must be a no-op.
	if err := gw.Block(ctx, mac); err != nil {
		t.Fatalf("second Block: %v", err)
	}

	if err := gw.Unblock(ctx, mac); err != nil {
		t.Fatalf("Unblock: %v", err)
	}
	if m.Blocked(freeMAC) {
		t.Fatal("the device is still blocked after Unblock")
	}
	if !m.Blocked(blockedMAC) {
		t.Errorf("unblocking %s also dropped the pre-existing rule on %s", freeMAC, blockedMAC)
	}
}

// TestF6600P_WriteNeedsIntegrityHeader posts by hand, the way a caller that
// skipped the web UI's signing would. A mock that accepted these would make
// every relay test pass against a box that checks nothing.
func TestF6600P_WriteNeedsIntegrityHeader(t *testing.T) {
	m, url := newMock(t)
	c := loggedInClient(t, url)

	const target = "/?_type=menuData&_tag=firewall_macfilterv3_lua.lua"
	body := "IF_ACTION=Apply&_InstID=-1&SrcMacAddr=AA:BB:CC:DD:EE:FF&Protocol=ALL&Name=x&Type=Route&_sessionTOKEN=" + mockrouter.PageToken

	for _, tc := range []struct {
		name, body, check, want string
	}{
		{"no Check header", body, "", "integrity check failed"},
		{"garbage Check header", body, "bm90LWFuLXJzYS1ibG9i", "integrity check failed"},
		{"stale page token", strings.Replace(body, mockrouter.PageToken, "stale-token", 1), "", "expiré"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := postRaw(t, c, url+target, tc.body, tc.check)
			if !strings.Contains(got, tc.want) {
				t.Errorf("response = %q, want it to contain %q", got, tc.want)
			}
		})
	}
	if m.Blocked("aa:bb:cc:dd:ee:ff") {
		t.Fatal("an unsigned write changed the mock's state")
	}
}

func TestF6600P_MACFilterSwitch(t *testing.T) {
	m, url := newMock(t)
	gw := login(t, url)
	ctx := context.Background()

	// The captured fixture has the filter on.
	if on, err := gw.MACFilterEnabled(ctx); err != nil || !on {
		t.Fatalf("MACFilterEnabled = %v, %v; want true, nil", on, err)
	}
	if err := gw.SetMACFilterEnabled(ctx, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if m.MACFilterEnabled() {
		t.Error("the mock still reports the filter as on")
	}
	if err := gw.SetMACFilterEnabled(ctx, true); err != nil || !m.MACFilterEnabled() {
		t.Fatalf("enable: err=%v on=%v", err, m.MACFilterEnabled())
	}
}

func TestF6600P_WiFiBlockAndUnblock(t *testing.T) {
	m, url := newMock(t)
	gw := login(t, url)
	ctx := context.Background()
	mac := mustMAC(t, freeMAC)

	if err := gw.WiFiBlock(ctx, mac); err != nil {
		t.Fatalf("WiFiBlock: %v", err)
	}
	if !m.WiFiBlocked(freeMAC) {
		t.Fatal("the mock does not show the device as Wi-Fi blocked")
	}
	blocked, err := gw.WiFiBlockedMACs(ctx)
	if err != nil {
		t.Fatalf("WiFiBlockedMACs: %v", err)
	}
	if len(blocked) == 0 {
		t.Error("WiFiBlockedMACs is empty right after a successful block")
	}
	if err := gw.WiFiUnblock(ctx, mac); err != nil {
		t.Fatalf("WiFiUnblock: %v", err)
	}
	if m.WiFiBlocked(freeMAC) {
		t.Fatal("the device is still Wi-Fi blocked after WiFiUnblock")
	}
}

func TestF6600P_SSIDs(t *testing.T) {
	_, url := newMock(t)
	gw := login(t, url)
	ssids, err := gw.WiFiSSIDs(context.Background())
	if err != nil {
		t.Fatalf("WiFiSSIDs: %v", err)
	}
	if len(ssids) != 2 {
		t.Fatalf("got %d SSIDs, want 2", len(ssids))
	}
	if ssids[0].Name != "MonWiFi-2G" || ssids[0].Band != "2.4GHz" {
		t.Errorf("first SSID = %+v, want MonWiFi-2G on 2.4GHz", ssids[0])
	}
	if ssids[1].Band != "5GHz" {
		t.Errorf("second SSID band = %q, want 5GHz", ssids[1].Band)
	}
}

// TestF6600P_OneSessionAtATime is the behaviour the relay lease exists for
// (RFC-005 C2): the box keeps a single admin session, and a second login
// evicts the first.
func TestF6600P_OneSessionAtATime(t *testing.T) {
	m, url := newMock(t)
	ctx := context.Background()

	first := login(t, url)
	if _, err := first.Devices(ctx); err != nil {
		t.Fatalf("first relay Devices: %v", err)
	}
	firstSession := m.ActiveSession()

	second := login(t, url)
	if m.ActiveSession() == firstSession {
		t.Fatal("the second login did not take over the session")
	}
	if m.Logins() != 2 {
		t.Fatalf("Logins() = %d, want 2", m.Logins())
	}
	if _, err := second.Devices(ctx); err != nil {
		t.Fatalf("second relay Devices: %v", err)
	}

	// The evicted gateway is told its session expired; it logs in again,
	// which in turn evicts the second one. That is the box's behaviour, and
	// the reason only the lease holder may talk to it.
	if _, err := first.Devices(ctx); err != nil {
		t.Fatalf("evicted relay Devices: %v", err)
	}
	if m.Logins() != 3 {
		t.Errorf("Logins() = %d, want 3 after the evicted relay logged in again", m.Logins())
	}
}

// TestF6600P_JoinAndLeave is what H2's inventory diff is built on: a device
// arrives, the three pages agree that it is there, it leaves, and only its
// DHCP lease remembers it.
func TestF6600P_JoinAndLeave(t *testing.T) {
	m, url := newMock(t)
	gw := login(t, url)
	ctx := context.Background()
	const newMAC = "aa:11:22:33:44:55"

	before, err := gw.Observe(ctx)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if findObs(before, newMAC) != nil {
		t.Fatalf("%s is on the box before it joined", newMAC)
	}

	m.Join(mockrouter.Station{
		MAC: newMAC, Hostname: "TECNO-POP-10", IP: "192.168.1.77",
		AP: "DEV.WIFI.AP5", Rx: 1024, Tx: 2048, LinkSecs: 45,
	})

	after, err := gw.Observe(ctx)
	if err != nil {
		t.Fatalf("Observe after join: %v", err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("got %d observations after the join, want %d", len(after), len(before)+1)
	}
	o := findObs(after, newMAC)
	if o == nil {
		t.Fatalf("%s did not appear", newMAC)
	}
	if !o.Associated || o.Hostname != "TECNO-POP-10" || o.IPv4.String() != "192.168.1.77" {
		t.Errorf("observation = %+v, want the joined station associated with its name and address", o)
	}
	if o.SSID != "MonWiFi-5G" || o.Band != "5G" {
		t.Errorf("SSID/band = %q/%q, want MonWiFi-5G on 5G", o.SSID, o.Band)
	}
	if o.Lease == nil {
		t.Error("the DHCP table has no lease for the joined station")
	}
	if !o.Radio.Known() {
		t.Error("the topology reported no radio capabilities")
	}

	// The counters travel through the same Wi-Fi client table as the devices.
	counters, err := gw.TrafficCounters(ctx)
	if err != nil {
		t.Fatalf("TrafficCounters: %v", err)
	}
	if c := counters[domain.MustMAC(newMAC)]; c.Rx != 1024 || c.Tx != 2048 || c.LinkSecs != 45 {
		t.Errorf("counters = %+v, want rx 1024 tx 2048 over 45 s", c)
	}

	// Leaving the Wi-Fi keeps the lease: the box still lists it, unassociated.
	m.Leave(newMAC)
	left, err := gw.Observe(ctx)
	if err != nil {
		t.Fatalf("Observe after leave: %v", err)
	}
	o = findObs(left, newMAC)
	if o == nil {
		t.Fatalf("%s vanished from the lease table when it left the Wi-Fi", newMAC)
	}
	if o.Associated {
		t.Error("a station that left is still reported as associated")
	}

	m.Forget(newMAC)
	gone, err := gw.Observe(ctx)
	if err != nil {
		t.Fatalf("Observe after forget: %v", err)
	}
	if findObs(gone, newMAC) != nil {
		t.Errorf("%s is still on the box after Forget", newMAC)
	}
}

// TestF6600P_SeededStationsMatchTheFixture: the station table starts out as
// the captured page did, so the mock still begins where the hardware was.
func TestF6600P_SeededStationsMatchTheFixture(t *testing.T) {
	m, url := newMock(t)
	gw := login(t, url)

	stations := m.Stations()
	if len(stations) != 3 {
		t.Fatalf("got %d seeded stations, want the 3 of the captured fixture", len(stations))
	}
	obs, err := gw.Observe(context.Background())
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(obs) != 3 {
		t.Fatalf("got %d observations, want 3", len(obs))
	}
	o := findObs(obs, blockedMAC)
	if o == nil || o.Hostname != "Phone-Salon" {
		t.Errorf("%s = %+v, want the captured Phone-Salon", blockedMAC, o)
	}
}

func findObs(obs []domain.Observation, mac string) *domain.Observation {
	want := domain.MustMAC(mac)
	for i := range obs {
		if obs[i].MAC.Equal(want) {
			return &obs[i]
		}
	}
	return nil
}
