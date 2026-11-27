package usecase

import (
	"context"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// The self guard (UX audit 2026-11-26, C1; #113): the core never cuts the
// device running Kergui off the box. On a phone that is the phone itself; on
// a relay, the relay. « Activer » and « Rendre l'accès » are free forever,
// but an app that lost the box cannot run them: the household would need a
// cable or the box's own page. The guard lives in the use cases, so the app,
// the relay (which runs the owner's commands on the same use cases) and
// WhatsApp's « 1 » (a relay command too) share it.
//
// The device is known by its address on the box (port.SelfLocator), not by
// its MAC: Android gives each Wi-Fi a private, random one.

// selfDevice is the device of the box's list at the address self gives, if
// any: none when self is nil, when it cannot tell (off the box's Wi-Fi), or
// when the box does not list that address.
func selfDevice(ctx context.Context, self port.SelfLocator, gw port.RouterPort, baseURL string) (domain.Device, bool, error) {
	if self == nil {
		return domain.Device{}, false, nil
	}
	ip := self.SelfIP(ctx, baseURL)
	if ip.IsZero() {
		return domain.Device{}, false, nil
	}
	devices, err := gw.Devices(ctx)
	if err != nil {
		return domain.Device{}, false, err
	}
	for _, d := range devices {
		if !d.MAC.IsZero() && d.IP == ip {
			return d, true, nil
		}
	}
	return domain.Device{}, false, nil
}

// refuseSelf refuses a write that cuts mac when mac is the device running
// Kergui.
func refuseSelf(ctx context.Context, self port.SelfLocator, gw port.RouterPort, baseURL string, mac domain.MAC) error {
	me, ok, err := selfDevice(ctx, self, gw, baseURL)
	if err != nil {
		return err
	}
	if ok && me.MAC == mac {
		return &domain.SelfLockoutError{Reason: domain.SelfLockoutDevice, Target: mac.String()}
	}
	return nil
}

// onNetwork says whether a device whose list entry names ssid is on the
// network s. The box gives a device its network's broadcast name; the
// alias and id are matched too, for a model that would give those.
func onNetwork(ssid string, s domain.WiFiSSID) bool {
	ssid = strings.TrimSpace(ssid)
	if ssid == "" {
		return false
	}
	for _, v := range []string{s.Name, s.Alias, s.ID} {
		if v != "" && strings.EqualFold(ssid, v) {
			return true
		}
	}
	return false
}
