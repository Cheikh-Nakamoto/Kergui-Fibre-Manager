package domain

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The closed Wi-Fi (RFC-007): on a box whose access points can run an allow
// list, only the household's devices may join. This file holds what the box
// says (WiFiLockBox), what kergui wants on the household's access points
// (WiFiLockTarget), the places the shared rule table leaves (D3), each
// device's trust state (D1, D5) and the household's stored intent (D7).
//
// A device's address is "private" when it is locally administered (bit 0x02
// of the first octet, MAC.IsRandomized): it may change, and the phone is then
// refused at home (D10).

// The mode of one access point's access control, as the F6600P names it
// (R10: « No Filter / Liste noire / Liste blanche »).
const (
	// WiFiModeDisabled filters nothing; the rules are kept and ignored.
	WiFiModeDisabled = "Disabled"
	// WiFiModeBan refuses the listed devices.
	WiFiModeBan = "Ban"
	// WiFiModeAllow admits only the listed devices.
	WiFiModeAllow = "Allow"
)

// ValidWiFiMode reports whether mode is one of the three modes.
func ValidWiFiMode(mode string) bool {
	return mode == WiFiModeDisabled || mode == WiFiModeBan || mode == WiFiModeAllow
}

// ErrEmptyAllowList refuses an access point in Allow with no rule. Measured on
// the F6600P (RFC-007 W2): switching to Allow with an empty list has no
// effect, and removing the last rule of an AP already in Allow refuses
// everyone. Neither may be asked of the box.
var ErrEmptyAllowList = errors.New("wifi lock: an access point in Allow needs at least one device")

// WiFiLockAP is one access point as the box reports it.
type WiFiLockAP struct {
	ID   string // router reference, e.g. DEV.WIFI.AP1
	Band string // "2.4GHz", "5GHz", or ""
	Name string // broadcast name
	Mode string // WiFiModeDisabled, WiFiModeBan or WiFiModeAllow
	// Visitors: the household said this network is for its visitors (D2).
	Visitors bool
}

// WiFiLockBox is what the box says of its Wi-Fi access control.
type WiFiLockBox struct {
	APs []WiFiLockAP
	// Rules are the listed devices per access point, whatever its mode: one
	// rule per device and per access point in a table every AP shares.
	Rules map[string][]MAC
	// Limit is the size of the shared table, 0 when the model never said it
	// (64 on the F6600P, W1).
	Limit int
	// Used is how many rules the table holds.
	Used int
}

// AP returns the access point id, and whether the box has it.
func (b WiFiLockBox) AP(id string) (WiFiLockAP, bool) {
	for _, ap := range b.APs {
		if ap.ID == id {
			return ap, true
		}
	}
	return WiFiLockAP{}, false
}

// WiFiPlaces is how many devices an allow list can hold (RFC-007 D3):
// floor((limit − rules on the APs that stay open) / closed APs). Each allowed
// device takes one rule on each closed AP. known is false when the limit is
// unknown (0) or no AP is closed: the app then shows the count with no
// maximum.
func WiFiPlaces(limit, rulesOnOpenAPs, closedAPs int) (places int, known bool) {
	if limit <= 0 || closedAPs <= 0 {
		return 0, false
	}
	free := limit - rulesOnOpenAPs
	if free <= 0 {
		return 0, true
	}
	return free / closedAPs, true
}

// Places is WiFiPlaces for the box with the access points closed closed:
// the rules on every other AP, enabled or not, keep their slots.
func (b WiFiLockBox) Places(closed []string) (places int, known bool) {
	shut := map[string]bool{}
	for _, id := range closed {
		shut[id] = true
	}
	open := 0
	for ap, macs := range b.Rules {
		if !shut[ap] {
			open += len(macs)
		}
	}
	return WiFiPlaces(b.Limit, open, len(shut))
}

// CheckFits refuses, before any write, a target the table cannot hold, with
// an *AccessListFullError counting devices (RFC-007 D4 step 1). A box whose
// limit is unknown is not refused here: the box answers when it is full.
func (b WiFiLockBox) CheckFits(t WiFiLockTarget) error {
	places, known := b.Places(t.APs)
	if !known || len(t.MACs) <= places {
		return nil
	}
	return &AccessListFullError{Kind: AccessListWiFi, Limit: places, Used: len(t.MACs)}
}

// CheckModeSwitch refuses a mode flip the box would get wrong: an access
// point it does not have, or one switched to Allow with no rule (W2). The
// rules are kept as they are, so they must already be there.
func (b WiFiLockBox) CheckModeSwitch(aps []string, mode string) error {
	if !ValidWiFiMode(mode) {
		return fmt.Errorf("%w: %q", ErrInvalidAccessMode, mode)
	}
	if len(aps) == 0 {
		return fmt.Errorf("%w: no access point", ErrInvalidInput)
	}
	for _, id := range aps {
		if _, ok := b.AP(id); !ok {
			return fmt.Errorf("%w: the box has no access point %q", ErrInvalidInput, id)
		}
		if mode == WiFiModeAllow && len(b.Rules[id]) == 0 {
			return fmt.Errorf("%w: %s", ErrEmptyAllowList, id)
		}
	}
	return nil
}

// WiFiLockTarget is what kergui wants on the household's access points: one
// mode for all of them and one list of devices, the allow list in Allow and
// Disabled, the deny list in Ban (RFC-007 D4).
type WiFiLockTarget struct {
	APs  []string
	Mode string
	MACs []MAC
}

// Validate refuses a target the box cannot be asked for: no access point, an
// unknown mode, an unknown or repeated device, and above all Allow with an
// empty list (ErrEmptyAllowList).
func (t WiFiLockTarget) Validate() error {
	if !ValidWiFiMode(t.Mode) {
		return fmt.Errorf("%w: %q", ErrInvalidAccessMode, t.Mode)
	}
	if len(t.APs) == 0 {
		return fmt.Errorf("%w: no access point", ErrInvalidInput)
	}
	aps := map[string]bool{}
	for _, id := range t.APs {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("%w: an access point with no id", ErrInvalidInput)
		}
		if aps[id] {
			return fmt.Errorf("%w: access point %s twice", ErrInvalidInput, id)
		}
		aps[id] = true
	}
	seen := map[string]bool{}
	for _, m := range t.MACs {
		if m.IsZero() {
			return fmt.Errorf("%w: an unknown address in the list", ErrInvalidMAC)
		}
		if seen[m.String()] {
			return fmt.Errorf("%w: %s twice in the list", ErrInvalidInput, m)
		}
		seen[m.String()] = true
	}
	if t.Mode == WiFiModeAllow && len(t.MACs) == 0 {
		return ErrEmptyAllowList
	}
	return nil
}

// WiFiHousehold is who goes in the household's allow list.
type WiFiHousehold struct {
	// Approved are the household's approvals (« Autorisé », D1).
	Approved []MAC
	// Blocked are the blocked devices: never in the list.
	Blocked []MAC
	// Self is this phone, zero when unknown (a close through the relay).
	Self MAC
	// Relays are the household's relays.
	Relays []MAC
	// Owners are the phones marked Owner.
	Owners []MAC
}

// HouseholdAllowList builds the allow-list target of the household APs
// (RFC-007 D4 step 1): the approved devices that are not blocked, plus this
// phone, every relay and every Owner phone, which are always in it, blocked
// or not, and cannot be left out. mode is WiFiModeAllow (closed) or
// WiFiModeDisabled (open, the list kept). With neither this phone nor a
// relay in it, closing would lock the household out of the box's Wi-Fi: it
// answers ErrSelfLockout.
func HouseholdAllowList(aps []string, mode string, h WiFiHousehold) (WiFiLockTarget, error) {
	if mode != WiFiModeAllow && mode != WiFiModeDisabled {
		return WiFiLockTarget{}, fmt.Errorf("%w: %q is not an allow-list mode", ErrInvalidAccessMode, mode)
	}
	if h.Self.IsZero() && !anyKnown(h.Relays) {
		return WiFiLockTarget{}, &SelfLockoutError{Reason: SelfLockoutAllowList}
	}
	blocked := map[string]bool{}
	for _, m := range h.Blocked {
		blocked[m.String()] = true
	}
	set := map[string]MAC{}
	add := func(m MAC, always bool) {
		if m.IsZero() || (!always && blocked[m.String()]) {
			return
		}
		set[m.String()] = m
	}
	add(h.Self, true)
	for _, m := range h.Relays {
		add(m, true)
	}
	for _, m := range h.Owners {
		add(m, true)
	}
	for _, m := range h.Approved {
		add(m, false)
	}
	macs := make([]MAC, 0, len(set))
	for _, m := range set {
		macs = append(macs, m)
	}
	sort.Slice(macs, func(i, j int) bool { return macs[i].String() < macs[j].String() })
	t := WiFiLockTarget{APs: append([]string(nil), aps...), Mode: mode, MACs: macs}
	if err := t.Validate(); err != nil {
		return WiFiLockTarget{}, err
	}
	return t, nil
}

func anyKnown(macs []MAC) bool {
	for _, m := range macs {
		if !m.IsZero() {
			return true
		}
	}
	return false
}

// DeviceTrust is a device's state for the household (RFC-007 D1).
type DeviceTrust string

const (
	// TrustAuthorised: « Autorisé », may always join (the approval).
	TrustAuthorised DeviceTrust = "authorised"
	// TrustToCheck: « À vérifier », neither authorised nor blocked.
	TrustToCheck DeviceTrust = "to_check"
	// TrustBlocked: « Bloqué », may no longer connect.
	TrustBlocked DeviceTrust = "blocked"
)

// DeviceAccess is what kergui holds of one device: its approval, its block
// (« Couper internet », a firewall rule) and, with the block only, its Wi-Fi
// refusal (« Le déconnecter aussi du Wi-Fi », D5). The transitions keep a
// device from being authorised and blocked at once.
type DeviceAccess struct {
	Approved bool
	Blocked  bool
	WiFiCut  bool
}

// Trust is the device's state. Should two stores disagree (approved and
// blocked at once), the block wins: the box enforces it anyway.
func (a DeviceAccess) Trust() DeviceTrust {
	switch {
	case a.Blocked:
		return TrustBlocked
	case a.Approved:
		return TrustAuthorised
	}
	return TrustToCheck
}

// Authorise approves the device; authorising a blocked device unblocks it.
func (a DeviceAccess) Authorise() DeviceAccess { return DeviceAccess{Approved: true} }

// ToCheck withdraws the approval; a blocked device is unblocked.
func (a DeviceAccess) ToCheck() DeviceAccess { return DeviceAccess{} }

// Block blocks the device, also refusing it at the Wi-Fi when cutWiFi;
// blocking an authorised device withdraws its approval.
func (a DeviceAccess) Block(cutWiFi bool) DeviceAccess {
	return DeviceAccess{Blocked: true, WiFiCut: cutWiFi}
}

// Unblock lifts the block and the Wi-Fi refusal: the device is « À vérifier ».
func (a DeviceAccess) Unblock() DeviceAccess { return DeviceAccess{} }

// WiFiLockIntent is what the household asked of its Wi-Fi, kept in the core's
// database (RFC-007 D7). The box is the truth for display; the intent only
// finishes an interrupted close, ends an add window, and keeps devices from
// approving themselves while the Wi-Fi is closed.
type WiFiLockIntent struct {
	// Closed: the household closed its Wi-Fi.
	Closed bool
	// HouseholdAPs and VisitorAPs are the access points of each kind (D2).
	HouseholdAPs []string
	VisitorAPs   []string
	// VisitorsClosed: « Fermer aussi le Wi-Fi des visiteurs ».
	VisitorsClosed bool
	// WindowUntil is the end of the add window, zero when none.
	WindowUntil time.Time
	// WindowStart is when the add window opened, zero when none: it caps
	// the window, extensions included (WiFiWindowMax).
	WindowStart time.Time
	// Since is when the Wi-Fi was last closed or opened.
	Since time.Time
}

// WindowOpen reports whether an add window runs at now.
func (i WiFiLockIntent) WindowOpen(now time.Time) bool {
	return !i.WindowUntil.IsZero() && now.Before(i.WindowUntil)
}

// SelfApproval reports whether a device that joined may approve itself after
// the delay (owner rule of 2026-11-23): only while the Wi-Fi is open (D1,
// G6). Closed, or during an add window, only a hand « Autoriser » lets a
// device in.
func (i WiFiLockIntent) SelfApproval(now time.Time) bool {
	return !i.Closed && !i.WindowOpen(now)
}

// The add window (« Ajouter un nouvel appareil », RFC-007 D4, G4): the
// household's access points stay in Disabled, their allow rules kept, for
// WiFiWindowStep; « Encore 5 min » adds WiFiWindowStep to the deadline; the
// window never lasts more than WiFiWindowMax in all.
const (
	WiFiWindowStep = 5 * time.Minute
	WiFiWindowMax  = 15 * time.Minute
)

var (
	// ErrWiFiNotClosed refuses an add window on a Wi-Fi the household has
	// not closed: there is nothing to open.
	ErrWiFiNotClosed = fmt.Errorf("%w: the Wi-Fi is not closed", ErrInvalidInput)
	// ErrNoAddWindow refuses « Encore 5 min » when no add window runs.
	ErrNoAddWindow = fmt.Errorf("%w: no add window runs", ErrInvalidInput)
	// ErrWindowAtMost refuses « Encore 5 min » on a window that already
	// lasts WiFiWindowMax.
	ErrWindowAtMost = fmt.Errorf("%w: the add window already lasts %s", ErrInvalidInput, WiFiWindowMax)
)

// ClosedAPs are the access points the household closes: its own, plus the
// visitors' with « Fermer aussi le Wi-Fi des visiteurs » (D2).
func (i WiFiLockIntent) ClosedAPs() []string {
	out := append([]string(nil), i.HouseholdAPs...)
	if i.VisitorsClosed {
		out = append(out, i.VisitorAPs...)
	}
	return out
}

// StartWindow opens an add window of WiFiWindowStep at now.
func (i WiFiLockIntent) StartWindow(now time.Time) (WiFiLockIntent, error) {
	if !i.Closed {
		return i, ErrWiFiNotClosed
	}
	i.WindowStart, i.WindowUntil = now, now.Add(WiFiWindowStep)
	return i, nil
}

// windowCap is the latest the window may end.
func (i WiFiLockIntent) windowCap() time.Time {
	start := i.WindowStart
	if start.IsZero() {
		start = i.WindowUntil.Add(-WiFiWindowStep)
	}
	return start.Add(WiFiWindowMax)
}

// CanExtend reports whether « Encore 5 min » would add anything at now.
func (i WiFiLockIntent) CanExtend(now time.Time) bool {
	return i.Closed && i.WindowOpen(now) && i.WindowUntil.Before(i.windowCap())
}

// ExtendWindow is « Encore 5 min »: WiFiWindowStep more from the deadline,
// never past WiFiWindowMax from the window's start.
func (i WiFiLockIntent) ExtendWindow(now time.Time) (WiFiLockIntent, error) {
	if !i.Closed || !i.WindowOpen(now) {
		return i, ErrNoAddWindow
	}
	if !i.CanExtend(now) {
		return i, ErrWindowAtMost
	}
	until := i.WindowUntil.Add(WiFiWindowStep)
	if limit := i.windowCap(); until.After(limit) {
		until = limit
	}
	i.WindowUntil = until
	return i, nil
}

// WindowDue reports whether an add window has reached its deadline at now:
// the next one to reach the box closes it again.
func (i WiFiLockIntent) WindowDue(now time.Time) bool {
	return i.Closed && !i.WindowUntil.IsZero() && !now.Before(i.WindowUntil)
}

// EndWindow forgets the add window; the Wi-Fi stays closed.
func (i WiFiLockIntent) EndWindow() WiFiLockIntent {
	i.WindowStart, i.WindowUntil = time.Time{}, time.Time{}
	return i
}

// MinutesLeft is what the button shows during a window (« Wi-Fi ouvert pour
// 3 min »): the minutes left, rounded up, 0 when no window runs.
func (i WiFiLockIntent) MinutesLeft(now time.Time) int {
	if !i.WindowOpen(now) {
		return 0
	}
	left := i.WindowUntil.Sub(now)
	return int((left + time.Minute - 1) / time.Minute)
}
