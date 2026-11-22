package usecase

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// AutoReject refuses, at the Wi-Fi access control, the devices nobody
// accepted within domain.AutoRejectDelay of joining the box (RFC-006, Max).
// The household turns it on from the app; whoever polls the box (the phone's
// watch service, the app itself) calls Enforce on each round. It does not
// check the tier: its caller does, so that a lapsed pass stops it.
type AutoReject struct {
	factory port.RouterFactory
	vault   port.CredentialVault
	disco   port.DiscoveryPort
	devices port.DeviceRepository
	store   port.AutoRejectRepository
	self    port.SelfLocator
	clock   port.Clock
	log     port.Logger
}

// NewAutoReject wires the interactor. self may be nil (no phone to spare).
func NewAutoReject(f port.RouterFactory, v port.CredentialVault, d port.DiscoveryPort, devices port.DeviceRepository,
	store port.AutoRejectRepository, self port.SelfLocator, c port.Clock, l port.Logger) *AutoReject {
	return &AutoReject{factory: f, vault: v, disco: d, devices: devices, store: store, self: self, clock: c, log: l}
}

// AutoRejectStatus is the setting and the devices it tracks, by MAC.
type AutoRejectStatus struct {
	Settings domain.AutoRejectSettings
	Devices  []domain.AutoRejectDevice
}

// AutoRejectRun is the outcome of one Enforce round.
type AutoRejectRun struct {
	Enabled bool
	// Refused are the devices refused in this round, with their names, for
	// the notification that opens their sheet.
	Refused []domain.Device
	// Pending is how many devices are still waiting to be accepted.
	Pending int
}

// Status returns the setting and the tracked devices. It never reaches the box.
func (uc *AutoReject) Status(ctx context.Context, baseURL string) (AutoRejectStatus, error) {
	routerID := RouterID(baseURL)
	s, err := uc.store.AutoRejectSettings(ctx, routerID)
	if err != nil {
		return AutoRejectStatus{}, err
	}
	tracked, err := uc.store.AutoRejectDevices(ctx, routerID)
	if err != nil {
		return AutoRejectStatus{}, err
	}
	out := AutoRejectStatus{Settings: s, Devices: make([]domain.AutoRejectDevice, 0, len(tracked))}
	for _, d := range tracked {
		out.Devices = append(out.Devices, d)
	}
	sort.Slice(out.Devices, func(i, j int) bool { return out.Devices[i].MAC.String() < out.Devices[j].MAC.String() })
	return out, nil
}

func (uc *AutoReject) gateway(ctx context.Context, in MACFilterInput) (port.RouterPort, port.WiFiAccessControl, error) {
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

// Enable turns the rule on: every device on the box now is accepted ("tout
// accepter"), and only later arrivals have to be. Enabling it again accepts
// whatever is there by then.
func (uc *AutoReject) Enable(ctx context.Context, in MACFilterInput) (AutoRejectStatus, error) {
	gw, _, err := uc.gateway(ctx, in)
	if err != nil {
		return AutoRejectStatus{}, err
	}
	present, err := gw.Devices(ctx)
	if err != nil {
		return AutoRejectStatus{}, err
	}
	routerID := RouterID(in.BaseURL)
	now := uc.clock.Now()
	if err := uc.store.ClearAutoRejectDevices(ctx, routerID); err != nil {
		return AutoRejectStatus{}, err
	}
	for _, d := range present {
		if d.MAC.IsZero() {
			continue
		}
		if err := uc.store.SaveAutoRejectDevice(ctx, routerID, domain.AutoRejectDevice{
			MAC: d.MAC, FirstSeen: now, State: domain.AutoRejectAccepted, DecidedAt: now,
		}); err != nil {
			return AutoRejectStatus{}, err
		}
	}
	if err := uc.store.SaveAutoRejectSettings(ctx, routerID, domain.AutoRejectSettings{Enabled: true, Since: now}); err != nil {
		return AutoRejectStatus{}, err
	}
	uc.log.Infof("auto : rejet automatique activé, %d appareil(s) présent(s) accepté(s)", len(present))
	return uc.Status(ctx, in.BaseURL)
}

// Disable turns the rule off and forgets the devices it tracked. What it
// already refused stays refused on the box, and can be restored for free.
func (uc *AutoReject) Disable(ctx context.Context, baseURL string) error {
	routerID := RouterID(baseURL)
	if err := uc.store.SaveAutoRejectSettings(ctx, routerID, domain.AutoRejectSettings{}); err != nil {
		return err
	}
	if err := uc.store.ClearAutoRejectDevices(ctx, routerID); err != nil {
		return err
	}
	uc.log.Infof("auto : rejet automatique désactivé")
	return nil
}

// Accept accepts a device for good: approved from its notification, renamed,
// or given its access back. A rule that is off has nothing to remember.
func (uc *AutoReject) Accept(ctx context.Context, baseURL string, mac domain.MAC) error {
	if mac.IsZero() {
		return domain.ErrInvalidMAC
	}
	routerID := RouterID(baseURL)
	s, err := uc.store.AutoRejectSettings(ctx, routerID)
	if err != nil || !s.Enabled {
		return err
	}
	tracked, err := uc.store.AutoRejectDevices(ctx, routerID)
	if err != nil {
		return err
	}
	now := uc.clock.Now()
	d, ok := tracked[mac]
	if ok && d.State == domain.AutoRejectAccepted {
		return nil
	}
	if !ok {
		d = domain.AutoRejectDevice{MAC: mac, FirstSeen: now}
	}
	d.State, d.DecidedAt = domain.AutoRejectAccepted, now
	if err := uc.store.SaveAutoRejectDevice(ctx, routerID, d); err != nil {
		return err
	}
	uc.log.Infof("auto : %s accepté", mac)
	return nil
}

// Enforce runs one round: it starts the delay of the devices it sees for the
// first time, accepts the renamed ones and the phone running the app, and
// refuses at the Wi-Fi access control those whose delay is over. A device
// is refused once; a refusal the box did not take is tried again next round.
func (uc *AutoReject) Enforce(ctx context.Context, in MACFilterInput) (AutoRejectRun, error) {
	routerID := RouterID(in.BaseURL)
	s, err := uc.store.AutoRejectSettings(ctx, routerID)
	if err != nil || !s.Enabled {
		return AutoRejectRun{}, err
	}
	gw, acl, err := uc.gateway(ctx, in)
	if err != nil {
		return AutoRejectRun{Enabled: true}, err
	}
	present, err := gw.Devices(ctx)
	if err != nil {
		return AutoRejectRun{Enabled: true}, err
	}
	tracked, err := uc.store.AutoRejectDevices(ctx, routerID)
	if err != nil {
		return AutoRejectRun{Enabled: true}, err
	}
	var self domain.IP
	if uc.self != nil {
		self = uc.self.SelfIP(ctx, in.BaseURL)
	}

	now := uc.clock.Now()
	run := AutoRejectRun{Enabled: true}
	for _, dev := range present {
		if dev.MAC.IsZero() {
			continue
		}
		t, seen := tracked[dev.MAC]
		if seen && t.State != domain.AutoRejectPending {
			continue // accepted, or already refused: never twice
		}
		if !seen {
			t = domain.AutoRejectDevice{MAC: dev.MAC, FirstSeen: now, State: domain.AutoRejectPending}
		}
		if dev.CustomName == "" && uc.devices != nil {
			if known, ok, err := uc.devices.FindByMAC(ctx, routerID, dev.MAC); err == nil && ok {
				dev.CustomName = known.CustomName
			}
		}
		switch {
		case !self.IsZero() && dev.IP == self:
			// The phone running the app: refusing it would cut the household
			// off the box with their own rule.
			t.State, t.DecidedAt = domain.AutoRejectAccepted, now
		case dev.CustomName != "":
			// Named by the household: they know it.
			t.State, t.DecidedAt = domain.AutoRejectAccepted, now
		case t.Due(now):
			if err := acl.WiFiBlock(ctx, dev.MAC); err != nil {
				uc.log.Warnf("auto : refus de %s (%s) impossible : %v", dev.DisplayName(), dev.MAC, err)
				run.Pending++
				if !seen {
					_ = uc.store.SaveAutoRejectDevice(ctx, routerID, t)
				}
				continue
			}
			t.State, t.DecidedAt = domain.AutoRejectRefused, now
			uc.log.Infof("auto : non accepté, %s (%s) refusé au Wi-Fi", dev.DisplayName(), dev.MAC)
			run.Refused = append(run.Refused, dev)
		default:
			run.Pending++
			if seen {
				continue // nothing changed
			}
		}
		if err := uc.store.SaveAutoRejectDevice(ctx, routerID, t); err != nil {
			return run, err
		}
	}
	return run, nil
}

// AutoRejectPolicy is the rule as the owner's phone set it, carried to the
// household's relay agent (RFC-006, #36): on or off, since when, and the
// devices the household accepted on the phone.
type AutoRejectPolicy struct {
	Enabled  bool
	Since    time.Time
	Accepted []domain.MAC
}

// Adopt makes the stored rule the one the owner set, without reaching the
// box. It is the relay's counterpart of Enable: the relay does not accept
// what happens to be on the box when it starts — a stranger pending on the
// phone would be let in that way — but the phone's own accepted list.
//
// The same rule adopted again (same Since) keeps what the relay tracked: a
// pending device keeps its clock and a refused one stays refused, unless the
// owner has since accepted it. A rule turned on again (a new Since) starts
// over from the owner's list.
func (uc *AutoReject) Adopt(ctx context.Context, baseURL string, p AutoRejectPolicy) error {
	routerID := RouterID(baseURL)
	cur, err := uc.store.AutoRejectSettings(ctx, routerID)
	if err != nil {
		return err
	}
	if !p.Enabled {
		if !cur.Enabled {
			return nil
		}
		return uc.Disable(ctx, baseURL)
	}
	if !cur.Enabled || !cur.Since.Equal(p.Since) {
		if err := uc.store.ClearAutoRejectDevices(ctx, routerID); err != nil {
			return err
		}
		if err := uc.store.SaveAutoRejectSettings(ctx, routerID, domain.AutoRejectSettings{Enabled: true, Since: p.Since}); err != nil {
			return err
		}
		uc.log.Infof("auto : rejet automatique repris du téléphone, %d appareil(s) accepté(s)", len(p.Accepted))
	}
	tracked, err := uc.store.AutoRejectDevices(ctx, routerID)
	if err != nil {
		return err
	}
	now := uc.clock.Now()
	for _, mac := range p.Accepted {
		if mac.IsZero() {
			continue
		}
		d, ok := tracked[mac]
		if ok && d.State == domain.AutoRejectAccepted {
			continue
		}
		if !ok {
			d = domain.AutoRejectDevice{MAC: mac, FirstSeen: now}
		}
		d.State, d.DecidedAt = domain.AutoRejectAccepted, now
		if err := uc.store.SaveAutoRejectDevice(ctx, routerID, d); err != nil {
			return err
		}
	}
	return nil
}

// RelayEnforces reports whether a relay last heard from the hub at last is
// enforcing the rule now, on the rule's own clock: the phone then stands
// down (domain.AutoRejectRelayWindow).
func (uc *AutoReject) RelayEnforces(last time.Time) bool {
	return domain.RelayEnforces(last, uc.clock.Now())
}
