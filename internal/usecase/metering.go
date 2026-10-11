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
	deltas := make(map[domain.MAC]uint64, len(cur))
	for mac, c := range cur {
		if res.Baseline {
			continue
		}
		p, seen := prev[mac]
		if !seen {
			// Associated since the previous round: everything on its counter is new.
			p = domain.TrafficCounters{}
		}
		d := domain.TrafficDelta(p, c)
		deltas[mac] = d
		res.Bytes += d
	}
	if err := uc.usage.RecordSamples(ctx, routerID, domain.BillingPeriod(uc.clock.Now()), cur, deltas); err != nil {
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
