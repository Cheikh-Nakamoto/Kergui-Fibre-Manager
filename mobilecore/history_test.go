package mobilecore

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
)

// grant makes the core hold a token of tier for the mock box, checked on the
// gate clock at.
func grant(t *testing.T, c *Core, tier entitlement.Tier, at time.Time) {
	t.Helper()
	c.gate.mu.Lock()
	c.gate.now = func() time.Time { return at }
	c.gate.mu.Unlock()
	if err := c.SetPublicKeys(base64.StdEncoding.EncodeToString(testPub)); err != nil {
		t.Fatal(err)
	}
	c.SetEntitlement(signToken(t, testPriv, tier, mockSerial, at))
}

func historyOf(t *testing.T, c *Core, mac, query string) (int, []usageRow, string) {
	t.Helper()
	status, body := call(t, c, "GET", "/api/usage/devices/"+mac+query, "")
	var rows []usageRow
	if status == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &rows); err != nil {
			t.Fatalf("decode history: %v\n%s", err, body)
		}
	}
	return status, rows, body
}

// TestUsageHistoryNeedsMax: the current month is free; another month and a
// device's history are Max, refused by the core itself without it.
func TestUsageHistoryNeedsMax(t *testing.T) {
	now := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	c, clk, box := meteredCore(t, time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC))
	const mac = "02:00:00:00:00:aa"
	box.Join(mockrouter.Station{MAC: mac, Hostname: "Tablette", IP: "192.168.1.50", LinkSecs: 600, Rx: 1_000, Tx: 10_000})
	sample(t, c) // baseline
	box.Join(mockrouter.Station{MAC: mac, Hostname: "Tablette", IP: "192.168.1.50", LinkSecs: 660, Rx: 2_000, Tx: 20_000})
	sample(t, c) // October: +11 000
	clk.set(now)
	box.Join(mockrouter.Station{MAC: mac, Hostname: "Tablette", IP: "192.168.1.50", LinkSecs: 720, Rx: 2_500, Tx: 25_000})
	sample(t, c) // November: +5 500

	for _, q := range []string{"", "?period=2026-11"} {
		if u := usageOf(t, c, q)[mac]; u.Bytes != 5_500 {
			t.Fatalf("current month (%q) = %+v, want 5 500 bytes, free", q, u)
		}
	}
	for _, tier := range []entitlement.Tier{"", entitlement.TierBasic} {
		if tier != "" {
			grant(t, c, tier, now)
		}
		status, body := call(t, c, "GET", "/api/usage?period=2026-10", "")
		if !refused(t, status, body) {
			t.Fatalf("October with tier %q = %d %s, want not_entitled", tier, status, body)
		}
		status, _, body = historyOf(t, c, mac, "?months=6")
		if !refused(t, status, body) {
			t.Fatalf("device history with tier %q = %d %s, want not_entitled", tier, status, body)
		}
	}

	grant(t, c, entitlement.TierMax, now)
	if oct := usageOf(t, c, "?period=2026-10")[mac]; oct.Bytes != 11_000 {
		t.Fatalf("October with Max = %+v, want 11 000 bytes", oct)
	}
	status, rows, body := historyOf(t, c, mac, "?months=6")
	if status != http.StatusOK || len(rows) != 6 {
		t.Fatalf("history with Max = %d %s, want 6 months", status, body)
	}
	if rows[0].Period != "2026-06" || rows[5].Period != "2026-11" {
		t.Fatalf("history months = %s..%s, want 2026-06..2026-11", rows[0].Period, rows[5].Period)
	}
	if rows[4].Bytes != 11_000 || rows[4].Sent != 1_000 || rows[5].Bytes != 5_500 || rows[0].Bytes != 0 {
		t.Fatalf("history = %+v, want October 11 000 (1 000 sent), November 5 500, June 0", rows)
	}
	if status, _, _ := historyOf(t, c, "not-a-mac", ""); status != http.StatusBadRequest {
		t.Fatalf("history of a bad MAC = %d, want 400", status)
	}
}

// TestDemoHasHistory: the demo shows six months of past consumption, so the
// month selector and the device curve have something to show, with no pass.
func TestDemoHasHistory(t *testing.T) {
	c := newCore(t, nil)
	if err := c.StartDemo(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.StopDemo)
	if status, body := call(t, c, "POST", "/api/login", `{"username":"`+DemoUsername+`","password":"`+DemoPassword+`"}`); status != http.StatusOK {
		t.Fatalf("login = %d %s", status, body)
	}
	// Laptop-Bureau (ac:bb:cc:00:00:22), as the app sends it.
	status, rows, body := historyOf(t, c, "AC%3ABB%3ACC%3A00%3A00%3A22", "?months=6")
	if status != http.StatusOK || len(rows) != 6 {
		t.Fatalf("demo history = %d %s, want 6 months", status, body)
	}
	for _, r := range rows[:5] {
		if r.Bytes == 0 || r.Sent == 0 || r.Received == 0 {
			t.Fatalf("demo month %s of the laptop = %+v, want some consumption", r.Period, r)
		}
	}
	last := time.Now().AddDate(0, -1, -time.Now().Day()+1).Format("2006-01")
	var total uint64
	for _, u := range usageOf(t, c, "?period="+last) {
		total += u.Bytes
	}
	if total == 0 {
		t.Fatalf("the demo's last month (%s) is empty", last)
	}

	// Starting the demo again on the same database does not add it twice.
	before := usageOf(t, c, "?period="+last)
	if err := c.seedDemoHistory(); err != nil {
		t.Fatal(err)
	}
	after := usageOf(t, c, "?period="+last)
	for mac, u := range before {
		if after[mac] != u {
			t.Fatalf("seeding again changed %s: %+v -> %+v", mac, u, after[mac])
		}
	}
}
