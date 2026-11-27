package usecase

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Wi-Fi refusals while the Wi-Fi is open (RFC-007 D4 « open, with Wi-Fi
// refusals », D5 « Le déconnecter aussi du Wi-Fi »).
//
// On a box with an allow list (port.WiFiAllowList) a refusal is written by
// converging the access points to Ban with the whole deny list
// (ApplyWiFiLock), never by adding one rule and switching to Ban
// (WiFiBlock): an access point that was closed and is open again keeps its
// allow list in Disabled, under the same rule names `<mac> APn`, and Ban
// would read every authorisation as a refusal (the trap by way of
// Disabled, #105, #106).
//
// A refusal the box answers « plein » first purges the stale Wi-Fi
// refusals, then is tried once more (RFC-007 D6), as a firewall block is.

// banRefusals lists the devices refused on the access points aps that are in
// Ban, by MAC: on an AP in Disabled or Allow, a rule is an authorisation.
func banRefusals(box domain.WiFiLockBox, aps []string) []domain.MAC {
	set := map[string]domain.MAC{}
	for _, id := range aps {
		ap, ok := box.AP(id)
		if !ok || ap.Mode != domain.WiFiModeBan {
			continue
		}
		for _, m := range box.Rules[id] {
			set[m.String()] = m
		}
	}
	return sortedMACs(set)
}

// enabledAPs are the ids of the access points the box lists.
func enabledAPs(box domain.WiFiLockBox) []string {
	out := make([]string, 0, len(box.APs))
	for _, ap := range box.APs {
		out = append(out, ap.ID)
	}
	return out
}

// macSet is a set of MACs by their canonical form.
type macSet map[string]domain.MAC

func newMACSet(lists ...[]domain.MAC) macSet {
	s := macSet{}
	for _, l := range lists {
		for _, m := range l {
			s.add(m)
		}
	}
	return s
}

func (s macSet) add(m domain.MAC) {
	if !m.IsZero() {
		s[m.String()] = m
	}
}

func (s macSet) drop(m domain.MAC) { delete(s, m.String()) }

func (s macSet) has(m domain.MAC) bool {
	_, ok := s[m.String()]
	return ok
}

func (s macSet) list() []domain.MAC { return sortedMACs(s) }

func sortedMACs(set map[string]domain.MAC) []domain.MAC {
	out := make([]domain.MAC, 0, len(set))
	for _, m := range set {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// applyWiFiRefusals makes the access points aps (nil: every one the box
// lists) refuse exactly the devices deny gives for the box as read now,
// never the devices of spare. It refuses to touch an access point in Allow:
// the Wi-Fi is closed there, and a refusal is not written on the box then.
//
//   - Rules on an AP in Disabled are its allow list. Any of them a refusal
//     would keep goes first, in Disabled, so that no rule ever changes
//     meaning when the AP goes to Ban.
//   - Nothing left to refuse: the APs in Ban go back to Disabled, with no
//     rule; the others are left alone.
func applyWiFiRefusals(ctx context.Context, al port.WiFiAllowList, aps []string, deny func(domain.WiFiLockBox) []domain.MAC, spare []domain.MAC) error {
	box, err := al.WiFiLockBox(ctx)
	if err != nil {
		return err
	}
	if aps == nil {
		aps = enabledAPs(box)
	}
	var on, disabled, ban []string
	for _, id := range aps {
		ap, ok := box.AP(id)
		if !ok {
			continue // switched off since: nothing to refuse there
		}
		switch ap.Mode {
		case domain.WiFiModeAllow:
			return fmt.Errorf("%w: le Wi-Fi est fermé sur %s, un refus ne s'y écrit pas", domain.ErrBlockFailed, apLabel(ap))
		case domain.WiFiModeBan:
			ban = append(ban, id)
		default:
			disabled = append(disabled, id)
		}
		on = append(on, id)
	}
	if len(on) == 0 {
		return fmt.Errorf("%w: no Wi-Fi access point to refuse a device on", domain.ErrUnexpectedResponse)
	}
	refused := newMACSet(deny(box))
	for _, m := range spare {
		refused.drop(m)
	}

	// The allow list kept on an open AP: the rules of the devices to refuse
	// go first, so none is read the other way.
	kept, turned := macSet{}, false
	for _, id := range disabled {
		for _, m := range box.Rules[id] {
			if refused.has(m) {
				turned = true
				continue
			}
			kept.add(m)
		}
	}
	if turned {
		if err := al.ApplyWiFiLock(ctx, domain.WiFiLockTarget{APs: disabled, Mode: domain.WiFiModeDisabled, MACs: kept.list()}); err != nil {
			return err
		}
	}
	if len(refused) == 0 {
		if len(ban) == 0 {
			return nil
		}
		return al.ApplyWiFiLock(ctx, domain.WiFiLockTarget{APs: ban, Mode: domain.WiFiModeDisabled})
	}
	return al.ApplyWiFiLock(ctx, domain.WiFiLockTarget{APs: on, Mode: domain.WiFiModeBan, MACs: refused.list()})
}

// refuseWithPurge runs try, a Wi-Fi refusal; when the box answers its table
// is full, it removes the stale Wi-Fi refusals (purge, RFC-007 D6), tells
// forgotten which devices lost theirs, and tries once more (#106). purge may
// be nil: the full answer is then returned as is.
func refuseWithPurge(ctx context.Context, gw port.RouterPort, baseURL string, mac domain.MAC, purge *PurgeStaleRules,
	log port.Logger, forgotten func([]domain.MAC), try func() error) error {
	err := try()
	if errors.Is(err, domain.ErrAccessListFull) && purge != nil {
		rep, perr := purge.purge(ctx, gw, baseURL, purgeForWiFi)
		if perr != nil {
			log.Warnf("purge before the Wi-Fi refusal of %s: %v", mac, perr)
		}
		if len(rep.RemovedWiFi) > 0 {
			if forgotten != nil {
				forgotten(rep.RemovedWiFi)
			}
			err = try()
		}
	}
	if errors.Is(err, domain.ErrAccessListFull) {
		log.Warnf("La liste Wi-Fi de la box est pleine : %s n'a pas pu être déconnecté du Wi-Fi", mac)
	}
	return err
}

// apLabel names an access point for the journal: its broadcast name, else
// its id.
func apLabel(ap domain.WiFiLockAP) string {
	if ap.Name != "" {
		return ap.Name
	}
	return ap.ID
}
