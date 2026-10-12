package usecase

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// CaptiveReason says why a device is held on the portal.
type CaptiveReason string

const (
	CaptiveUnpaid    CaptiveReason = "unpaid"    // cut by the enforcement for an overdue invoice
	CaptiveBlacklist CaptiveReason = "blacklist" // cut for changing its Wi-Fi address too often
	CaptiveManual    CaptiveReason = "manual"    // cut by the admin
	// CaptivePayment is not a cut: the device has a pending payment link and
	// is shown the payment page until it pays or chooses "pay later".
	CaptivePayment CaptiveReason = "payment"
)

// CaptiveState is what the captive DNS and portal know about one LAN address.
type CaptiveState struct {
	Captive bool            `json:"captive"`
	MAC     domain.MAC      `json:"mac"`
	Name    string          `json:"name"`
	Reason  CaptiveReason   `json:"reason,omitempty"`
	Payment *domain.Payment `json:"payment,omitempty"` // pending payment with a link, if any
}

// Captive keeps an in-memory map from LAN IP to captive state, rebuilt by
// Refresh from the stored inventory. Lookups are lock-cheap: the DNS server
// asks on every query.
type Captive struct {
	devices   port.DeviceRepository
	payments  port.PaymentRepository
	cuts      port.CutRepository
	identity  port.IdentityRepository
	reminders port.ReminderRepository
	clock     port.Clock
	log       port.Logger

	mu       sync.RWMutex
	byIP     map[string]CaptiveState
	routerID string
}

// staleAfter bounds how old an IP↔MAC pairing may be: DHCP hands addresses
// back out, and a long-gone blocked device must not capture its successor.
const staleAfter = 30 * time.Minute

// NewCaptive wires the interactor.
func NewCaptive(d port.DeviceRepository, p port.PaymentRepository, c port.CutRepository, id port.IdentityRepository,
	r port.ReminderRepository, clk port.Clock, l port.Logger) *Captive {
	return &Captive{devices: d, payments: p, cuts: c, identity: id, reminders: r, clock: clk, log: l, byIP: map[string]CaptiveState{}}
}

// Refresh rebuilds the map. The inventory must be fresh (ListDevices with
// Persist) for Blocked and IPs to be current.
func (uc *Captive) Refresh(ctx context.Context, baseURL string) error {
	routerID := RouterID(baseURL)
	devs, err := uc.devices.ListByRouter(ctx, routerID)
	if err != nil {
		return err
	}
	cuts, err := uc.cuts.Cuts(ctx, routerID)
	if err != nil {
		return err
	}
	blCuts, err := uc.identity.BlacklistCuts(ctx, routerID)
	if err != nil {
		return err
	}
	snoozes, err := uc.reminders.Snoozes(ctx, routerID)
	if err != nil {
		return err
	}
	now := uc.clock.Now()
	next := make(map[string]CaptiveState, len(devs))
	seenAt := map[string]time.Time{}
	captive := 0
	for _, d := range devs {
		ip := d.IP.String()
		if d.IP.IsZero() || now.Sub(d.LastSeen) > staleAfter {
			continue
		}
		if prev, ok := seenAt[ip]; ok && !d.LastSeen.After(prev) {
			continue // the most recently seen device owns a shared address
		}
		st := CaptiveState{MAC: d.MAC, Name: d.DisplayName()}
		_, unpaid := cuts[d.MAC]
		_, blacklisted := blCuts[d.MAC]
		switch {
		case unpaid:
			st.Captive, st.Reason = true, CaptiveUnpaid
		case blacklisted:
			st.Captive, st.Reason = true, CaptiveBlacklist
		case d.Blocked:
			st.Captive, st.Reason = true, CaptiveManual
		}
		p, err := uc.pendingLink(ctx, routerID, d.MAC)
		if err != nil {
			return err
		}
		st.Payment = p
		// A pending link alone shows the payment page, without cutting
		// anything, until it is paid or postponed.
		if !st.Captive && p != nil && !now.Before(snoozes[p.ID]) {
			st.Captive, st.Reason = true, CaptivePayment
		}
		seenAt[ip] = d.LastSeen
		next[ip] = st
	}
	for _, st := range next {
		if st.Captive {
			captive++
		}
	}
	uc.mu.Lock()
	changed := len(uc.byIP) == 0 || countCaptive(uc.byIP) != captive
	uc.byIP = next
	uc.routerID = routerID
	uc.mu.Unlock()
	if changed {
		uc.log.Infof("captive: %d device(s) redirected to the portal", captive)
	}
	return nil
}

func countCaptive(m map[string]CaptiveState) int {
	n := 0
	for _, st := range m {
		if st.Captive {
			n++
		}
	}
	return n
}

// pendingLink returns the newest pending payment with a checkout link covering mac.
func (uc *Captive) pendingLink(ctx context.Context, routerID string, mac domain.MAC) (*domain.Payment, error) {
	ps, err := uc.payments.ListByMAC(ctx, routerID, mac, 20)
	if err != nil {
		return nil, err
	}
	var best *domain.Payment
	for i := range ps {
		p := ps[i]
		if p.Status != domain.PaymentPending || p.CheckoutURL == "" {
			continue
		}
		if best == nil || p.CreatedAt.After(best.CreatedAt) {
			best = &p
		}
	}
	return best, nil
}

// Lookup returns the state of a LAN address; ok is false for unknown addresses.
func (uc *Captive) Lookup(ip string) (CaptiveState, bool) {
	uc.mu.RLock()
	defer uc.mu.RUnlock()
	st, ok := uc.byIP[ip]
	return st, ok
}

// IsCaptive reports whether DNS answers for ip must point to the portal.
func (uc *Captive) IsCaptive(ip string) bool {
	st, ok := uc.Lookup(ip)
	return ok && st.Captive
}

// Redirect implements the captive DNS policy. A cut device is sent to the
// portal for every name; a device that is only reminded to pay, for its
// phone's connectivity checks alone, so nothing else stops working (a PC or a
// TV, which never shows the portal, keeps browsing normally).
func (uc *Captive) Redirect(ip string, probe bool) bool {
	st, ok := uc.Lookup(ip)
	if !ok || !st.Captive {
		return false
	}
	return st.Reason != CaptivePayment || probe
}

// PayLater hides the payment page of a device that is not cut, for
// domain.PaymentReminderSnooze. A cut device cannot postpone its cut.
func (uc *Captive) PayLater(ctx context.Context, ip string) error {
	uc.mu.RLock()
	st, ok := uc.byIP[ip]
	routerID := uc.routerID
	uc.mu.RUnlock()
	if !ok || st.Reason != CaptivePayment || st.Payment == nil {
		return fmt.Errorf("%w: rien à reporter pour cet appareil", domain.ErrInvalidInput)
	}
	until := uc.clock.Now().Add(domain.PaymentReminderSnooze)
	if err := uc.reminders.Snooze(ctx, routerID, st.Payment.ID, until); err != nil {
		return err
	}
	uc.mu.Lock()
	if cur, ok := uc.byIP[ip]; ok && cur.Reason == CaptivePayment {
		cur.Captive, cur.Reason = false, ""
		uc.byIP[ip] = cur
	}
	uc.mu.Unlock()
	uc.log.Infof("captive: %s (%s) postponed payment %s until %s", st.MAC, st.Name, st.Payment.ID, until.Format("02/01 15:04"))
	return nil
}
