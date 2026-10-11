package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
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
	members   port.MemberRepository
	gateway   port.PaymentGateway // nil when NabooPay is not configured
	clock     port.Clock
	log       port.Logger
	publicURL string
}

// NewBilling wires the interactor. gateway may be nil.
func NewBilling(h port.DeviceHistory, u port.UsageRepository, p port.PaymentRepository, s port.SettingsRepository,
	m port.MemberRepository, g port.PaymentGateway, c port.Clock, l port.Logger, publicURL string) *Billing {
	return &Billing{history: h, usage: u, payments: p, settings: s, members: m, gateway: g, clock: c, log: l,
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
	if s.CapGB < 0 || s.CapGB > 100_000 {
		return fmt.Errorf("%w: plafond entre 0 (désactivé) et 100 000 Go", domain.ErrInvalidInput)
	}
	if s.DueDays < 0 || s.DueDays > 60 || s.GraceDays < 0 || s.GraceDays > 60 {
		return fmt.Errorf("%w: délais de paiement et de grâce entre 0 et 60 jours", domain.ErrInvalidInput)
	}
	if err := uc.settings.SaveBillingSettings(ctx, s); err != nil {
		return err
	}
	mode := "simulation"
	if s.EnforceCuts {
		mode = "ACTIVE"
	}
	uc.log.Infof("billing: price %d F/GB, minimum %d F, due %d d, grace %d d, cuts %s",
		s.PricePerGB, s.MinAmount, s.DueDays, s.GraceDays, mode)
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
	if n := utf8.RuneCountInString(name); n > 80 {
		return domain.Payment{}, fmt.Errorf("%w: nom du payeur trop long (80 caractères max)", domain.ErrInvalidInput)
	}
	// The phone is optional: the payment is recognised by its order_id.
	phone := ""
	if strings.TrimSpace(in.Phone) != "" {
		p, err := domain.NormalizeSenegalPhone(in.Phone)
		if err != nil {
			return domain.Payment{}, err
		}
		phone = p
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
		ID: newID(), RouterID: routerID, Kind: domain.PaymentTest, MAC: in.MAC, Period: period, Bytes: due,
		Items: []domain.PaymentItem{{MAC: in.MAC, Bytes: due}}, AmountXOF: res.AmountXOF,
		CustomerName: name, Phone: phone, OrderID: res.OrderID, CheckoutURL: res.CheckoutURL,
		Status: domain.PaymentPending, CreatedAt: now, UpdatedAt: now,
	}
	if err := uc.payments.Create(ctx, p); err != nil {
		return domain.Payment{}, err
	}
	uc.log.Infof("billing: checkout %s for %s: %s = %d F (order %s)", p.ID, in.MAC, formatGB(due), p.AmountXOF, p.OrderID)
	return p, nil
}

// EnsureCapLinks opens a payment link for every device whose unpaid
// consumption this month reached the ceiling and has none yet, so that the cut
// that follows always comes with a way to pay. It returns the links created.
func (uc *Billing) EnsureCapLinks(ctx context.Context, baseURL string, names map[domain.MAC]string) ([]domain.Payment, error) {
	set, err := uc.settings.BillingSettings(ctx)
	if err != nil || set.CapBytes() == 0 {
		return nil, err
	}
	routerID := RouterID(baseURL)
	period := domain.BillingPeriod(uc.clock.Now())
	usage, err := uc.usage.UsageByPeriod(ctx, routerID, period)
	if err != nil {
		return nil, err
	}
	var created []domain.Payment
	for _, u := range usage {
		paid, err := uc.payments.PaidBytes(ctx, routerID, u.MAC, period)
		if err != nil {
			return created, err
		}
		if u.Bytes < paid || u.Bytes-paid < set.CapBytes() {
			continue
		}
		if _, ok, err := uc.pendingFor(ctx, routerID, u.MAC, period); err != nil {
			return created, err
		} else if ok {
			continue
		}
		if uc.gateway == nil {
			uc.log.Warnf("billing: %s reached the %d GB ceiling but NabooPay is not configured: no link, no cut", u.MAC, set.CapGB)
			continue
		}
		p, err := uc.RequestPayment(ctx, RequestPaymentInput{BaseURL: baseURL, MAC: u.MAC, CustomerName: names[u.MAC]})
		if err != nil {
			uc.log.Errorf("billing: ceiling link for %s failed: %v", u.MAC, err)
			continue
		}
		uc.log.Warnf("billing: %s reached the %d GB ceiling: payment link %s (%d F) created", u.MAC, set.CapGB, p.ID, p.AmountXOF)
		created = append(created, p)
	}
	return created, nil
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
		if p.Kind == domain.PaymentTest && p.Period == period && p.Status == domain.PaymentPending {
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

// CloseReport summarises a monthly close.
type CloseReport struct {
	Period   string           `json:"period"`
	Invoices []domain.Payment `json:"invoices"`
	TotalXOF int64            `json:"total_xof"`
	NoLink   []string         `json:"no_link"`  // members invoiced without a NabooPay link
	Unbilled int              `json:"unbilled"` // devices with usage but no member
	Errors   []string         `json:"errors"`
}

// CloseMonth invoices every billable member for the consumption of period that
// is neither paid nor already invoiced. Running it again only invoices what was
// consumed since, so it is safe to run on a schedule.
func (uc *Billing) CloseMonth(ctx context.Context, baseURL, period string) (CloseReport, error) {
	now := uc.clock.Now()
	current := domain.BillingPeriod(now)
	if period == "" {
		p, err := domain.PreviousPeriod(current)
		if err != nil {
			return CloseReport{}, err
		}
		period = p
	}
	if _, err := domain.PreviousPeriod(period); err != nil {
		return CloseReport{}, err
	}
	if period > current {
		return CloseReport{}, fmt.Errorf("%w: impossible de clôturer un mois futur (%s)", domain.ErrInvalidInput, period)
	}
	routerID := RouterID(baseURL)
	set, err := uc.settings.BillingSettings(ctx)
	if err != nil {
		return CloseReport{}, err
	}
	members, err := uc.members.List(ctx, routerID)
	if err != nil {
		return CloseReport{}, err
	}
	usage, err := uc.usage.UsageByPeriod(ctx, routerID, period)
	if err != nil {
		return CloseReport{}, err
	}
	used := make(map[domain.MAC]uint64, len(usage))
	for _, u := range usage {
		used[u.MAC] = u.Bytes
	}

	rep := CloseReport{Period: period, Invoices: []domain.Payment{}, NoLink: []string{}, Errors: []string{}}
	owned := map[domain.MAC]bool{}
	for _, m := range members {
		for _, mac := range m.MACs {
			owned[mac] = true
		}
		if !m.Billable() {
			continue
		}
		var items []domain.PaymentItem
		var total uint64
		for _, mac := range m.MACs {
			covered, err := uc.coveredBytes(ctx, routerID, mac, period, true)
			if err != nil {
				return rep, err
			}
			if used[mac] > covered {
				d := used[mac] - covered
				items = append(items, domain.PaymentItem{MAC: mac, Bytes: d})
				total += d
			}
		}
		if total == 0 {
			continue
		}
		inv, err := uc.createInvoice(ctx, m, period, items, total, set, now)
		if err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", m.Name, err))
			continue
		}
		if inv.CheckoutURL == "" {
			rep.NoLink = append(rep.NoLink, m.Name)
		}
		rep.Invoices = append(rep.Invoices, inv)
		rep.TotalXOF += inv.AmountXOF
	}
	for mac, b := range used {
		if b > 0 && !owned[mac] {
			rep.Unbilled++
		}
	}
	if len(rep.Invoices) > 0 || len(rep.Errors) > 0 {
		uc.log.Infof("billing: closed %s: %d invoice(s), %d F, %d device(s) without member, %d error(s)",
			period, len(rep.Invoices), rep.TotalXOF, rep.Unbilled, len(rep.Errors))
	}
	return rep, nil
}

func (uc *Billing) createInvoice(ctx context.Context, m domain.Member, period string, items []domain.PaymentItem,
	total uint64, set domain.BillingSettings, now time.Time) (domain.Payment, error) {
	// Pending test links on these devices are superseded by the invoice.
	for _, it := range items {
		if p, ok, err := uc.pendingFor(ctx, m.RouterID, it.MAC, period); err != nil {
			return domain.Payment{}, err
		} else if ok {
			uc.cancel(ctx, p, "superseded by the monthly invoice")
		}
	}
	inv := domain.Payment{
		ID: newID(), RouterID: m.RouterID, Kind: domain.PaymentInvoice, MemberID: m.ID, Period: period,
		Bytes: total, Items: items, AmountXOF: set.Amount(total), CustomerName: m.Name, Phone: m.Phone,
		Status: domain.PaymentPending, CreatedAt: now, UpdatedAt: now,
		DueAt: now.Add(time.Duration(set.DueDays) * 24 * time.Hour),
	}
	if uc.gateway != nil {
		res, err := uc.gateway.CreateCheckout(ctx, port.CheckoutRequest{
			Label:        "Internet Kergui " + period,
			Description:  fmt.Sprintf("%s : %s sur %d appareil(s)", period, formatGB(total), len(items)),
			AmountXOF:    inv.AmountXOF,
			CustomerName: m.Name,
			Phone:        m.Phone,
			SuccessURL:   uc.publicURL + "/paiement/merci",
			ErrorURL:     uc.publicURL + "/paiement/echec",
		})
		if err != nil {
			return domain.Payment{}, err
		}
		inv.OrderID, inv.CheckoutURL, inv.AmountXOF = res.OrderID, res.CheckoutURL, res.AmountXOF
	}
	if err := uc.payments.Create(ctx, inv); err != nil {
		return domain.Payment{}, err
	}
	uc.log.Infof("billing: invoice %s for %q, %s: %s = %d F, due %s",
		inv.ID, m.Name, period, formatGB(total), inv.AmountXOF, inv.DueAt.Format("2006-01-02"))
	return inv, nil
}

// coveredBytes is a device's consumption in period already paid, or (with
// invoiced) also covered by a pending invoice.
func (uc *Billing) coveredBytes(ctx context.Context, routerID string, mac domain.MAC, period string, invoiced bool) (uint64, error) {
	ps, err := uc.payments.ListByMAC(ctx, routerID, mac, 200)
	if err != nil {
		return 0, err
	}
	var n uint64
	for _, p := range ps {
		if p.Period != period {
			continue
		}
		if p.Status != domain.PaymentPaid && !(invoiced && p.Kind == domain.PaymentInvoice && p.Status == domain.PaymentPending) {
			continue
		}
		for _, it := range p.Items {
			if it.MAC.Equal(mac) {
				n += it.Bytes
			}
		}
	}
	return n, nil
}

// Invoices lists the invoices of a period ("" = previous month).
func (uc *Billing) Invoices(ctx context.Context, baseURL, period string) ([]domain.Payment, error) {
	if period == "" {
		p, err := domain.PreviousPeriod(domain.BillingPeriod(uc.clock.Now()))
		if err != nil {
			return nil, err
		}
		period = p
	}
	return uc.payments.ListByPeriod(ctx, RouterID(baseURL), period, domain.PaymentInvoice)
}

// MarkPaid records a payment received outside NabooPay (cash, transfer).
func (uc *Billing) MarkPaid(ctx context.Context, id string) (domain.Payment, error) {
	p, ok, err := uc.payments.Get(ctx, id)
	if err != nil {
		return domain.Payment{}, err
	}
	if !ok {
		return domain.Payment{}, domain.ErrPaymentNotFound
	}
	if p.Status != domain.PaymentPending {
		return domain.Payment{}, fmt.Errorf("%w: seul un paiement en attente peut être marqué payé (statut %s)", domain.ErrInvalidInput, p.Status)
	}
	if uc.gateway != nil && p.OrderID != "" {
		if err := uc.gateway.CancelCheckout(ctx, p.OrderID); err != nil {
			uc.log.Warnf("billing: could not cancel NabooPay link %s after manual payment: %v", p.OrderID, err)
		}
	}
	now := uc.clock.Now()
	p.Status, p.PaidAt, p.UpdatedAt = domain.PaymentPaid, now, now
	if err := uc.payments.Update(ctx, p); err != nil {
		return domain.Payment{}, err
	}
	uc.log.Infof("billing: payment %s marked paid manually (%d F)", p.ID, p.AmountXOF)
	return p, nil
}

func formatGB(b uint64) string {
	return strings.Replace(fmt.Sprintf("%.2f Go", float64(b)/1e9), ".", ",", 1)
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
