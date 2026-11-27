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
	self    port.SelfLocator // nil: no device to spare (selfguard.go)
}

// NewWiFiAccess wires the interactor.
func NewWiFiAccess(f port.RouterFactory, v port.CredentialVault, d port.DiscoveryPort) *WiFiAccess {
	return &WiFiAccess{factory: f, vault: v, disco: d}
}

// WithSelf makes the interactor spare the device running Kergui, found at
// the address self gives (selfguard.go): its Wi-Fi access is never cut, and
// the network it is on never turned off. It returns uc.
func (uc *WiFiAccess) WithSelf(self port.SelfLocator) *WiFiAccess {
	uc.self = self
	return uc
}

func (uc *WiFiAccess) gateway(ctx context.Context, in MACFilterInput) (port.RouterPort, port.WiFiAccessControl, error) {
	gw, err := loggedInGateway(ctx, uc.factory, uc.vault, uc.disco, routerTarget(in))
	if err != nil {
		return nil, nil, err
	}
	w, ok := gw.(port.WiFiAccessControl)
	if !ok {
		return nil, nil, fmt.Errorf("%w: adapter %s has no Wi-Fi access control", domain.ErrNotImplemented, gw.Meta().ID)
	}
	return gw, w, nil
}

// Blocked lists the MACs refused by the Wi-Fi access control.
func (uc *WiFiAccess) Blocked(ctx context.Context, in MACFilterInput) ([]domain.MAC, error) {
	_, w, err := uc.gateway(ctx, in)
	if err != nil {
		return nil, err
	}
	return w.WiFiBlockedMACs(ctx)
}

// Set blocks (true) or re-allows (false) mac on the Wi-Fi. It never blocks
// the device running Kergui.
func (uc *WiFiAccess) Set(ctx context.Context, in MACFilterInput, mac domain.MAC, blocked bool) error {
	if mac.IsZero() {
		return domain.ErrInvalidMAC
	}
	gw, w, err := uc.gateway(ctx, in)
	if err != nil {
		return err
	}
	if blocked {
		if err := refuseSelf(ctx, uc.self, gw, in.BaseURL, mac); err != nil {
			return err
		}
		return w.WiFiBlock(ctx, mac)
	}
	return w.WiFiUnblock(ctx, mac)
}

func (uc *WiFiAccess) ssidManager(ctx context.Context, in MACFilterInput) (port.RouterPort, port.WiFiSSIDManager, error) {
	gw, err := loggedInGateway(ctx, uc.factory, uc.vault, uc.disco, routerTarget(in))
	if err != nil {
		return nil, nil, err
	}
	m, ok := gw.(port.WiFiSSIDManager)
	if !ok {
		return nil, nil, fmt.Errorf("%w: adapter %s cannot manage Wi-Fi networks", domain.ErrNotImplemented, gw.Meta().ID)
	}
	return gw, m, nil
}

// SSIDs lists the router's Wi-Fi networks, enabled or not.
func (uc *WiFiAccess) SSIDs(ctx context.Context, in MACFilterInput) ([]domain.WiFiSSID, error) {
	_, m, err := uc.ssidManager(ctx, in)
	if err != nil {
		return nil, err
	}
	return m.WiFiSSIDs(ctx)
}

// SetSSID switches one Wi-Fi network on or off and/or changes its name,
// password or visibility. It refuses to switch off the last enabled network,
// which would leave every wireless device (and maybe the admin) offline, and
// the network the device running Kergui is on (domain.SelfLockoutError).
func (uc *WiFiAccess) SetSSID(ctx context.Context, in MACFilterInput, id string, change domain.WiFiSSIDChange) error {
	if id == "" {
		return fmt.Errorf("%w: réseau Wi-Fi non précisé", domain.ErrInvalidInput)
	}
	if err := change.Validate(); err != nil {
		return err
	}
	gw, m, err := uc.ssidManager(ctx, in)
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
	if change.Enabled != nil && !*change.Enabled && target.Enabled {
		// The device running Kergui first: its sentence says why it matters.
		me, ok, err := selfDevice(ctx, uc.self, gw, in.BaseURL)
		if err != nil {
			return err
		}
		if ok && onNetwork(me.SSID, *target) {
			return &domain.SelfLockoutError{Reason: domain.SelfLockoutNetwork, Target: ssidLabel(*target)}
		}
		if enabled <= 1 {
			return &domain.SelfLockoutError{Reason: domain.SelfLockoutLastNetwork, Target: ssidLabel(*target)}
		}
	}
	return m.SetWiFiSSID(ctx, id, change)
}

// ssidLabel is how a refusal names a network: its broadcast name, else the
// box's label for it.
func ssidLabel(s domain.WiFiSSID) string {
	if s.Name != "" {
		return s.Name
	}
	return s.Alias
}
