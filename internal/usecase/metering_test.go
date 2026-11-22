package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

type meterGateway struct {
	fakeGateway
	counters map[domain.MAC]domain.TrafficCounters
}

func (g *meterGateway) TrafficCounters(context.Context) (map[domain.MAC]domain.TrafficCounters, error) {
	return g.counters, nil
}

type meterFactory struct{ gw *meterGateway }

func (f meterFactory) New(string, string, port.RouterOptions) (port.RouterPort, error) {
	return f.gw, nil
}
func (f meterFactory) Available() []domain.AdapterMeta          { return nil }
func (f meterFactory) Lookup(string) (domain.AdapterMeta, bool) { return f.gw.meta, true }

type memUsage struct {
	last  map[domain.MAC]domain.TrafficCounters
	usage map[string]uint64 // period|mac
	split map[string]domain.UsageDelta
	asked []string // DeviceMonths ranges
}

func addDelta(a, b domain.UsageDelta) domain.UsageDelta {
	return domain.UsageDelta{Sent: a.Sent + b.Sent, Received: a.Received + b.Received}
}

func (m *memUsage) LastSamples(context.Context, string) (map[domain.MAC]domain.TrafficCounters, error) {
	out := map[domain.MAC]domain.TrafficCounters{}
	for k, v := range m.last {
		out[k] = v
	}
	return out, nil
}
func (m *memUsage) RecordSamples(_ context.Context, _ string, at time.Time, s map[domain.MAC]domain.TrafficCounters, d map[domain.MAC]domain.UsageDelta) error {
	period := domain.BillingPeriod(at)
	for k, v := range s {
		m.last[k] = v
	}
	for k, v := range d {
		m.usage[period+"|"+k.String()] += v.Total()
		m.split[period+"|"+k.String()] = addDelta(m.split[period+"|"+k.String()], v)
	}
	return nil
}
func (m *memUsage) UsageByPeriod(context.Context, string, string) ([]domain.Usage, error) {
	return nil, nil
}

// DeviceMonths answers from the per-period totals (the repository adds the
// days up; see the sqlite tests).
func (m *memUsage) DeviceMonths(_ context.Context, _ string, mac domain.MAC, from, to string) ([]domain.Usage, error) {
	m.asked = append(m.asked, from+".."+to)
	out := []domain.Usage{}
	for _, p := range []string{"2026-04", "2026-05", "2026-06", "2026-07", "2026-08", "2026-09", "2026-10"} {
		if p < from || p > to {
			continue
		}
		if b, ok := m.usage[p+"|"+mac.String()]; ok {
			d := m.split[p+"|"+mac.String()]
			out = append(out, domain.Usage{MAC: mac, Period: p, Bytes: b, Sent: d.Sent, Received: d.Received})
		}
	}
	return out, nil
}

// TestMetering_DeviceHistory: the last n months of one device, oldest
// first, a month without any measure at zero, so the app draws n points.
func TestMetering_DeviceHistory(t *testing.T) {
	a := domain.MustMAC("aa:bb:cc:00:00:11")
	repo := &memUsage{last: map[domain.MAC]domain.TrafficCounters{}, usage: map[string]uint64{
		"2026-04|" + a.String(): 1, // before the window
		"2026-06|" + a.String(): 600,
		"2026-09|" + a.String(): 900,
	}, split: map[string]domain.UsageDelta{"2026-09|" + a.String(): {Sent: 100, Received: 800}}}
	m := usecase.NewMetering(nil, nil, nil, repo, fakeClock{time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC)}, nopLogger{})

	got, err := m.DeviceHistory(context.Background(), "http://r", a, 6)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.Usage{
		{MAC: a, Period: "2026-05"},
		{MAC: a, Period: "2026-06", Bytes: 600},
		{MAC: a, Period: "2026-07"},
		{MAC: a, Period: "2026-08"},
		{MAC: a, Period: "2026-09", Bytes: 900, Sent: 100, Received: 800},
		{MAC: a, Period: "2026-10"},
	}
	if len(got) != len(want) {
		t.Fatalf("DeviceHistory = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DeviceHistory[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if len(repo.asked) != 1 || repo.asked[0] != "2026-05..2026-10" {
		t.Errorf("asked the repository for %v, want 2026-05..2026-10", repo.asked)
	}

	// The window is clamped to what is kept: 1 to 13 months.
	if got, _ := m.DeviceHistory(context.Background(), "http://r", a, 40); len(got) != domain.UsageRetentionMonths {
		t.Errorf("40 months asked: %d months, want %d", len(got), domain.UsageRetentionMonths)
	}
	if got, _ := m.DeviceHistory(context.Background(), "http://r", a, 0); len(got) != 1 {
		t.Errorf("0 months asked: %d months, want 1", len(got))
	}
}

func TestMetering_Period(t *testing.T) {
	m := usecase.NewMetering(nil, nil, nil, nil, fakeClock{time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC)}, nopLogger{})
	if got := m.Period(); got != "2026-10" {
		t.Errorf("Period = %q, want 2026-10", got)
	}
}

func TestMetering_BaselineThenDeltas(t *testing.T) {
	a := domain.MustMAC("aa:bb:cc:00:00:11")
	b := domain.MustMAC("aa:bb:cc:00:00:22")
	gw := &meterGateway{fakeGateway: fakeGateway{meta: f660Meta()},
		counters: map[domain.MAC]domain.TrafficCounters{a: {Rx: 100, Tx: 900, LinkSecs: 600}}}
	repo := &memUsage{last: map[domain.MAC]domain.TrafficCounters{}, usage: map[string]uint64{}, split: map[string]domain.UsageDelta{}}
	vault := newVault()
	_ = vault.Store(context.Background(), usecase.RouterID("http://r"), domain.Credentials{Username: "u", Password: "p"})
	m := usecase.NewMetering(meterFactory{gw}, vault, fakeDisco{}, repo,
		fakeClock{time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}, nopLogger{})
	in := usecase.MACFilterInput{BaseURL: "http://r", AdapterID: "zte_f660"}
	ctx := context.Background()

	res, err := m.Sample(ctx, in)
	if err != nil || !res.Baseline || res.Bytes != 0 {
		t.Fatalf("first sample = %+v, %v; want baseline with 0 bytes", res, err)
	}

	gw.counters = map[domain.MAC]domain.TrafficCounters{
		a: {Rx: 300, Tx: 1700, LinkSecs: 900}, // +1000
		b: {Rx: 50, Tx: 450, LinkSecs: 120},   // newly associated: +500
	}
	res, err = m.Sample(ctx, in)
	if err != nil || res.Bytes != 1500 {
		t.Fatalf("second sample = %+v, %v; want 1500 bytes", res, err)
	}
	if repo.usage["2026-10|"+a.String()] != 1000 || repo.usage["2026-10|"+b.String()] != 500 {
		t.Errorf("usage = %v", repo.usage)
	}
	if got := repo.split["2026-10|"+a.String()]; got != (domain.UsageDelta{Sent: 200, Received: 800}) {
		t.Errorf("split for a = %+v, want 200 sent / 800 received", got)
	}
}
