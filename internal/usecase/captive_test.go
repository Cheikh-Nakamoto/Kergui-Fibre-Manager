package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

type listDevices struct{ fakeDeviceRepo }

func (l *listDevices) ListByRouter(context.Context, string) ([]domain.Device, error) {
	var out []domain.Device
	for _, d := range l.byMAC {
		out = append(out, d)
	}
	return out, nil
}

func TestCaptive_RefreshAndLookup(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	manual := domain.MustMAC("aa:bb:cc:00:00:11")
	unpaid := domain.MustMAC("aa:bb:cc:00:00:22")
	free := domain.MustMAC("ac:bb:cc:00:00:33")
	gone := domain.MustMAC("aa:bb:cc:00:00:44")
	devs := &listDevices{fakeDeviceRepo{byMAC: map[string]domain.Device{}}}
	add := func(mac domain.MAC, ip string, blocked bool, seen time.Time) {
		devs.byMAC[mac.String()] = domain.Device{MAC: mac, IP: domain.MustIP(ip), Hostname: mac.String(), Blocked: blocked, LastSeen: seen}
	}
	add(manual, "192.168.1.10", true, now)
	add(unpaid, "192.168.1.11", false, now)
	add(free, "192.168.1.12", false, now)
	add(gone, "192.168.1.12", true, now.Add(-2*time.Hour)) // old blocked owner of a reused IP

	pays := &memPayments{byID: map[string]domain.Payment{
		"p": {ID: "p", Kind: domain.PaymentTest, Status: domain.PaymentPending, CheckoutURL: "https://c/p",
			AmountXOF: 300, Items: []domain.PaymentItem{{MAC: manual, Bytes: 1}}, CreatedAt: now},
	}}
	cuts := &memCuts{m: map[domain.MAC]string{unpaid: "inv"}}
	c := usecase.NewCaptive(devs, pays, cuts, newMemIdentity(), &memReminders{m: map[string]time.Time{}}, fakeClock{now}, nopLogger{})
	if err := c.Refresh(context.Background(), "http://r"); err != nil {
		t.Fatal(err)
	}

	st, ok := c.Lookup("192.168.1.10")
	if !ok || !st.Captive || st.Reason != usecase.CaptiveManual || st.Payment == nil || st.Payment.CheckoutURL != "https://c/p" {
		t.Errorf("manual cut = %+v", st)
	}
	if st, _ := c.Lookup("192.168.1.11"); !st.Captive || st.Reason != usecase.CaptiveUnpaid || st.Payment != nil {
		t.Errorf("unpaid cut = %+v", st)
	}
	if c.IsCaptive("192.168.1.12") {
		t.Error("an IP reused by a free device must not be captive")
	}
	if c.IsCaptive("192.168.1.99") {
		t.Error("unknown address must not be captive")
	}
}

type memReminders struct{ m map[string]time.Time }

func (r *memReminders) Snoozes(context.Context, string) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	for k, v := range r.m {
		out[k] = v
	}
	return out, nil
}
func (r *memReminders) Snooze(_ context.Context, _, id string, until time.Time) error {
	r.m[id] = until
	return nil
}

func TestCaptive_PendingLinkShowsPaymentUntilPayLater(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	mac := domain.MustMAC("ac:bb:cc:00:00:11")
	devs := &listDevices{fakeDeviceRepo{byMAC: map[string]domain.Device{
		mac.String(): {MAC: mac, IP: domain.MustIP("192.168.1.30"), Hostname: "Phone", LastSeen: now},
	}}}
	pays := &memPayments{byID: map[string]domain.Payment{
		"p": {ID: "p", Kind: domain.PaymentTest, Status: domain.PaymentPending, CheckoutURL: "https://c/p",
			Items: []domain.PaymentItem{{MAC: mac, Bytes: 1}}, CreatedAt: now},
	}}
	clk := now
	rem := &memReminders{m: map[string]time.Time{}}
	c := usecase.NewCaptive(devs, pays, &memCuts{m: map[domain.MAC]string{}}, newMemIdentity(), rem, mutableClock{&clk}, nopLogger{})
	ctx := context.Background()
	_ = c.Refresh(ctx, "http://r")

	st, _ := c.Lookup("192.168.1.30")
	if !st.Captive || st.Reason != usecase.CaptivePayment || st.Payment == nil {
		t.Fatalf("pending link, not cut = %+v; want the payment page", st)
	}
	if err := c.PayLater(ctx, "192.168.1.30"); err != nil {
		t.Fatal(err)
	}
	if c.IsCaptive("192.168.1.30") {
		t.Error("pay later must release the device at once")
	}
	_ = c.Refresh(ctx, "http://r")
	if c.IsCaptive("192.168.1.30") {
		t.Error("pay later must survive a refresh")
	}
	clk = now.Add(25 * time.Hour)
	d := devs.byMAC[mac.String()]
	d.LastSeen = clk // still connected the next day
	devs.byMAC[mac.String()] = d
	_ = c.Refresh(ctx, "http://r")
	if !c.IsCaptive("192.168.1.30") {
		t.Error("the reminder must come back the next day")
	}
	p := pays.byID["p"]
	p.Status = domain.PaymentPaid
	pays.byID["p"] = p
	_ = c.Refresh(ctx, "http://r")
	if c.IsCaptive("192.168.1.30") {
		t.Error("a paid link must not be shown any more")
	}
}
