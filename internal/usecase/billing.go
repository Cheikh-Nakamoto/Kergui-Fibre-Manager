package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Billing turns measured consumption into NabooPay payment requests.
type Billing struct {
	history   port.DeviceHistory
	usage     port.UsageRepository
	payments  port.PaymentRepository
	settings  port.SettingsRepository
	gateway   port.PaymentGateway // nil when NabooPay is not configured
	clock     port.Clock
	log       port.Logger
	publicURL string
}

// NewBilling wires the interactor. gateway may be nil.
func NewBilling(h port.DeviceHistory, u port.UsageRepository, p port.PaymentRepository, s port.SettingsRepository,
	g port.PaymentGateway, c port.Clock, l port.Logger, publicURL string) *Billing {
	return &Billing{history: h, usage: u, payments: p, settings: s, gateway: g, clock: c, log: l,
		publicURL: strings.TrimRight(publicURL, "/")}
}

// PaymentsEnabled reports whether a payment provider is configured.
func (uc *Billing) PaymentsEnabled() bool { return uc.gateway != nil }

// HistoryItem is one device of the history page.
type HistoryItem struct {
	domain.DeviceUsage
	DueBytes    uint64          // consumption not yet covered by a paid payment
	EstimateXOF int64           // what DueBytes would cost now
	Pending     *domain.Payment // latest unpaid checkout for this period, if any
}

// HistoryPage is one page of the device history.
type HistoryPage struct {
	Items  []HistoryItem
	Total  int
	Page   int
	Size   int
	Period string
}

// History lists every recorded device with its consumption, one page at a time.
func (uc *Billing) History(ctx context.Context, baseURL, query string, page, size int) (HistoryPage, error) {
	size = min(max(size, 1), 100)
	page = max(page, 1)
	routerID := RouterID(baseURL)
	period := domain.BillingPeriod(uc.clock.Now())
	set, err := uc.settings.BillingSettings(ctx)
	if err != nil {
		return HistoryPage{}, err
	}
	rows, total, err := uc.history.ListWithUsage(ctx, routerID, period, query, size, (page-1)*size)
	if err != nil {
		return HistoryPage{}, err
	}
	out := HistoryPage{Items: make([]HistoryItem, 0, len(rows)), Total: total, Page: page, Size: size, Period: period}
	for _, r := range rows {
		paid, err := uc.payments.PaidBytes(ctx, routerID, r.MAC, period)
		if err != nil {
			return HistoryPage{}, err
		}
		it := HistoryItem{DeviceUsage: r}
		if r.Bytes > paid {
			it.DueBytes = r.Bytes - paid
		}
		it.EstimateXOF = set.Amount(it.DueBytes)
		if p, ok, err := uc.pendingFor(ctx, routerID, r.MAC, period); err != nil {
			return HistoryPage{}, err
		} else if ok {
			it.Pending = &p
		}
		out.Items = append(out.Items, it)
	}
	return out, nil
}

// Settings returns the billing settings.
func (uc *Billing) Settings(ctx context.Context) (domain.BillingSettings, error) {
	return uc.settings.BillingSettings(ctx)
}

// SaveSettings validates and stores the billing settings.
func (uc *Billing) SaveSettings(ctx context.Context, s domain.BillingSettings) error {
	if s.PricePerGB <= 0 || s.PricePerGB > 1_000_000 || s.MinAmount < 0 || s.MinAmount > 1_000_000 {
		return fmt.Errorf("%w: prix par Go entre 1 et 1 000 000 F, minimum entre 0 et 1 000 000 F", domain.ErrInvalidInput)
	}
	if err := uc.settings.SaveBillingSettings(ctx, s); err != nil {
		return err
	}
	uc.log.Infof("billing: price set to %d F/GB, minimum %d F", s.PricePerGB, s.MinAmount)
	return nil
}

// RequestPaymentInput identifies who pays for which device.
type RequestPaymentInput struct {
	BaseURL      string
	MAC          domain.MAC
	CustomerName string
	Phone        string
}

// RequestPayment opens a NabooPay checkout for everything the device has
// consumed this month and not yet paid. A previous unpaid checkout for the same
// period is cancelled first, so only one link is ever valid.
func (uc *Billing) RequestPayment(ctx context.Context, in RequestPaymentInput) (domain.Payment, error) {
	if uc.gateway == nil {
		return domain.Payment{}, fmt.Errorf("%w: NabooPay n'est pas configuré (variable KERGUI_NABOOPAY_API_KEY)", domain.ErrPaymentUnavailable)
	}
	if in.MAC.IsZero() {
		return domain.Payment{}, domain.ErrInvalidMAC
	}
	name := strings.TrimSpace(in.CustomerName)
	if n := utf8.RuneCountInString(name); n < 2 || n > 80 {
		return domain.Payment{}, fmt.Errorf("%w: nom du payeur requis (2 à 80 caractères)", domain.ErrInvalidInput)
	}
	phone, err := domain.NormalizeSenegalPhone(in.Phone)
	if err != nil {
		return domain.Payment{}, err
	}

	routerID := RouterID(in.BaseURL)
	now := uc.clock.Now()
	period := domain.BillingPeriod(now)
	used, err := uc.deviceUsage(ctx, routerID, in.MAC, period)
	if err != nil {
		return domain.Payment{}, err
	}
	paid, err := uc.payments.PaidBytes(ctx, routerID, in.MAC, period)
	if err != nil {
		return domain.Payment{}, err
	}
	if used <= paid {
		return domain.Payment{}, fmt.Errorf("%w: aucune consommation impayée pour %s en %s", domain.ErrNothingToPay, in.MAC, period)
	}
	due := used - paid
	set, err := uc.settings.BillingSettings(ctx)
	if err != nil {
		return domain.Payment{}, err
	}
	amount := set.Amount(due)

	if prev, ok, err := uc.pendingFor(ctx, routerID, in.MAC, period); err != nil {
		return domain.Payment{}, err
	} else if ok {
		uc.cancel(ctx, prev, "replaced by a new checkout")
	}

	res, err := uc.gateway.CreateCheckout(ctx, port.CheckoutRequest{
		Label:        "Internet Kergui " + period,
		Description:  fmt.Sprintf("Consommation %s : %s", period, formatGB(due)),
		AmountXOF:    amount,
		CustomerName: name,
		Phone:        phone,
		SuccessURL:   uc.publicURL + "/paiement/merci",
		ErrorURL:     uc.publicURL + "/paiement/echec",
	})
	if err != nil {
		uc.log.Errorf("billing: NabooPay checkout for %s failed: %v", in.MAC, err)
		return domain.Payment{}, err
	}
	p := domain.Payment{
		ID: newID(), RouterID: routerID, MAC: in.MAC, Period: period, Bytes: due, AmountXOF: res.AmountXOF,
		CustomerName: name, Phone: phone, OrderID: res.OrderID, CheckoutURL: res.CheckoutURL,
		Status: domain.PaymentPending, CreatedAt: now, UpdatedAt: now,
	}
	if err := uc.payments.Create(ctx, p); err != nil {
		return domain.Payment{}, err
	}
	uc.log.Infof("billing: checkout %s for %s: %s = %d F (order %s)", p.ID, in.MAC, formatGB(due), p.AmountXOF, p.OrderID)
	return p, nil
}

// Refresh asks NabooPay for a pending payment's status and records any change.
func (uc *Billing) Refresh(ctx context.Context, id string) (domain.Payment, error) {
	p, ok, err := uc.payments.Get(ctx, id)
	if err != nil {
		return domain.Payment{}, err
	}
	if !ok {
		return domain.Payment{}, domain.ErrPaymentNotFound
	}
	if p.Status != domain.PaymentPending || p.OrderID == "" {
		return p, nil
	}
	if uc.gateway == nil {
		return domain.Payment{}, fmt.Errorf("%w: NabooPay n'est pas configuré", domain.ErrPaymentUnavailable)
	}
	st, err := uc.gateway.TransactionStatus(ctx, p.OrderID)
	if err != nil {
		return domain.Payment{}, err
	}
	if st == p.Status {
		return p, nil
	}
	now := uc.clock.Now()
	p.Status, p.UpdatedAt = st, now
	if st == domain.PaymentPaid {
		p.PaidAt = now
	}
	if err := uc.payments.Update(ctx, p); err != nil {
		return domain.Payment{}, err
	}
	uc.log.Infof("billing: payment %s for %s is now %s (%d F)", p.ID, p.MAC, p.Status, p.AmountXOF)
	return p, nil
}

// RefreshPending checks every pending payment once; it returns how many became paid.
func (uc *Billing) RefreshPending(ctx context.Context) (int, error) {
	if uc.gateway == nil {
		return 0, nil
	}
	pending, err := uc.payments.ListByStatus(ctx, domain.PaymentPending, 50)
	if err != nil {
		return 0, err
	}
	paid := 0
	for _, p := range pending {
		got, err := uc.Refresh(ctx, p.ID)
		if err != nil {
			uc.log.Warnf("billing: status check for %s failed: %v", p.ID, err)
			continue
		}
		if got.Status == domain.PaymentPaid {
			paid++
		}
	}
	return paid, nil
}

// Cancel abandons an unpaid checkout.
func (uc *Billing) Cancel(ctx context.Context, id string) (domain.Payment, error) {
	p, ok, err := uc.payments.Get(ctx, id)
	if err != nil {
		return domain.Payment{}, err
	}
	if !ok {
		return domain.Payment{}, domain.ErrPaymentNotFound
	}
	if p.Status != domain.PaymentPending {
		return domain.Payment{}, fmt.Errorf("%w: seul un paiement en attente peut être annulé (statut %s)", domain.ErrInvalidInput, p.Status)
	}
	return uc.cancel(ctx, p, "cancelled by admin"), nil
}

// Payments lists a device's payments, newest first.
func (uc *Billing) Payments(ctx context.Context, baseURL string, mac domain.MAC) ([]domain.Payment, error) {
	return uc.payments.ListByMAC(ctx, RouterID(baseURL), mac, 20)
}

func (uc *Billing) cancel(ctx context.Context, p domain.Payment, why string) domain.Payment {
	if uc.gateway != nil && p.OrderID != "" {
		if err := uc.gateway.CancelCheckout(ctx, p.OrderID); err != nil {
			uc.log.Warnf("billing: NabooPay cancel of %s failed (%v); marked cancelled locally", p.OrderID, err)
		}
	}
	p.Status, p.UpdatedAt = domain.PaymentCancelled, uc.clock.Now()
	if err := uc.payments.Update(ctx, p); err != nil {
		uc.log.Errorf("billing: cannot mark %s cancelled: %v", p.ID, err)
	}
	uc.log.Infof("billing: payment %s %s", p.ID, why)
	return p
}

func (uc *Billing) pendingFor(ctx context.Context, routerID string, mac domain.MAC, period string) (domain.Payment, bool, error) {
	ps, err := uc.payments.ListByMAC(ctx, routerID, mac, 10)
	if err != nil {
		return domain.Payment{}, false, err
	}
	for _, p := range ps {
		if p.Period == period && p.Status == domain.PaymentPending {
			return p, true, nil
		}
	}
	return domain.Payment{}, false, nil
}

func (uc *Billing) deviceUsage(ctx context.Context, routerID string, mac domain.MAC, period string) (uint64, error) {
	all, err := uc.usage.UsageByPeriod(ctx, routerID, period)
	if err != nil {
		return 0, err
	}
	for _, u := range all {
		if u.MAC.Equal(mac) {
			return u.Bytes, nil
		}
	}
	return 0, nil
}

func formatGB(b uint64) string {
	return strings.Replace(fmt.Sprintf("%.2f Go", float64(b)/1e9), ".", ",", 1)
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
