package usecase

import (
	"context"
	"fmt"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Metering samples per-device traffic counters and accumulates consumption per
// billing period.
type Metering struct {
	factory port.RouterFactory
	vault   port.CredentialVault
	disco   port.DiscoveryPort
	usage   port.UsageRepository
	clock   port.Clock
	log     port.Logger
}

// NewMetering wires the interactor.
func NewMetering(f port.RouterFactory, v port.CredentialVault, d port.DiscoveryPort, u port.UsageRepository, c port.Clock, l port.Logger) *Metering {
	return &Metering{factory: f, vault: v, disco: d, usage: u, clock: c, log: l}
}

// SampleResult summarises one sampling round.
type SampleResult struct {
	Devices  int
	Bytes    uint64
	Baseline bool // first sample ever for this router: recorded, nothing counted
}

// Sample reads the router's counters once and records the deltas.
func (uc *Metering) Sample(ctx context.Context, in MACFilterInput) (SampleResult, error) {
	gw, err := loggedInGateway(ctx, uc.factory, uc.vault, uc.disco, routerTarget(in))
	if err != nil {
		return SampleResult{}, err
	}
	meter, ok := gw.(port.TrafficMeter)
	if !ok {
		return SampleResult{}, fmt.Errorf("%w: adapter %s cannot measure traffic", domain.ErrNotImplemented, gw.Meta().ID)
	}
	cur, err := meter.TrafficCounters(ctx)
	if err != nil {
		return SampleResult{}, err
	}
	routerID := RouterID(in.BaseURL)
	prev, err := uc.usage.LastSamples(ctx, routerID)
	if err != nil {
		return SampleResult{}, err
	}

	res := SampleResult{Devices: len(cur), Baseline: len(prev) == 0}
	deltas := make(map[domain.MAC]domain.UsageDelta, len(cur))
	for mac, c := range cur {
		if res.Baseline {
			continue
		}
		p, seen := prev[mac]
		if !seen {
			// Associated since the previous round: everything on its counter is new.
			p = domain.TrafficCounters{}
		}
		d := domain.UsageDeltaOf(p, c)
		deltas[mac] = d
		res.Bytes += d.Total()
	}
	if err := uc.usage.RecordSamples(ctx, routerID, uc.clock.Now(), cur, deltas); err != nil {
		return SampleResult{}, err
	}
	return res, nil
}

// Usage returns per-device consumption for period ("" = current month).
func (uc *Metering) Usage(ctx context.Context, baseURL, period string) ([]domain.Usage, error) {
	if period == "" {
		period = domain.BillingPeriod(uc.clock.Now())
	}
	return uc.usage.UsageByPeriod(ctx, RouterID(baseURL), period)
}

// Period is the current month ("YYYY-MM"): the one the free Conso tab shows.
func (uc *Metering) Period() string { return domain.BillingPeriod(uc.clock.Now()) }

// DeviceHistory is one device's consumption over the last months (the
// current one included), oldest first, a month without a measure at zero.
// months is clamped to 1..domain.UsageRetentionMonths, the days kept.
func (uc *Metering) DeviceHistory(ctx context.Context, baseURL string, mac domain.MAC, months int) ([]domain.Usage, error) {
	months = min(max(months, 1), domain.UsageRetentionMonths)
	periods := domain.PeriodsBack(uc.clock.Now(), months)
	rows, err := uc.usage.DeviceMonths(ctx, RouterID(baseURL), mac, periods[0], periods[len(periods)-1])
	if err != nil {
		return nil, err
	}
	byPeriod := make(map[string]domain.Usage, len(rows))
	for _, r := range rows {
		byPeriod[r.Period] = r
	}
	out := make([]domain.Usage, len(periods))
	for i, p := range periods {
		u, ok := byPeriod[p]
		if !ok {
			u = domain.Usage{MAC: mac, Period: p}
		}
		out[i] = u
	}
	return out, nil
}
