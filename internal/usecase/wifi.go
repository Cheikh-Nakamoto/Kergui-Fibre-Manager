package usecase

import (
	"context"
	"fmt"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// WiFiAccess disconnects devices at the Wi-Fi access point (as opposed to the
// firewall MAC filter, which only cuts their internet traffic).
type WiFiAccess struct {
	factory port.RouterFactory
	vault   port.CredentialVault
	disco   port.DiscoveryPort
}

// NewWiFiAccess wires the interactor.
func NewWiFiAccess(f port.RouterFactory, v port.CredentialVault, d port.DiscoveryPort) *WiFiAccess {
	return &WiFiAccess{factory: f, vault: v, disco: d}
}

func (uc *WiFiAccess) gateway(ctx context.Context, in MACFilterInput) (port.WiFiAccessControl, error) {
	gw, err := loggedInGateway(ctx, uc.factory, uc.vault, uc.disco, routerTarget(in))
	if err != nil {
		return nil, err
	}
	w, ok := gw.(port.WiFiAccessControl)
	if !ok {
		return nil, fmt.Errorf("%w: adapter %s has no Wi-Fi access control", domain.ErrNotImplemented, gw.Meta().ID)
	}
	return w, nil
}

// Blocked lists the MACs refused by the Wi-Fi access control.
func (uc *WiFiAccess) Blocked(ctx context.Context, in MACFilterInput) ([]domain.MAC, error) {
	w, err := uc.gateway(ctx, in)
	if err != nil {
		return nil, err
	}
	return w.WiFiBlockedMACs(ctx)
}

// Set blocks (true) or re-allows (false) mac on the Wi-Fi.
func (uc *WiFiAccess) Set(ctx context.Context, in MACFilterInput, mac domain.MAC, blocked bool) error {
	if mac.IsZero() {
		return domain.ErrInvalidMAC
	}
	w, err := uc.gateway(ctx, in)
	if err != nil {
		return err
	}
	if blocked {
		return w.WiFiBlock(ctx, mac)
	}
	return w.WiFiUnblock(ctx, mac)
}
