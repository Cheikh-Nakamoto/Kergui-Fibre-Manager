package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

type memPayments struct{ byID map[string]domain.Payment }

func (m *memPayments) Create(_ context.Context, p domain.Payment) error { m.byID[p.ID] = p; return nil }
func (m *memPayments) Update(_ context.Context, p domain.Payment) error { m.byID[p.ID] = p; return nil }
func (m *memPayments) Get(_ context.Context, id string) (domain.Payment, bool, error) {
	p, ok := m.byID[id]
	return p, ok, nil
}
func hasItem(p domain.Payment, mac domain.MAC) bool {
	for _, it := range p.Items {
		if it.MAC.Equal(mac) {
			return true
		}
	}
	return false
}

func (m *memPayments) ListByMAC(_ context.Context, _ string, mac domain.MAC, _ int) ([]domain.Payment, error) {
	var out []domain.Payment
	for _, p := range m.byID {
		if hasItem(p, mac) {
			out = append(out, p)
		}
	}
	return out, nil
}
func (m *memPayments) ListByPeriod(_ context.Context, _, period string, kind domain.PaymentKind) ([]domain.Payment, error) {
	var out []domain.Payment
	for _, p := range m.byID {
		if p.Period == period && p.Kind == kind {
			out = append(out, p)
		}
	}
	return out, nil
}
func (m *memPayments) ListByStatus(_ context.Context, st domain.PaymentStatus, _ int) ([]domain.Payment, error) {
	var out []domain.Payment
	for _, p := range m.byID {
		if p.Status == st {
			out = append(out, p)
		}
	}
	return out, nil
}
func (m *memPayments) PaidBytes(_ context.Context, _ string, mac domain.MAC, period string) (uint64, error) {
	var n uint64
	for _, p := range m.byID {
		if p.Period != period || p.Status != domain.PaymentPaid {
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

type memSettings struct{ s domain.BillingSettings }

func (m *memSettings) BillingSettings(context.Context) (domain.BillingSettings, error) {
	return m.s, nil
}
func (m *memSettings) SaveBillingSettings(_ context.Context, s domain.BillingSettings) error {
	m.s = s
	return nil
}

type usageOnly struct{ bytes map[domain.MAC]uint64 }

func (u usageOnly) LastSamples(context.Context, string) (map[domain.MAC]domain.TrafficCounters, error) {
	return nil, nil
}
func (u usageOnly) RecordSamples(context.Context, string, string, map[domain.MAC]domain.TrafficCounters, map[domain.MAC]uint64) error {
	return nil
}
func (u usageOnly) UsageByPeriod(_ context.Context, _, period string) ([]domain.Usage, error) {
	var out []domain.Usage
	for m, b := range u.bytes {
		out = append(out, domain.Usage{MAC: m, Period: period, Bytes: b})
	}
	return out, nil
}

type memMembers struct{ list []domain.Member }

func (m *memMembers) Create(_ context.Context, x domain.Member) error {
	m.list = append(m.list, x)
	return nil
}
func (m *memMembers) Update(_ context.Context, x domain.Member) error {
	for i := range m.list {
		if m.list[i].ID == x.ID {
			x.MACs = m.list[i].MACs
			m.list[i] = x
		}
	}
	return nil
}
func (m *memMembers) Delete(_ context.Context, _, id string) error {
	for i := range m.list {
		if m.list[i].ID == id {
			m.list = append(m.list[:i], m.list[i+1:]...)
			return nil
		}
	}
	return domain.ErrMemberNotFound
}
func (m *memMembers) Get(_ context.Context, _, id string) (domain.Member, bool, error) {
	for _, x := range m.list {
		if x.ID == id {
			return x, true, nil
		}
	}
	return domain.Member{}, false, nil
}
func (m *memMembers) List(context.Context, string) ([]domain.Member, error) { return m.list, nil }
func (m *memMembers) Assign(_ context.Context, _ string, mac domain.MAC, id string) error {
	for i := range m.list {
		if m.list[i].ID == id {
			m.list[i].MACs = append(m.list[i].MACs, mac)
		}
	}
	return nil
}

type noHistory struct{}

func (noHistory) ListWithUsage(context.Context, string, string, string, int, int) ([]domain.DeviceUsage, int, error) {
	return nil, 0, nil
}

type fakePay struct {
	created   []port.CheckoutRequest
	cancelled []string
	status    domain.PaymentStatus
}

func (f *fakePay) CreateCheckout(_ context.Context, r port.CheckoutRequest) (port.CheckoutResult, error) {
	f.created = append(f.created, r)
	id := "ord-" + string(rune('0'+len(f.created)))
	return port.CheckoutResult{OrderID: id, CheckoutURL: "https://checkout/" + id, AmountXOF: r.AmountXOF}, nil
}
func (f *fakePay) TransactionStatus(context.Context, string) (domain.PaymentStatus, error) {
	return f.status, nil
}
func (f *fakePay) CancelCheckout(_ context.Context, id string) error {
	f.cancelled = append(f.cancelled, id)
	return nil
}

func TestBilling_RequestRefreshAndReplace(t *testing.T) {
	mac := domain.MustMAC("aa:bb:cc:00:00:11")
	usage := usageOnly{bytes: map[domain.MAC]uint64{mac: 2_500_000_000}}
	pays := &memPayments{byID: map[string]domain.Payment{}}
	gw := &fakePay{status: domain.PaymentPending}
	b := usecase.NewBilling(noHistory{}, usage, pays, &memSettings{s: domain.BillingSettings{PricePerGB: 500, MinAmount: 100}},
		&memMembers{}, gw, fakeClock{time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)}, nopLogger{}, "https://pay.example/")
	ctx := context.Background()
	in := usecase.RequestPaymentInput{BaseURL: "http://r", MAC: mac, CustomerName: "Awa Diop", Phone: "77 123 45 67"}

	p1, err := b.RequestPayment(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if p1.AmountXOF != 1250 || p1.Bytes != 2_500_000_000 || p1.Phone != "+221771234567" || p1.Status != domain.PaymentPending {
		t.Errorf("p1 = %+v", p1)
	}
	if gw.created[0].SuccessURL != "https://pay.example/paiement/merci" {
		t.Errorf("success url = %q", gw.created[0].SuccessURL)
	}

	// A second request replaces the first link.
	p2, err := b.RequestPayment(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if got := pays.byID[p1.ID].Status; got != domain.PaymentCancelled || len(gw.cancelled) != 1 {
		t.Errorf("first checkout should be cancelled, status %s, cancels %v", got, gw.cancelled)
	}

	// Paid: nothing left to pay.
	gw.status = domain.PaymentPaid
	if p, err := b.Refresh(ctx, p2.ID); err != nil || p.Status != domain.PaymentPaid || p.PaidAt.IsZero() {
		t.Fatalf("refresh = %+v, %v", p, err)
	}
	if _, err := b.RequestPayment(ctx, in); !errors.Is(err, domain.ErrNothingToPay) {
		t.Errorf("after payment, err = %v, want ErrNothingToPay", err)
	}

	// New consumption after payment: only the difference is billed.
	usage.bytes[mac] = 3_000_000_000
	p3, err := b.RequestPayment(ctx, in)
	if err != nil || p3.Bytes != 500_000_000 || p3.AmountXOF != 250 {
		t.Errorf("p3 = %+v, %v; want 0.5 GB = 250 F", p3, err)
	}
}

func TestBilling_Validation(t *testing.T) {
	mac := domain.MustMAC("aa:bb:cc:00:00:11")
	clk := fakeClock{time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)}
	set := &memSettings{s: domain.DefaultBillingSettings}
	usage := usageOnly{bytes: map[domain.MAC]uint64{mac: 1}}
	ctx := context.Background()

	off := usecase.NewBilling(noHistory{}, usage, &memPayments{byID: map[string]domain.Payment{}}, set, &memMembers{}, nil, clk, nopLogger{}, "")
	if _, err := off.RequestPayment(ctx, usecase.RequestPaymentInput{MAC: mac, CustomerName: "Awa", Phone: "771234567"}); !errors.Is(err, domain.ErrPaymentUnavailable) {
		t.Errorf("without NabooPay: %v", err)
	}

	on := usecase.NewBilling(noHistory{}, usage, &memPayments{byID: map[string]domain.Payment{}}, set, &memMembers{}, &fakePay{}, clk, nopLogger{}, "")
	if _, err := on.RequestPayment(ctx, usecase.RequestPaymentInput{MAC: mac, CustomerName: "Awa", Phone: "0612"}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("bad phone: %v", err)
	}
	if _, err := on.RequestPayment(ctx, usecase.RequestPaymentInput{MAC: domain.MustMAC("aa:bb:cc:00:00:99"), CustomerName: "Awa", Phone: "771234567"}); !errors.Is(err, domain.ErrNothingToPay) {
		t.Errorf("no usage: %v", err)
	}
	if err := on.SaveSettings(ctx, domain.BillingSettings{PricePerGB: 0}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("price 0: %v", err)
	}
}
