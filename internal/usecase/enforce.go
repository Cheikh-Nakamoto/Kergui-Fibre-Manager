package usecase

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Enforcement cuts the internet of members whose invoice is overdue, and
// restores it once the invoice is settled. While the billing settings say
// EnforceCuts=false it only reports what it would do.
type Enforcement struct {
	factory  port.RouterFactory
	vault    port.CredentialVault
	disco    port.DiscoveryPort
	payments port.PaymentRepository
	members  port.MemberRepository
	settings port.SettingsRepository
	cuts     port.CutRepository
	devices  port.DeviceRepository
	usage    port.UsageRepository
	clock    port.Clock
	log      port.Logger

	mu        sync.Mutex
	simLogged map[domain.MAC]time.Time // simulation warnings, at most hourly
}

// NewEnforcement wires the interactor.
func NewEnforcement(f port.RouterFactory, v port.CredentialVault, d port.DiscoveryPort, p port.PaymentRepository,
	m port.MemberRepository, s port.SettingsRepository, c port.CutRepository, devs port.DeviceRepository,
	u port.UsageRepository, clk port.Clock, l port.Logger) *Enforcement {
	return &Enforcement{factory: f, vault: v, disco: d, payments: p, members: m, settings: s, cuts: c, devices: devs,
		usage: u, clock: clk, log: l, simLogged: map[domain.MAC]time.Time{}}
}

// LinkPayment ties a device that is currently cut to a new payment link, so
// that paying it restores the device automatically.
func (uc *Enforcement) LinkPayment(ctx context.Context, baseURL string, p domain.Payment) error {
	routerID := RouterID(baseURL)
	cuts, err := uc.cuts.Cuts(ctx, routerID)
	if err != nil {
		return err
	}
	for _, it := range p.Items {
		if _, already := cuts[it.MAC]; already {
			continue
		}
		d, ok, err := uc.devices.FindByMAC(ctx, routerID, it.MAC)
		if err != nil {
			return err
		}
		if !ok || !d.Blocked {
			continue
		}
		if err := uc.cuts.RecordCut(ctx, routerID, it.MAC, p.ID); err != nil {
			return err
		}
		uc.log.Infof("enforcement: %s is cut; paying %s (%d F) will restore it", it.MAC, p.ID, p.AmountXOF)
	}
	return nil
}

// EnforceAction is one cut or restore, done or (in simulation) planned.
type EnforceAction struct {
	MAC     domain.MAC `json:"mac"`
	Member  string     `json:"member"`
	Payment string     `json:"payment_id"`
	Error   string     `json:"error,omitempty"`
}

// EnforceReport says what a run did.
type EnforceReport struct {
	Simulated bool            `json:"simulated"`
	Cut       []EnforceAction `json:"cut"`
	Restored  []EnforceAction `json:"restored"`
}

// Run cuts overdue members and restores settled ones.
func (uc *Enforcement) Run(ctx context.Context, in MACFilterInput) (EnforceReport, error) {
	routerID := RouterID(in.BaseURL)
	set, err := uc.settings.BillingSettings(ctx)
	if err != nil {
		return EnforceReport{}, err
	}
	rep := EnforceReport{Simulated: !set.EnforceCuts, Cut: []EnforceAction{}, Restored: []EnforceAction{}}

	members, err := uc.members.List(ctx, routerID)
	if err != nil {
		return rep, err
	}
	byID := make(map[string]domain.Member, len(members))
	for _, m := range members {
		byID[m.ID] = m
	}
	cuts, err := uc.cuts.Cuts(ctx, routerID)
	if err != nil {
		return rep, err
	}

	// Restore. An invoice cut stays while the invoice is pending and its member
	// still billable; paid, cancelled, deleted or now-protected all restore.
	// A manual cut tied to a test link is restored only once that link is paid;
	// if the link is cancelled the device simply stays cut, as the admin left it.
	var toRestore []EnforceAction
	for mac, pid := range cuts {
		p, ok, err := uc.payments.Get(ctx, pid)
		if err != nil {
			return rep, err
		}
		if ok && p.Kind == domain.PaymentTest {
			switch p.Status {
			case domain.PaymentPending:
				continue
			case domain.PaymentPaid:
				toRestore = append(toRestore, EnforceAction{MAC: mac, Member: p.CustomerName, Payment: pid})
			default:
				if err := uc.cuts.RemoveCut(ctx, routerID, mac); err != nil {
					return rep, err
				}
			}
			continue
		}
		m, known := byID[p.MemberID]
		if ok && p.Status == domain.PaymentPending && known && m.Billable() {
			continue
		}
		toRestore = append(toRestore, EnforceAction{MAC: mac, Member: m.Name, Payment: pid})
	}

	// Cut: devices of members with an overdue invoice, not already cut.
	now := uc.clock.Now()
	pending, err := uc.payments.ListByStatus(ctx, domain.PaymentPending, 500)
	if err != nil {
		return rep, err
	}
	var toCut []EnforceAction
	planned := map[domain.MAC]bool{}
	for _, p := range pending {
		if p.RouterID != routerID || !p.Overdue(now, set.Grace()) {
			continue
		}
		m, ok := byID[p.MemberID]
		if !ok || !m.Billable() {
			continue
		}
		for _, mac := range m.MACs {
			if _, already := cuts[mac]; already || planned[mac] {
				continue
			}
			planned[mac] = true
			toCut = append(toCut, EnforceAction{MAC: mac, Member: m.Name, Payment: p.ID})
		}
	}

	// Ceiling: a device whose unpaid consumption this month reached the cap is
	// cut until the payment link opened for it (Billing.EnsureCapLinks) is paid.
	if capBytes := set.CapBytes(); capBytes > 0 {
		period := domain.BillingPeriod(now)
		usage, err := uc.usage.UsageByPeriod(ctx, routerID, period)
		if err != nil {
			return rep, err
		}
		for _, u := range usage {
			if _, already := cuts[u.MAC]; already || planned[u.MAC] {
				continue
			}
			paid, err := uc.payments.PaidBytes(ctx, routerID, u.MAC, period)
			if err != nil {
				return rep, err
			}
			if u.Bytes < paid || u.Bytes-paid < capBytes {
				continue
			}
			link, ok, err := uc.capLink(ctx, routerID, u.MAC, period)
			if err != nil {
				return rep, err
			}
			if !ok {
				continue // no way to pay yet: never cut without a link
			}
			planned[u.MAC] = true
			toCut = append(toCut, EnforceAction{MAC: u.MAC, Member: fmt.Sprintf("plafond %d Go", set.CapGB), Payment: link})
		}
	}

	if len(toCut) == 0 && len(toRestore) == 0 {
		return rep, nil
	}
	if rep.Simulated {
		for _, a := range toCut {
			if uc.shouldLogSim(a.MAC, now) {
				uc.log.Warnf("enforcement (simulation): would cut %s (%s), payment %s pending", a.MAC, a.Member, a.Payment)
			}
		}
		rep.Cut = toCut
		// Real cuts made before switching to simulation are still restored when settled.
		toCut = nil
		if len(toRestore) == 0 {
			return rep, nil
		}
	}

	gw, err := loggedInGateway(ctx, uc.factory, uc.vault, uc.disco, routerTarget(in))
	if err != nil {
		return rep, err
	}
	for _, a := range toRestore {
		if err := gw.Unblock(ctx, a.MAC); err != nil {
			a.Error = err.Error()
			uc.log.Errorf("enforcement: restore %s of %q failed: %v", a.MAC, a.Member, err)
		} else if err := uc.cuts.RemoveCut(ctx, routerID, a.MAC); err != nil {
			a.Error = err.Error()
		} else {
			uc.log.Infof("enforcement: internet restored for %s of %q (invoice %s settled)", a.MAC, a.Member, a.Payment)
		}
		rep.Restored = append(rep.Restored, a)
	}
	if len(toCut) > 0 {
		if err := ensureFilterOn(ctx, gw, uc.log); err != nil {
			uc.log.Errorf("enforcement: cannot enable the router's MAC filter, cuts would have no effect: %v", err)
			return rep, err
		}
	}
	for _, a := range toCut {
		if err := gw.Block(ctx, a.MAC); err != nil {
			a.Error = err.Error()
			uc.log.Errorf("enforcement: cut %s of %q failed: %v", a.MAC, a.Member, err)
		} else if err := uc.cuts.RecordCut(ctx, routerID, a.MAC, a.Payment); err != nil {
			a.Error = fmt.Sprintf("cut on router but not recorded: %v", err)
			uc.log.Errorf("enforcement: %s", a.Error)
		} else {
			uc.log.Warnf("enforcement: internet cut for %s of %q, invoice %s overdue", a.MAC, a.Member, a.Payment)
		}
		rep.Cut = append(rep.Cut, a)
	}
	return rep, nil
}

// capLink returns the pending on-demand link covering mac this period.
func (uc *Enforcement) capLink(ctx context.Context, routerID string, mac domain.MAC, period string) (string, bool, error) {
	ps, err := uc.payments.ListByMAC(ctx, routerID, mac, 20)
	if err != nil {
		return "", false, err
	}
	for _, p := range ps {
		if p.Kind == domain.PaymentTest && p.Period == period && p.Status == domain.PaymentPending {
			return p.ID, true, nil
		}
	}
	return "", false, nil
}

func (uc *Enforcement) shouldLogSim(mac domain.MAC, now time.Time) bool {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	if last, ok := uc.simLogged[mac]; ok && now.Sub(last) < time.Hour {
		return false
	}
	uc.simLogged[mac] = now
	return true
}
