package usecase

import (
	"encoding/json"
	"testing"
	"time"

	coredomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/inventory/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

const tenant = "11111111-1111-1111-1111-111111111111"

var base = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// host builds one observation of a station, as a relay reports it.
func host(mac, hostname, ip string, at time.Time) port.ObservationView {
	return port.ObservationView{
		MAC: mac, At: at, IPv4: ip, Hostname: hostname,
		SSID: "MonWiFi-2G", Band: "2.4G", Associated: true,
		// Associated a minute ago: a station that just arrived, which is what
		// makes a hand-off visible to the recognition rules.
		AssocSince: ptr(at.Add(-time.Minute)),
	}
}

func ptr[T any](v T) *T { return &v }

func poll(at time.Time, hosts ...port.ObservationView) Poll {
	return Poll{TenantID: tenant, AgentID: "agent", AsOf: at, SHA256: "digest", Hosts: hosts}
}

// settle runs a diff and folds its changes back into an inventory snapshot,
// so a test can push two polls in a row the way the hub does.
func settle(inv domain.Snapshot, ch domain.Changes) domain.Snapshot {
	out := domain.Snapshot{Baseline: inv.Baseline || ch.SetBaseline, NextDeviceSeq: inv.NextDeviceSeq}
	byMAC := map[string]domain.MACRecord{}
	for _, m := range inv.MACs {
		byMAC[m.MAC] = m
	}
	for _, m := range ch.MACs {
		byMAC[m.MAC] = m
	}
	for _, m := range byMAC {
		out.MACs = append(out.MACs, m)
	}
	for _, d := range ch.Devices {
		if n := domain.DeviceSeq(d.ID); n > out.NextDeviceSeq {
			out.NextDeviceSeq = n
		}
	}
	out.OpenWindow = ch.Window
	if out.OpenWindow == nil {
		out.OpenWindow = inv.OpenWindow
	}
	return out
}

func diff(inv domain.Snapshot, p Poll, now time.Time) domain.Changes {
	return Diff(inv, p, now, coredomain.DefaultRecognitionRules, 0)
}

// TestDiff_FirstSnapshotIsTheBaseline: a household that has just paired a
// relay must not be told that each of its own phones is a stranger.
func TestDiff_FirstSnapshotIsTheBaseline(t *testing.T) {
	ch := diff(domain.Snapshot{}, poll(base,
		host("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10", base),
		host("ac:bb:cc:00:00:22", "Laptop-Bureau", "192.168.1.11", base),
	), base)

	if !ch.SetBaseline {
		t.Error("the first snapshot did not set the baseline")
	}
	if len(ch.Devices) != 2 || len(ch.MACs) != 2 {
		t.Fatalf("got %d devices and %d addresses, want 2 and 2", len(ch.Devices), len(ch.MACs))
	}
	if ch.Window != nil {
		t.Errorf("the baseline raised an alert for %d devices", len(ch.Window.Items))
	}
	if ch.Devices[0].ID != "DEVICE-000001" || ch.Devices[1].ID != "DEVICE-000002" {
		t.Errorf("device ids = %s, %s, want DEVICE-000001 and DEVICE-000002", ch.Devices[0].ID, ch.Devices[1].ID)
	}
}

// TestDiff_UnknownDeviceAlerts is the point of H2.
func TestDiff_UnknownDeviceAlerts(t *testing.T) {
	inv := settle(domain.Snapshot{}, diff(domain.Snapshot{}, poll(base,
		host("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10", base)), base))

	later := base.Add(time.Minute)
	ch := diff(inv, poll(later,
		host("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10", later),
		host("aa:11:22:33:44:55", "TECNO-POP-10", "192.168.1.77", later),
	), later)

	if ch.Touched != 1 {
		t.Errorf("Touched = %d, want the one address we already knew", ch.Touched)
	}
	if len(ch.Devices) != 1 {
		t.Fatalf("got %d new devices, want 1", len(ch.Devices))
	}
	if ch.Window == nil || len(ch.Window.Items) != 1 {
		t.Fatalf("no alert for the unknown device: %+v", ch.Window)
	}
	it := ch.Window.Items[0]
	if it.No != 1 || it.MAC != "aa:11:22:33:44:55" || it.Hostname != "TECNO-POP-10" {
		t.Errorf("alert item = %+v, want number 1 for the new station", it)
	}
	if it.SnapshotSHA256 != "digest" || !it.AsOf.Equal(later) {
		t.Errorf("the alert does not point at the signed snapshot it came from: %+v", it)
	}
}

// TestDiff_RotatedMACIsProposedNotAlerted: a phone that comes back under a
// private address keeps its host name. Recognition proposes it, and a
// proposal is silent — alerting on a device the household already has is how
// people learn to ignore alerts (RFC-005 H2 as built, departure 1).
func TestDiff_RotatedMACIsProposedNotAlerted(t *testing.T) {
	inv := settle(domain.Snapshot{}, diff(domain.Snapshot{}, poll(base,
		host("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10", base)), base))

	// It leaves, then comes back ten minutes later under a random MAC: the
	// handoff window of the recognition rules.
	later := base.Add(10 * time.Minute)
	ch := diff(inv, poll(later, host("a6:11:22:33:44:55", "Phone-Salon", "192.168.1.12", later)), later)

	if ch.Window != nil {
		t.Fatalf("a known phone with a rotated MAC raised an alert: %+v", ch.Window.Items)
	}
	if len(ch.Devices) != 0 {
		t.Errorf("got %d new devices, want none: it is the same phone", len(ch.Devices))
	}
	if len(ch.MACs) != 1 {
		t.Fatalf("got %d records, want the proposal", len(ch.MACs))
	}
	rec := ch.MACs[0]
	if rec.State != domain.StateProposed || rec.DeviceID != "DEVICE-000001" {
		t.Errorf("record = %+v, want it proposed for DEVICE-000001", rec)
	}
}

// TestDiff_SharedHostNameIsNoEvidence: two stations answering to the same
// generic-looking name are not one device, so the second one is still news.
func TestDiff_SharedHostNameIsNoEvidence(t *testing.T) {
	first := diff(domain.Snapshot{}, poll(base,
		host("ac:bb:cc:00:00:11", "iPhone-de-Awa", "192.168.1.10", base),
		host("ac:bb:cc:00:00:22", "iPhone-de-Awa", "192.168.1.11", base),
	), base)
	inv := settle(domain.Snapshot{}, first)

	later := base.Add(10 * time.Minute)
	ch := diff(inv, poll(later, host("a6:11:22:33:44:55", "iPhone-de-Awa", "192.168.1.30", later)), later)

	if ch.Window == nil || len(ch.Window.Items) != 1 {
		t.Fatalf("a name two devices share decided a recognition: %+v", ch.Window)
	}
}

// TestDiff_TwoArrivalsShareOneWindow: two devices in the same two minutes
// give one alert, numbered 1 and 2.
func TestDiff_TwoArrivalsShareOneWindow(t *testing.T) {
	inv := settle(domain.Snapshot{}, diff(domain.Snapshot{}, poll(base,
		host("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10", base)), base))

	t1 := base.Add(time.Minute)
	first := diff(inv, poll(t1,
		host("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10", t1),
		host("aa:11:22:33:44:55", "TECNO-POP-10", "192.168.1.77", t1),
	), t1)
	inv = settle(inv, first)

	t2 := t1.Add(30 * time.Second)
	second := diff(inv, poll(t2,
		host("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10", t2),
		host("aa:11:22:33:44:55", "TECNO-POP-10", "192.168.1.77", t2),
		host("bb:22:33:44:55:66", "Chromecast", "192.168.1.78", t2),
	), t2)

	if second.Window == nil {
		t.Fatal("the second arrival opened no window")
	}
	if second.Window.ID != first.Window.ID {
		t.Errorf("window ids differ (%q vs %q): the two arrivals must share one alert", first.Window.ID, second.Window.ID)
	}
	if len(second.Window.Items) != 2 {
		t.Fatalf("got %d items, want both arrivals in one window", len(second.Window.Items))
	}
	if second.Window.Items[0].No != 1 || second.Window.Items[1].No != 2 {
		t.Errorf("items numbered %d and %d, want 1 and 2", second.Window.Items[0].No, second.Window.Items[1].No)
	}
	if !second.Window.ClosesAt.Equal(first.Window.ClosesAt) {
		t.Error("the window's deadline moved: a stream of arrivals would never be announced")
	}
}

// TestDiff_WindowReopensAfterItClosed: once the two minutes are up, the next
// arrival starts a fresh alert instead of joining one nobody sent yet.
func TestDiff_WindowReopensAfterItClosed(t *testing.T) {
	inv := settle(domain.Snapshot{}, diff(domain.Snapshot{}, poll(base,
		host("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10", base)), base))

	t1 := base.Add(time.Minute)
	first := diff(inv, poll(t1, host("aa:11:22:33:44:55", "TECNO-POP-10", "192.168.1.77", t1)), t1)
	inv = settle(inv, first)
	inv.OpenWindow.ID = "the-first-window"

	t2 := t1.Add(AlertWindow + time.Second)
	second := diff(inv, poll(t2, host("bb:22:33:44:55:66", "Chromecast", "192.168.1.78", t2)), t2)
	if second.Window == nil || second.Window.ID != "" {
		t.Fatalf("the arrival joined a window that had closed: %+v", second.Window)
	}
	if len(second.Window.Items) != 1 || second.Window.Items[0].No != 1 {
		t.Errorf("the new window = %+v, want one item numbered 1", second.Window.Items)
	}
}

// TestDiff_RelayItselfRaisesNoAlert: the relay is a device of the household
// by definition, and one that re-paired must not look like an intruder.
func TestDiff_RelayItselfRaisesNoAlert(t *testing.T) {
	inv := settle(domain.Snapshot{}, diff(domain.Snapshot{}, poll(base,
		host("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10", base)), base))

	later := base.Add(time.Minute)
	p := poll(later, host("aa:11:22:33:44:55", "Pixel-du-relais", "192.168.1.50", later))
	p.RelayMAC = "AA:11:22:33:44:55" // the relay reports it as the box spells it
	ch := diff(inv, p, later)

	if ch.Window != nil {
		t.Errorf("the relay's own device raised an alert: %+v", ch.Window.Items)
	}
	if len(ch.Devices) != 1 {
		t.Errorf("the relay's own device was not recorded: %+v", ch.Devices)
	}
}

// TestDiff_KnownAddressKeepsItsFirstSeen: the inventory remembers when a
// device first showed up, whatever the latest poll says.
func TestDiff_KnownAddressKeepsItsFirstSeen(t *testing.T) {
	inv := settle(domain.Snapshot{}, diff(domain.Snapshot{}, poll(base,
		host("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10", base)), base))

	later := base.Add(48 * time.Hour)
	ch := diff(inv, poll(later, host("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.19", later)), later)
	if len(ch.MACs) != 1 {
		t.Fatalf("got %d records, want 1", len(ch.MACs))
	}
	rec := ch.MACs[0]
	if !rec.FirstSeen.Equal(base) {
		t.Errorf("FirstSeen = %s, want the first poll %s", rec.FirstSeen, base)
	}
	if !rec.LastSeen.Equal(later) || rec.IP != "192.168.1.19" {
		t.Errorf("record = %+v, want the latest poll's address and time", rec)
	}
	var stored port.ObservationView
	if err := json.Unmarshal(rec.LastObs, &stored); err != nil {
		t.Fatalf("the stored observation is not readable back: %v", err)
	}
	if stored.MAC != "ac:bb:cc:00:00:11" {
		t.Errorf("stored observation = %+v, want the station's own view", stored)
	}
}
