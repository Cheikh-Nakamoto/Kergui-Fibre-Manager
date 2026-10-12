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

func (uc *WiFiAccess) ssidManager(ctx context.Context, in MACFilterInput) (port.WiFiSSIDManager, error) {
	gw, err := loggedInGateway(ctx, uc.factory, uc.vault, uc.disco, routerTarget(in))
	if err != nil {
		return nil, err
	}
	m, ok := gw.(port.WiFiSSIDManager)
	if !ok {
		return nil, fmt.Errorf("%w: adapter %s cannot manage Wi-Fi networks", domain.ErrNotImplemented, gw.Meta().ID)
	}
	return m, nil
}

// SSIDs lists the router's Wi-Fi networks, enabled or not.
func (uc *WiFiAccess) SSIDs(ctx context.Context, in MACFilterInput) ([]domain.WiFiSSID, error) {
	m, err := uc.ssidManager(ctx, in)
	if err != nil {
		return nil, err
	}
	return m.WiFiSSIDs(ctx)
}

// SetSSID switches one Wi-Fi network on or off and/or changes its name,
// password or visibility. It refuses to switch off the last enabled network,
// which would leave every wireless device (and maybe the admin) offline.
func (uc *WiFiAccess) SetSSID(ctx context.Context, in MACFilterInput, id string, change domain.WiFiSSIDChange) error {
	if id == "" {
		return fmt.Errorf("%w: réseau Wi-Fi non précisé", domain.ErrInvalidInput)
	}
	if err := change.Validate(); err != nil {
		return err
	}
	m, err := uc.ssidManager(ctx, in)
	if err != nil {
		return err
	}
	ssids, err := m.WiFiSSIDs(ctx)
	if err != nil {
		return err
	}
	var target *domain.WiFiSSID
	enabled := 0
	for i := range ssids {
		if ssids[i].ID == id {
			target = &ssids[i]
		}
		if ssids[i].Enabled {
			enabled++
		}
	}
	if target == nil {
		return fmt.Errorf("%w: réseau Wi-Fi %s introuvable", domain.ErrInvalidInput, id)
	}
	if target.Locked != "" {
		return fmt.Errorf("%w: %s ne peut pas être modifié : %s", domain.ErrInvalidInput, target.Alias, target.Locked)
	}
	if change.Enabled != nil && !*change.Enabled && target.Enabled && enabled <= 1 {
		return fmt.Errorf("%w: %s est le dernier réseau Wi-Fi actif", domain.ErrInvalidInput, target.Alias)
	}
	return m.SetWiFiSSID(ctx, id, change)
}
