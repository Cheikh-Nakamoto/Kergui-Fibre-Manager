package usecase

import (
	"context"
	"fmt"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// ObserveHosts exposes the router's raw per-station view: one merged
// observation per MAC (Wi-Fi association, DHCP lease, radio capabilities) and
// the byte counters.
//
// It is read-only and keeps no state. The callers are the relay, which signs
// what it reads into a snapshot for the hub's inventory diff (RFC-005 H2), and
// H4's usage charts. Metering already samples the same counters, but it
// accumulates them into billing periods; this one hands them over untouched,
// because the hub must be able to compute its own deltas from two signed
// snapshots rather than trust a total.
type ObserveHosts struct {
	factory port.RouterFactory
	vault   port.CredentialVault
	disco   port.DiscoveryPort
	log     port.Logger
}

// NewObserveHosts wires the interactor.
func NewObserveHosts(f port.RouterFactory, v port.CredentialVault, d port.DiscoveryPort, l port.Logger) *ObserveHosts {
	return &ObserveHosts{factory: f, vault: v, disco: d, log: l}
}

// Observations returns one observation per station the router currently
// reports. An adapter that cannot observe hosts answers ErrNotImplemented:
// the capability is optional, and the caller falls back to the device list.
func (uc *ObserveHosts) Observations(ctx context.Context, in MACFilterInput) ([]domain.Observation, error) {
	gw, err := loggedInGateway(ctx, uc.factory, uc.vault, uc.disco, routerTarget(in))
	if err != nil {
		return nil, err
	}
	obs, ok := gw.(port.HostObserver)
	if !ok {
		return nil, fmt.Errorf("%w: adapter %s cannot observe hosts", domain.ErrNotImplemented, gw.Meta().ID)
	}
	return obs.Observe(ctx)
}

// Traffic returns the cumulative counters the router reports per station.
func (uc *ObserveHosts) Traffic(ctx context.Context, in MACFilterInput) (map[domain.MAC]domain.TrafficCounters, error) {
	gw, err := loggedInGateway(ctx, uc.factory, uc.vault, uc.disco, routerTarget(in))
	if err != nil {
		return nil, err
	}
	meter, ok := gw.(port.TrafficMeter)
	if !ok {
		return nil, fmt.Errorf("%w: adapter %s cannot measure traffic", domain.ErrNotImplemented, gw.Meta().ID)
	}
	return meter.TrafficCounters(ctx)
}
