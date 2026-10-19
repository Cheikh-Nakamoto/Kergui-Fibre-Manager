package e2e

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/snapshot"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// TestE2E_UnknownDeviceBecomesAnEvent is H2's first half, end to end: a
// device joins the mock F6600P, the relay polls it and pushes a signed
// snapshot, the hub sees it as new, and one grouped event comes out.
//
// Nothing here is simulated between the box and the event: the relay reads
// the captured F6600P protocol over TLS and signs what it saw, and the hub
// decides only from those signed bytes.
func TestE2E_UnknownDeviceBecomesAnEvent(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.owner.registerNetwork(w.boxPin)
	w.owner.publishBundle(w.boxPin)
	_, state, _ := w.startRelay(ctx, w.owner.invite("pc"), "pc", "192.168.1.50")
	w.owner.publishBundle(w.boxPin, trustRelay(state, "pc", t))
	w.owner.sealCredentials(state.AgentID, relayPublic(t, state).X25519)

	// The relay itself is on the Wi-Fi, like a phone left plugged in at
	// home: that is how it finds its own address and refuses to cut it.
	w.box.Join(mockrouter.Station{
		MAC: relayStationMAC, Hostname: "PC-du-relais", IP: "192.168.1.50", AP: "DEV.WIFI.AP1",
	})

	// The relay takes the lease and reports the household as it is: that
	// first snapshot is the baseline, and nothing in it is news.
	waitForInventory(t, w, 4)
	if n := countRows(t, `SELECT count(*) FROM hub_inventory_windows WHERE tenant_id = $1`, w.owner.tenantID); n != 0 {
		t.Fatalf("the baseline opened %d alert windows", n)
	}

	// Two devices join the Wi-Fi inside the same grouping window.
	w.box.Join(mockrouter.Station{
		MAC: "aa:11:22:33:44:55", Hostname: "TECNO-POP-10", IP: "192.168.1.77", AP: "DEV.WIFI.AP1",
	})
	w.box.Join(mockrouter.Station{
		MAC: "bb:22:33:44:55:66", Hostname: "Chromecast", IP: "192.168.1.78", AP: "DEV.WIFI.AP5",
	})

	events := waitForEvents(t, w.owner.tenantID, 20*time.Second)
	if len(events) != 1 {
		t.Fatalf("got %d router.device_first_seen events, want one grouped alert", len(events))
	}
	devices, _ := events[0]["devices"].([]any)
	if len(devices) != 2 {
		t.Fatalf("the event names %d devices, want both arrivals", len(devices))
	}
	byMAC := map[string]map[string]any{}
	for _, d := range devices {
		if m, ok := d.(map[string]any); ok {
			byMAC[str(m, "mac")] = m
		}
	}
	for _, want := range []string{"aa:11:22:33:44:55", "bb:22:33:44:55:66"} {
		item, ok := byMAC[want]
		if !ok {
			t.Fatalf("%s is not in the alert: %v", want, byMAC)
		}
		if str(item, "snapshot_sha256") == "" {
			t.Errorf("%s carries no snapshot digest, so the app cannot verify the alert", want)
		}
	}
	// The numbers are what the owner answers in WhatsApp.
	if byMAC["aa:11:22:33:44:55"]["no"] == byMAC["bb:22:33:44:55:66"]["no"] {
		t.Error("the two devices share one number: a reply could not tell them apart")
	}

	// The observations snapshot the alert came from is the relay's, signed.
	kind, payload := latestSnapshot(t, w.owner.tenantID, snapshot.KindObservations)
	if kind == "" {
		t.Fatal("the relay pushed no observations snapshot")
	}
	var signed trust.SignedSnapshot
	if err := json.Unmarshal(payload, &signed); err != nil {
		t.Fatal(err)
	}
	snap, err := signed.Open(trust.DomainSnapshot, relayPublic(t, state).Ed25519)
	if err != nil {
		t.Fatalf("the stored snapshot is not the relay's: %v", err)
	}
	var body snapshot.Observations
	if err := json.Unmarshal(snap.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Degraded {
		t.Error("the F6600P adapter observes hosts, so the snapshot must not be the degraded one")
	}
	if body.RelayMAC == "" {
		t.Error("the relay did not report which device carries it")
	}
	var withTraffic int
	for _, h := range body.Hosts {
		if _, ok := h.TrafficCounters(); ok {
			withTraffic++
		}
		if h.Lease == nil {
			t.Errorf("%s carries no DHCP lease: the three pages were not merged", h.MAC)
		}
	}
	if withTraffic == 0 {
		t.Error("no station carries traffic counters")
	}

	// The SSIDs travel too, on their own slower schedule.
	if kind, payload := latestSnapshot(t, w.owner.tenantID, snapshot.KindSSIDs); kind == "" {
		t.Error("the relay pushed no SSID snapshot")
	} else {
		var signed trust.SignedSnapshot
		if err := json.Unmarshal(payload, &signed); err != nil {
			t.Fatal(err)
		}
		snap, err := signed.Open(trust.DomainSnapshot, relayPublic(t, state).Ed25519)
		if err != nil {
			t.Fatalf("the SSID snapshot is not the relay's: %v", err)
		}
		var body snapshot.SSIDs
		if err := json.Unmarshal(snap.Body, &body); err != nil {
			t.Fatal(err)
		}
		if len(body.SSIDs) != 2 {
			t.Errorf("got %d SSIDs, want the box's 2", len(body.SSIDs))
		}
	}
}

// TestE2E_KnownDeviceWithARotatedMACIsSilent: the household's own phone
// comes back under a private address and nobody is woken up.
func TestE2E_KnownDeviceWithARotatedMACIsSilent(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.owner.registerNetwork(w.boxPin)
	w.owner.publishBundle(w.boxPin)
	_, state, _ := w.startRelay(ctx, w.owner.invite("pc"), "pc", "192.168.1.50")
	w.owner.publishBundle(w.boxPin, trustRelay(state, "pc", t))
	w.owner.sealCredentials(state.AgentID, relayPublic(t, state).X25519)
	waitForInventory(t, w, 3)

	// Phone-Salon leaves the Wi-Fi, and the relay reports a poll without it:
	// from then on, the inventory's last sighting of it stops moving.
	w.box.Forget("ac:bb:cc:00:00:11")
	waitForAbsence(t, w, "ac:bb:cc:00:00:11")

	// It comes back under a private address, having just reassociated. Same
	// host name, same band and the same radio it always had: a chip does not
	// change its Wi-Fi standard, and a station reporting another one would
	// be a different device however familiar its name looks.
	w.box.Join(mockrouter.Station{
		MAC: "a6:11:22:33:44:55", Hostname: "Phone-Salon", IP: "192.168.1.12",
		AP: "DEV.WIFI.AP1", LinkSecs: 1, Standard: "11n", MaxLinkMbps: 600,
	})

	// Give the relay several snapshot rounds and the sweep its chance.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if countRows(t, `SELECT count(*) FROM hub_inventory_macs WHERE tenant_id = $1 AND state = 'proposed'`,
			w.owner.tenantID) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if n := countRows(t, `SELECT count(*) FROM hub_inventory_macs WHERE tenant_id = $1 AND state = 'proposed'`,
		w.owner.tenantID); n != 1 {
		t.Fatalf("got %d proposed addresses, want the rotated one", n)
	}
	if events := currentEvents(t, w.owner.tenantID); len(events) != 0 {
		t.Fatalf("a known phone with a rotated MAC raised %d alerts", len(events))
	}
	// And it did not become a second device.
	if n := countRows(t, `SELECT count(*) FROM hub_inventory_devices WHERE tenant_id = $1`, w.owner.tenantID); n != 3 {
		t.Errorf("the household now has %d devices, want the 3 it started with", n)
	}
}

// relayStationMAC is the address the PC relay answers to on the mock box.
const relayStationMAC = "aa:bb:cc:dd:ee:50"

// --- helpers --------------------------------------------------------------

// waitForInventory waits for the hub to have absorbed a first snapshot.
func waitForInventory(t *testing.T, w *world, wantDevices int) {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if countRows(t, `SELECT count(*) FROM hub_inventory_devices WHERE tenant_id = $1`,
			w.owner.tenantID) >= wantDevices {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the hub's inventory never reached %d devices", wantDevices)
}

// waitForAbsence waits for a snapshot that no longer lists a MAC, which is
// how a test says "that device really left" instead of guessing a delay.
func waitForAbsence(t *testing.T, w *world, mac string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, h := range storedHosts(t, w) {
			if h.MAC == mac {
				goto wait
			}
		}
		return
	wait:
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s still shows up in the relay's snapshots", mac)
}

// storedHosts decodes the stations of the newest observations snapshot.
func storedHosts(t *testing.T, w *world) []port.ObservationView {
	t.Helper()
	_, payload := latestSnapshot(t, w.owner.tenantID, snapshot.KindObservations)
	if payload == nil {
		return nil
	}
	var signed trust.SignedSnapshot
	if err := json.Unmarshal(payload, &signed); err != nil {
		t.Fatal(err)
	}
	snap, err := signed.Unverified()
	if err != nil {
		t.Fatal(err)
	}
	var body snapshot.Observations
	if err := json.Unmarshal(snap.Body, &body); err != nil {
		t.Fatal(err)
	}
	return body.Hosts
}

func waitForEvents(t *testing.T, tenantID string, timeout time.Duration) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		events := currentEvents(t, tenantID)
		if len(events) > 0 || time.Now().After(deadline) {
			return events
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func currentEvents(t *testing.T, tenantID string) []map[string]any {
	t.Helper()
	pool := e2ePool(t)
	rows, err := pool.Query(context.Background(),
		`SELECT payload FROM event_outbox
		 WHERE event_type = 'router.device_first_seen' AND payload->>'tenant_id' = $1`, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
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
	return out
}

func latestSnapshot(t *testing.T, tenantID, kind string) (string, []byte) {
	t.Helper()
	var (
		got     string
		payload []byte
	)
	err := e2ePool(t).QueryRow(context.Background(),
		`SELECT kind, payload FROM hub_relay_snapshots WHERE tenant_id = $1 AND kind = $2`,
		tenantID, kind).Scan(&got, &payload)
	if err != nil {
		return "", nil
	}
	return got, payload
}

func countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e2ePool(t).QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// sharedPool is one small pool for the whole test binary. These helpers poll
// in loops, and a pool per call exhausts Postgres' connection slots long
// before the assertions are reached.
var sharedPool = sync.OnceValues(func() (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(os.Getenv(testDatabaseEnv))
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 2
	return pgxpool.NewWithConfig(context.Background(), cfg)
})

func e2ePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := sharedPool()
	if err != nil {
		t.Fatal(err)
	}
	return pool
}
