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
}

func (m *memUsage) LastSamples(context.Context, string) (map[domain.MAC]domain.TrafficCounters, error) {
	out := map[domain.MAC]domain.TrafficCounters{}
	for k, v := range m.last {
		out[k] = v
	}
	return out, nil
}
func (m *memUsage) RecordSamples(_ context.Context, _, period string, s map[domain.MAC]domain.TrafficCounters, d map[domain.MAC]uint64) error {
	for k, v := range s {
		m.last[k] = v
	}
	for k, v := range d {
		m.usage[period+"|"+k.String()] += v
	}
	return nil
}
func (m *memUsage) UsageByPeriod(context.Context, string, string) ([]domain.Usage, error) {
	return nil, nil
}

func TestMetering_BaselineThenDeltas(t *testing.T) {
	a := domain.MustMAC("aa:bb:cc:00:00:11")
	b := domain.MustMAC("aa:bb:cc:00:00:22")
	gw := &meterGateway{fakeGateway: fakeGateway{meta: f660Meta()},
		counters: map[domain.MAC]domain.TrafficCounters{a: {Rx: 100, Tx: 900, LinkSecs: 600}}}
	repo := &memUsage{last: map[domain.MAC]domain.TrafficCounters{}, usage: map[string]uint64{}}
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
}
