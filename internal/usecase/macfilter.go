package usecase

import (
	"context"
	"fmt"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// MACFilterInput targets one router's global MAC filter switch.
type MACFilterInput struct {
	BaseURL   string
	AdapterID string
	Opts      port.RouterOptions
	Creds     domain.Credentials
}

// MACFilter reads and sets the router's global MAC filter switch.
type MACFilter struct {
	factory port.RouterFactory
	vault   port.CredentialVault
	disco   port.DiscoveryPort
	log     port.Logger
}

// NewMACFilter wires the interactor.
func NewMACFilter(f port.RouterFactory, v port.CredentialVault, d port.DiscoveryPort, l port.Logger) *MACFilter {
	return &MACFilter{factory: f, vault: v, disco: d, log: l}
}

func (uc *MACFilter) gateway(ctx context.Context, in MACFilterInput) (port.MACFilterSwitch, error) {
	gw, err := loggedInGateway(ctx, uc.factory, uc.vault, uc.disco, routerTarget(in))
	if err != nil {
		return nil, err
	}
	sw, ok := gw.(port.MACFilterSwitch)
	if !ok {
		return nil, fmt.Errorf("%w: adapter %s has no MAC filter switch", domain.ErrNotImplemented, gw.Meta().ID)
	}
	return sw, nil
}

// Enabled reports whether the MAC filter is active.
func (uc *MACFilter) Enabled(ctx context.Context, in MACFilterInput) (bool, error) {
	sw, err := uc.gateway(ctx, in)
	if err != nil {
		return false, err
	}
	return sw.MACFilterEnabled(ctx)
}

// Set turns the MAC filter on or off.
func (uc *MACFilter) Set(ctx context.Context, in MACFilterInput, enabled bool) error {
	sw, err := uc.gateway(ctx, in)
	if err != nil {
		return err
	}
	return sw.SetMACFilterEnabled(ctx, enabled)
}
