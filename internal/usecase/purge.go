package usecase

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// PurgeEvery is how often the stale rules are purged (RFC-007 D6).
const PurgeEvery = 24 * time.Hour

// PurgeStaleRules keeps the box's deny lists from filling up (RFC-007 D6,
// decisions Q4 and G3), on the device that holds the inventory (the phone's
// core, or the relay):
//
//   - a firewall rule kergui wrote goes when its device is in the inventory
//     and has been away domain.StaleRuleAge;
//   - a Wi-Fi refusal kergui wrote (`<mac> APn`) goes the same way, on every
//     access point, when the box model's wifi_unblock is Verified (#103);
//     otherwise it is only listed;
//   - a Wi-Fi refusal kergui wrote, of a device seen in that time, gets the
//     firewall rule it lacks (« Couper internet », verified on the box).
//
// A MAC the inventory does not know is never removed: a reinstalled app has
// an empty inventory and must not unblock everyone. A device on the box right
// now is not away. A rule the household made in the box's page is never
// touched, and neither is any rule of a MAC the household has a rule for, in
// either list. The household removes those by hand (« liste pleine »).
type PurgeStaleRules struct {
	factory port.RouterFactory
	vault   port.CredentialVault
	disco   port.DiscoveryPort
	devices port.DeviceRepository
	store   port.RulePurgeRepository
	clock   port.Clock
	log     port.Logger

	// last is the pacing when there is no store: per box, in memory.
	mu   sync.Mutex
	last map[string]time.Time
}

// NewPurgeStaleRules wires the interactor. store may be nil: the pacing is
// then kept in memory.
func NewPurgeStaleRules(f port.RouterFactory, v port.CredentialVault, d port.DiscoveryPort, devices port.DeviceRepository,
	store port.RulePurgeRepository, c port.Clock, l port.Logger) *PurgeStaleRules {
	return &PurgeStaleRules{factory: f, vault: v, disco: d, devices: devices, store: store, clock: c, log: l, last: map[string]time.Time{}}
}

// PurgeReport says what one purge did.
type PurgeReport struct {
	// Removed are the firewall rules removed: devices away 30 days.
	Removed []domain.MAC
	// Added are the firewall rules added to Wi-Fi refusals of devices seen
	// in the last 30 days.
	Added []domain.MAC
	// RemovedWiFi are the devices away 30 days whose Wi-Fi refusals were
	// removed, on every access point.
	RemovedWiFi []domain.MAC
	// StaleWiFi are the Wi-Fi refusals of devices away 30 days kept on the
	// box: the model's wifi_unblock is not verified, the removal failed, or
	// the purge ran right before a block (it frees no room for it).
	StaleWiFi []domain.MAC
}

// Run purges now.
func (uc *PurgeStaleRules) Run(ctx context.Context, in MACFilterInput) (PurgeReport, error) {
	gw, err := loggedInGateway(ctx, uc.factory, uc.vault, uc.disco, routerTarget(in))
	if err != nil {
		return PurgeReport{}, err
	}
	rep, err := uc.purge(ctx, gw, RouterID(in.BaseURL), false)
	if err == nil {
		uc.saveLast(ctx, RouterID(in.BaseURL), uc.clock.Now())
	}
	return rep, err
}

// RunIfDue purges when the last purge of this box is PurgeEvery old or more.
// ran is false when it was not due.
func (uc *PurgeStaleRules) RunIfDue(ctx context.Context, in MACFilterInput) (rep PurgeReport, ran bool, err error) {
	last := uc.lastPurge(ctx, RouterID(in.BaseURL))
	if !last.IsZero() && uc.clock.Now().Sub(last) < PurgeEvery {
		return PurgeReport{}, false, nil
	}
	rep, err = uc.Run(ctx, in)
	return rep, true, err
}

func (uc *PurgeStaleRules) lastPurge(ctx context.Context, routerID string) time.Time {
	if uc.store != nil {
		at, err := uc.store.LastRulePurge(ctx, routerID)
		if err != nil {
			uc.log.Warnf("purge: cannot read the last purge date: %v", err)
		}
		return at
	}
	uc.mu.Lock()
	defer uc.mu.Unlock()
	return uc.last[routerID]
}

func (uc *PurgeStaleRules) saveLast(ctx context.Context, routerID string, at time.Time) {
	if uc.store != nil {
		if err := uc.store.SaveRulePurge(ctx, routerID, at); err != nil {
			uc.log.Warnf("purge: cannot record the purge date: %v", err)
		}
		return
	}
	uc.mu.Lock()
	uc.last[routerID] = at
	uc.mu.Unlock()
}

// BlockedDevice is one device the box's deny lists hold, with what the
// inventory knows of it: the « liste pleine » screen offers these for removal
// by hand (RFC-007 D6, Q4).
type BlockedDevice struct {
	MAC domain.MAC
	// Name is the inventory's name for it, "" when it does not know it.
	Name string
	// LastSeen is zero when the inventory does not know it.
	LastSeen time.Time
	// Present: on the box right now.
	Present bool
	// Firewall: a firewall rule (« Couper internet »). WiFi: a Wi-Fi
	// refusal.
	Firewall, WiFi bool
	// ByKergui: every rule for it was written by kergui.
	ByKergui bool
}

// Blocked lists every device the box's deny lists hold, by MAC. It only reads.
func (uc *PurgeStaleRules) Blocked(ctx context.Context, in MACFilterInput) ([]BlockedDevice, error) {
	gw, err := loggedInGateway(ctx, uc.factory, uc.vault, uc.disco, routerTarget(in))
	if err != nil {
		return nil, err
	}
	present, err := gw.Devices(ctx)
	if err != nil {
		return nil, err
	}
	here := map[domain.MAC]bool{}
	for _, d := range present {
		here[d.MAC] = true
	}
	firewall, err := gw.AccessRules(ctx)
	if err != nil {
		return nil, err
	}
	var wifi []domain.AccessRule
	if reader, ok := gw.(port.WiFiRuleReader); ok {
		wifi, err = reader.WiFiRules(ctx)
		if err != nil && !errors.Is(err, domain.ErrNotImplemented) {
			return nil, err
		}
	}
	by := map[domain.MAC]*BlockedDevice{}
	household := map[domain.MAC]bool{}
	add := func(rules []domain.AccessRule, fw bool) {
		for _, g := range groupRules(rules) {
			b, ok := by[g.mac]
			if !ok {
				b = &BlockedDevice{MAC: g.mac, Present: here[g.mac]}
				by[g.mac] = b
			}
			if fw {
				b.Firewall = true
			} else {
				b.WiFi = true
			}
			household[g.mac] = household[g.mac] || g.byHousehold
		}
	}
	add(firewall, true)
	add(wifi, false)
	routerID := RouterID(in.BaseURL)
	out := make([]BlockedDevice, 0, len(by))
	for mac, b := range by {
		b.ByKergui = !household[mac]
		if uc.devices != nil {
			if d, ok, err := uc.devices.FindByMAC(ctx, routerID, mac); err == nil && ok {
				b.LastSeen = d.LastSeen
				if d.CustomName != "" || d.Hostname != "" {
					b.Name = d.DisplayName()
				}
			}
		}
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MAC.String() < out[j].MAC.String() })
	return out, nil
}

// macRules is what the box holds for one MAC.
type macRules struct {
	mac         domain.MAC
	byKergui    bool // a rule kergui wrote
	byHousehold bool // a rule the household wrote
}

// purge runs on a logged-in gateway. removeOnly skips the firewall rules
// added to Wi-Fi refusals: before a block that did not fit, the room freed is
// for that block.
func (uc *PurgeStaleRules) purge(ctx context.Context, gw port.RouterPort, routerID string, removeOnly bool) (PurgeReport, error) {
	var rep PurgeReport
	now := uc.clock.Now()
	inventory := map[domain.MAC]domain.Device{}
	if uc.devices != nil {
		devs, err := uc.devices.ListByRouter(ctx, routerID)
		if err != nil {
			return rep, err
		}
		for _, d := range devs {
			inventory[d.MAC] = d
		}
	}
	// A device on the box right now is not away, whatever an inventory the
	// app has not refreshed says.
	present, err := gw.Devices(ctx)
	if err != nil {
		return rep, err
	}
	for _, d := range present {
		inv := inventory[d.MAC]
		inv.MAC, inv.LastSeen = d.MAC, now
		if inv.Hostname == "" {
			inv.Hostname = d.Hostname
		}
		inventory[d.MAC] = inv
	}
	// away: in the inventory, and absent 30 days. recent: in the inventory,
	// seen in the last 30 days. A MAC it does not know is neither.
	away := func(m domain.MAC) bool {
		d, ok := inventory[m]
		return ok && domain.AbsentLongEnough(d.LastSeen, now)
	}
	recent := func(m domain.MAC) bool {
		d, ok := inventory[m]
		return ok && !d.LastSeen.IsZero() && !domain.AbsentLongEnough(d.LastSeen, now)
	}
	name := func(m domain.MAC) string {
		if d, ok := inventory[m]; ok {
			return d.DisplayName()
		}
		return m.String()
	}

	rules, err := gw.AccessRules(ctx)
	if err != nil {
		return rep, err
	}
	firewall := groupRules(rules)
	var wifi []macRules
	reader, hasWiFi := gw.(port.WiFiRuleReader)
	if hasWiFi {
		wifiRules, err := reader.WiFiRules(ctx)
		switch {
		case errors.Is(err, domain.ErrNotImplemented):
			hasWiFi = false
		case err != nil:
			return rep, err
		default:
			wifi = groupRules(wifiRules)
		}
	}
	// A MAC the household has a rule for, in either list, is theirs: the
	// purge leaves all of its rules (wifi_unblock removes every rule of a
	// MAC, the household's too).
	household := map[domain.MAC]bool{}
	for _, r := range append(slices.Clone(firewall), wifi...) {
		if r.byHousehold {
			household[r.mac] = true
		}
	}

	for _, r := range firewall {
		if !r.byKergui || household[r.mac] || !away(r.mac) {
			continue
		}
		if err := gw.Unblock(ctx, r.mac); err != nil {
			uc.log.Warnf("Règle de %s pas retirée : %v", name(r.mac), err)
			continue
		}
		uc.log.Infof("Règle retirée : appareil absent depuis 30 jours (%s)", name(r.mac))
		rep.Removed = append(rep.Removed, r.mac)
	}
	if !hasWiFi {
		return rep, nil
	}

	blocked := map[domain.MAC]bool{}
	for _, r := range firewall {
		blocked[r.mac] = true
	}
	for _, m := range rep.Removed {
		blocked[m] = false
	}
	unblocker, canUnblock := gw.(port.WiFiAccessControl)
	canUnblock = canUnblock && endpointVerified(gw.Meta(), "wifi_unblock")
	full := false
	for _, r := range wifi {
		switch {
		case !r.byKergui || household[r.mac]:
			// The household's own refusal: theirs to keep or remove.
		case away(r.mac) && removeOnly:
			// Right before a firewall block: removing it frees no room
			// for that block. The daily purge does it.
			rep.StaleWiFi = append(rep.StaleWiFi, r.mac)
		case away(r.mac) && !canUnblock:
			uc.log.Infof("Refus Wi-Fi gardé pour l'instant : %s est absent depuis 30 jours", name(r.mac))
			rep.StaleWiFi = append(rep.StaleWiFi, r.mac)
		case away(r.mac):
			// WiFiUnblock reads the table again before each delete: the box
			// renumbers its rules after one, or leaves holes (RFC-007 R6).
			if err := unblocker.WiFiUnblock(ctx, r.mac); err != nil {
				uc.log.Warnf("Refus Wi-Fi de %s pas retiré : %v", name(r.mac), err)
				rep.StaleWiFi = append(rep.StaleWiFi, r.mac)
				continue
			}
			uc.log.Infof("Règle retirée : appareil absent depuis 30 jours (%s)", name(r.mac))
			rep.RemovedWiFi = append(rep.RemovedWiFi, r.mac)
		case recent(r.mac) && !blocked[r.mac] && !removeOnly && !full:
			err := gw.Block(ctx, r.mac)
			if errors.Is(err, domain.ErrAccessListFull) {
				uc.log.Warnf("La liste de la box est pleine : internet n'a pas pu être coupé pour %s", name(r.mac))
				full = true
				continue
			}
			if err != nil {
				uc.log.Warnf("Internet pas coupé pour %s : %v", name(r.mac), err)
				continue
			}
			uc.log.Infof("Internet coupé aussi pour %s, déjà refusé au Wi-Fi", name(r.mac))
			rep.Added = append(rep.Added, r.mac)
		}
	}
	if len(rep.Added) > 0 {
		if err := ensureFilterOn(ctx, gw, uc.log); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// endpointVerified says whether the box model documents op as validated on a
// real box. An operation it does not document is not.
func endpointVerified(meta domain.AdapterMeta, op string) bool {
	for _, e := range meta.Endpoints {
		if e.Operation == op {
			return e.Verified
		}
	}
	return false
}

// groupRules folds rules per MAC (a Wi-Fi refusal is one rule per access
// point), in the order of the MACs, so a purge reads the same every time.
func groupRules(rules []domain.AccessRule) []macRules {
	by := map[domain.MAC]*macRules{}
	for _, r := range rules {
		if r.Mode != domain.AccessBlock || r.MAC.IsZero() {
			continue
		}
		g, ok := by[r.MAC]
		if !ok {
			g = &macRules{mac: r.MAC}
			by[r.MAC] = g
		}
		if r.ByKergui {
			g.byKergui = true
		} else {
			g.byHousehold = true
		}
	}
	out := make([]macRules, 0, len(by))
	for _, g := range by {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].mac.String() < out[j].mac.String() })
	return out
}
