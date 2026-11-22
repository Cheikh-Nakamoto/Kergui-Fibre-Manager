package mobilecore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
)

type usageRow struct {
	MAC      string `json:"mac"`
	Period   string `json:"period"`
	Bytes    uint64 `json:"bytes"`
	Sent     uint64 `json:"sent"`
	Received uint64 `json:"received"`
}

// meteredCore is a core pointed at the F6600P mock (which has per-station
// byte counters), logged in, on a fake clock.
func meteredCore(t *testing.T, at time.Time) (*Core, *fakeClock, *mockrouter.F6600P) {
	t.Helper()
	box := mockrouter.NewF6600P("admin", "s3cret")
	srv := httptest.NewServer(box.Handler())
	t.Cleanup(srv.Close)
	c := newCore(t, nil)
	clk := &fakeClock{t: at}
	c.clock.set(clk.now)
	if err := c.Configure(srv.URL, "", ""); err != nil {
		t.Fatal(err)
	}
	if status, body := call(t, c, "POST", "/api/login", `{"username":"admin","password":"s3cret"}`); status != http.StatusOK {
		t.Fatalf("login = %d %s", status, body)
	}
	return c, clk, box
}

func sample(t *testing.T, c *Core) {
	t.Helper()
	if status, body := call(t, c, "POST", "/api/usage/sample", ""); status != http.StatusOK {
		t.Fatalf("POST /api/usage/sample = %d %s", status, body)
	}
}

func usageOf(t *testing.T, c *Core, query string) map[string]usageRow {
	t.Helper()
	status, body := call(t, c, "GET", "/api/usage"+query, "")
	if status != http.StatusOK {
		t.Fatalf("GET /api/usage%s = %d %s", query, status, body)
	}
	var rows []usageRow
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("decode usage: %v\n%s", err, body)
	}
	out := map[string]usageRow{}
	for _, r := range rows {
		out[r.MAC] = r
	}
	return out
}

// TestUsageAcrossAReconnect is what the Conso tab relies on: the phone
// samples the box's counters, the first sample is a baseline, and a device
// that reconnects (its counters start again from zero) keeps what it had
// consumed before, split into sent and received.
func TestUsageAcrossAReconnect(t *testing.T) {
	c, clk, box := meteredCore(t, time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC))
	const mac = "02:00:00:00:00:aa"
	box.Join(mockrouter.Station{MAC: mac, Hostname: "Tablette", IP: "192.168.1.50", LinkSecs: 600, Rx: 1_000, Tx: 10_000})

	sample(t, c) // baseline: nothing counted yet
	if u := usageOf(t, c, ""); len(u) != 0 {
		t.Fatalf("usage after the baseline = %+v, want nothing", u)
	}

	clk.set(clk.now().Add(30 * time.Second))
	box.Join(mockrouter.Station{MAC: mac, Hostname: "Tablette", IP: "192.168.1.50", LinkSecs: 630, Rx: 3_000, Tx: 50_000})
	sample(t, c) // +2 000 sent, +40 000 received

	// It leaves and comes back: a new association, counters from zero.
	clk.set(clk.now().Add(30 * time.Second))
	box.Join(mockrouter.Station{MAC: mac, Hostname: "Tablette", IP: "192.168.1.50", LinkSecs: 20, Rx: 500, Tx: 7_000})
	sample(t, c) // the whole new counter is new traffic

	got := usageOf(t, c, "")[mac]
	want := usageRow{MAC: mac, Period: "2026-11", Bytes: 49_500, Sent: 2_500, Received: 47_000}
	if got != want {
		t.Fatalf("usage of %s = %+v, want %+v", mac, got, want)
	}

	// Another month starts from zero; November keeps its figures.
	clk.set(time.Date(2026, 12, 1, 0, 0, 30, 0, time.UTC))
	box.Join(mockrouter.Station{MAC: mac, Hostname: "Tablette", IP: "192.168.1.50", LinkSecs: 60, Rx: 600, Tx: 7_100})
	sample(t, c)
	if dec := usageOf(t, c, "")[mac]; dec.Period != "2026-12" || dec.Bytes != 200 {
		t.Fatalf("December usage = %+v, want 200 bytes in 2026-12", dec)
	}
	// Looking back at November is the Max monthly filter.
	grant(t, c, entitlement.TierMax, time.Date(2026, 12, 1, 0, 0, 30, 0, time.UTC))
	if nov := usageOf(t, c, "?period=2026-11")[mac]; nov.Bytes != 49_500 {
		t.Fatalf("November usage = %+v, want it kept", nov)
	}
}

// TestUsageOnABoxWithoutCounters: the F660 has no byte counters; the
// routes answer 501, which the app shows as "not measurable on this box".
func TestUsageOnABoxWithoutCounters(t *testing.T) {
	c := newCore(t, nil)
	srv := httptest.NewServer(mockrouter.New("admin", "admin").Handler())
	t.Cleanup(srv.Close)
	if err := c.Configure(srv.URL, "", ""); err != nil {
		t.Fatal(err)
	}
	if status, body := call(t, c, "POST", "/api/login", `{"username":"admin","password":"admin"}`); status != http.StatusOK {
		t.Fatalf("login = %d %s", status, body)
	}
	if status, body := call(t, c, "POST", "/api/usage/sample", ""); status != http.StatusNotImplemented {
		t.Fatalf("sample on the F660 = %d %s, want 501", status, body)
	}
	if status, body := call(t, c, "GET", "/api/usage", ""); status != http.StatusOK || body == "" {
		t.Fatalf("usage on the F660 = %d %s, want 200 (empty list)", status, body)
	}
}

// TestDemoShowsEveryFeature: the demo is an F6600P whose devices use the
// Wi-Fi, so a store reviewer sees the serial number, the Wi-Fi access
// control and a consumption that grows.
func TestDemoShowsEveryFeature(t *testing.T) {
	c := newCore(t, nil)
	if err := c.StartDemo(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.StopDemo)
	if status, body := call(t, c, "POST", "/api/login", `{"username":"`+DemoUsername+`","password":"`+DemoPassword+`"}`); status != http.StatusOK {
		t.Fatalf("login = %d %s", status, body)
	}
	if status, body := call(t, c, "GET", "/api/wifi/blocked", ""); status != http.StatusOK {
		t.Fatalf("Wi-Fi deny list on the demo = %d %s, want 200", status, body)
	}
	if status, body := call(t, c, "POST", "/api/usage/sample", ""); status != http.StatusOK {
		t.Fatalf("first sample = %d %s", status, body)
	}
	time.Sleep(demoTrafficEvery + 500*time.Millisecond)
	if status, body := call(t, c, "POST", "/api/usage/sample", ""); status != http.StatusOK {
		t.Fatalf("second sample = %d %s", status, body)
	}
	var total uint64
	for _, u := range usageOf(t, c, "") {
		total += u.Bytes
	}
	if total == 0 {
		t.Fatal("the demo devices used no Wi-Fi between two samples")
	}
}
