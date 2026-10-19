package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/snapshot"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// These tests drive H2's hub side against a real Postgres: a relay pushes
// observations, the hub builds the household's inventory, and a device it has
// never seen turns into one grouped outbox event.

// pushObservations signs and pushes an observations snapshot, the way
// internal/agent does.
func (r *testRelay) pushObservations(body snapshot.Observations) (int, map[string]any) {
	r.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		r.t.Fatal(err)
	}
	snap, err := trust.NewSnapshot(r.keys, r.tenant, snapshot.KindObservations, r.id, r.epoch, raw, time.Now())
	if err != nil {
		r.t.Fatal(err)
	}
	return r.do("POST", "/relay/v1/snapshots", map[string]any{"payload": snap.Payload, "sig": snap.Sig})
}

// station is one observed device, as a relay reports it: polled now, and
// associated a minute ago.
func station(mac, hostname, ip string) port.ObservationView {
	return stationAt(mac, hostname, ip, time.Now().UTC())
}

// stationAt is the same, polled at a given moment. A test that needs a
// device to have left and come back moves the earlier poll back in time,
// because recognition weighs the gap between the two (handoff window).
func stationAt(mac, hostname, ip string, at time.Time) port.ObservationView {
	since := at.Add(-time.Minute)
	return port.ObservationView{
		MAC: mac, At: at, IPv4: ip, Hostname: hostname, SSID: "MonWiFi-2G",
		Band: "2.4G", Associated: true, AssocSince: &since,
	}
}

// activeRelay pairs a relay, trusts it and lets it take the lease.
func (h *household) activeRelay(t *testing.T) *testRelay {
	t.Helper()
	h.registerNetwork([]string{"MonWiFi-2G"})
	r := h.enroll(h.pairingCode("pc"))
	h.publishBundle(r.trustRelay("pc"))
	if lease, body := r.heartbeat(routerPin, "MonWiFi-2G"); lease != "active" {
		t.Fatalf("the relay did not take the lease: %v", body)
	}
	return r
}

// inventory reads the household's inventory as the app does.
func (h *household) inventory() (int, map[string]any) {
	h.t.Helper()
	return h.owner.do("GET", h.path("router/inventory"), nil)
}

// TestInventory_BaselineThenNewDevice: the first snapshot is the household as
// it already is, and only what turns up afterwards is news.
func TestInventory_BaselineThenNewDevice(t *testing.T) {
	srv := newTestHub(t, WithCallerWait(2*time.Second), WithAlertWindow(time.Second))
	h := signupHousehold(t, srv)
	r := h.activeRelay(t)

	if code, body := r.pushObservations(snapshot.Observations{
		RelayMAC: "ac:bb:cc:00:00:11",
		Hosts: []port.ObservationView{
			station("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10"),
			station("ac:bb:cc:00:00:22", "Laptop-Bureau", "192.168.1.11"),
		},
	}); code != http.StatusNoContent {
		t.Fatalf("POST snapshots = %d %v", code, body)
	}

	code, body := h.inventory()
	if code != http.StatusOK {
		t.Fatalf("GET inventory = %d %v", code, body)
	}
	devices, _ := body["devices"].([]any)
	if len(devices) != 2 {
		t.Fatalf("the inventory holds %d devices, want the household's 2", len(devices))
	}
	if n := windowCount(t, h.tenantID); n != 0 {
		t.Fatalf("the baseline opened %d alert windows", n)
	}

	// A device nobody has seen before.
	if code, body := r.pushObservations(snapshot.Observations{
		RelayMAC: "ac:bb:cc:00:00:11",
		Hosts: []port.ObservationView{
			station("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10"),
			station("ac:bb:cc:00:00:22", "Laptop-Bureau", "192.168.1.11"),
			station("aa:11:22:33:44:55", "TECNO-POP-10", "192.168.1.77"),
		},
	}); code != http.StatusNoContent {
		t.Fatalf("POST snapshots = %d %v", code, body)
	}
	if n := windowCount(t, h.tenantID); n != 1 {
		t.Fatalf("got %d alert windows, want 1", n)
	}

	code, body = h.inventory()
	if code != http.StatusOK {
		t.Fatalf("GET inventory = %d %v", code, body)
	}
	if devices, _ := body["devices"].([]any); len(devices) != 3 {
		t.Errorf("the inventory holds %d devices, want 3", len(devices))
	}
}

// TestInventory_RotatedMACIsProposed: a phone that comes back under a private
// address is proposed for the device it belongs to, and raises no alert.
func TestInventory_RotatedMACIsProposed(t *testing.T) {
	srv := newTestHub(t, WithAlertWindow(time.Second))
	h := signupHousehold(t, srv)
	r := h.activeRelay(t)

	// The phone was last seen ten minutes ago and comes back now under a
	// private address: inside the hand-off window of the recognition rules.
	gone := time.Now().UTC().Add(-10 * time.Minute)
	if code, _ := r.pushObservations(snapshot.Observations{
		Hosts: []port.ObservationView{stationAt("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10", gone)},
	}); code != http.StatusNoContent {
		t.Fatal("the baseline snapshot was refused")
	}
	if code, _ := r.pushObservations(snapshot.Observations{
		Hosts: []port.ObservationView{station("a6:11:22:33:44:55", "Phone-Salon", "192.168.1.12")},
	}); code != http.StatusNoContent {
		t.Fatal("the second snapshot was refused")
	}

	if n := windowCount(t, h.tenantID); n != 0 {
		t.Fatalf("a rotated MAC opened %d alert windows", n)
	}
	code, body := h.inventory()
	if code != http.StatusOK {
		t.Fatalf("GET inventory = %d %v", code, body)
	}
	proposed, _ := body["proposed"].([]any)
	if len(proposed) != 1 {
		t.Fatalf("got %d proposals, want the rotated address", len(proposed))
	}
	first, _ := proposed[0].(map[string]any)
	if str(first, "mac") != "a6:11:22:33:44:55" || str(first, "device_id") != "DEVICE-000001" {
		t.Errorf("proposal = %v, want the rotated address proposed for DEVICE-000001", first)
	}
	if devices, _ := body["devices"].([]any); len(devices) != 1 {
		t.Errorf("the inventory holds %d devices, want 1: it is the same phone", len(devices))
	}
}

// TestInventory_SnapshotOfAnotherHouseholdIsRefused: the hub reads the
// household out of the signed payload, never out of the caller's token
// alone, and refuses a snapshot signed for somebody else.
func TestInventory_SnapshotOfAnotherHouseholdIsRefused(t *testing.T) {
	srv := newTestHub(t)
	h := signupHousehold(t, srv)
	r := h.activeRelay(t)

	raw, err := json.Marshal(snapshot.Observations{
		Hosts: []port.ObservationView{station("aa:11:22:33:44:55", "TECNO-POP-10", "192.168.1.77")},
	})
	if err != nil {
		t.Fatal(err)
	}
	other := "99999999-9999-9999-9999-999999999999"
	snap, err := trust.NewSnapshot(r.keys, other, snapshot.KindObservations, r.id, r.epoch, raw, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	code, _ := r.do("POST", "/relay/v1/snapshots", map[string]any{"payload": snap.Payload, "sig": snap.Sig})
	if code == http.StatusNoContent {
		t.Fatal("the hub stored a snapshot signed for another household")
	}
	if n := inventoryMACCount(t, h.tenantID); n != 0 {
		t.Errorf("%d addresses landed in this household's inventory", n)
	}
}

// TestInventory_ClosedWindowBecomesOneEvent: the window is announced once, as
// a single outbox event carrying both arrivals.
func TestInventory_ClosedWindowBecomesOneEvent(t *testing.T) {
	srv, _ := newRunningTestHub(t, WithAlertWindow(500*time.Millisecond))
	h := signupHousehold(t, srv)
	r := h.activeRelay(t)

	if code, _ := r.pushObservations(snapshot.Observations{
		Hosts: []port.ObservationView{station("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10")},
	}); code != http.StatusNoContent {
		t.Fatal("the baseline snapshot was refused")
	}
	if code, _ := r.pushObservations(snapshot.Observations{
		Hosts: []port.ObservationView{
			station("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10"),
			station("aa:11:22:33:44:55", "TECNO-POP-10", "192.168.1.77"),
			station("bb:22:33:44:55:66", "Chromecast", "192.168.1.78"),
		},
	}); code != http.StatusNoContent {
		t.Fatal("the second snapshot was refused")
	}

	// The sweep announces the window once its time is up.
	events := waitForFirstSeenEvents(t, h.tenantID, 1, 30*time.Second)
	if len(events) != 1 {
		t.Fatalf("got %d router.device_first_seen events, want exactly 1 for the two arrivals", len(events))
	}
	devices, _ := events[0]["devices"].([]any)
	if len(devices) != 2 {
		t.Fatalf("the event names %d devices, want both arrivals", len(devices))
	}
	first, _ := devices[0].(map[string]any)
	if first["no"] != float64(1) || first["mac"] == "" {
		t.Errorf("event device = %v, want it numbered for the WhatsApp reply", first)
	}
	if first["snapshot_sha256"] == "" {
		t.Error("the event does not point at the signed snapshot it came from")
	}
}

// --- helpers --------------------------------------------------------------

// newRunningTestHub is newTestHub plus the background work: the outbox
// dispatcher and the sweeps that announce closed alert windows.
func newRunningTestHub(t *testing.T, opts ...Option) (*httptest.Server, *App) {
	t.Helper()
	srv, app := newTestHubApp(t, opts...)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = app.Run(ctx) }()
	t.Cleanup(func() {
		stop()
		<-done
	})
	return srv, app
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), os.Getenv(testDatabaseEnv))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func windowCount(t *testing.T, tenantID string) int {
	t.Helper()
	var n int
	if err := testPool(t).QueryRow(context.Background(),
		`SELECT count(*) FROM hub_inventory_windows WHERE tenant_id = $1`, tenantID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func inventoryMACCount(t *testing.T, tenantID string) int {
	t.Helper()
	var n int
	if err := testPool(t).QueryRow(context.Background(),
		`SELECT count(*) FROM hub_inventory_macs WHERE tenant_id = $1`, tenantID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// waitForFirstSeenEvents waits for the sweep to publish the household's
// "new device" events and returns their payloads.
func waitForFirstSeenEvents(t *testing.T, tenantID string, want int, timeout time.Duration) []map[string]any {
	t.Helper()
	pool := testPool(t)
	deadline := time.Now().Add(timeout)
	for {
		rows, err := pool.Query(context.Background(),
			`SELECT payload FROM event_outbox
			 WHERE event_type = 'router.device_first_seen' AND payload->>'tenant_id' = $1`, tenantID)
		if err != nil {
			t.Fatal(err)
		}
		var out []map[string]any
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			out = append(out, payload)
		}
		rows.Close()
		if len(out) >= want || time.Now().After(deadline) {
			return out
		}
		time.Sleep(100 * time.Millisecond)
	}
}
