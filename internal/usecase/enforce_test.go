package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

type writeGateway struct {
	fakeGateway
	blocked, unblocked []domain.MAC
}

func (g *writeGateway) Block(_ context.Context, m domain.MAC) error {
	g.blocked = append(g.blocked, m)
	return nil
}
func (g *writeGateway) Unblock(_ context.Context, m domain.MAC) error {
	g.unblocked = append(g.unblocked, m)
	return nil
}

type writeFactory struct{ gw *writeGateway }

func (f writeFactory) New(string, string, port.RouterOptions) (port.RouterPort, error) {
	return f.gw, nil
}
func (f writeFactory) Available() []domain.AdapterMeta          { return nil }
func (f writeFactory) Lookup(string) (domain.AdapterMeta, bool) { return f.gw.meta, true }

type memCuts struct{ m map[domain.MAC]string }

func (c *memCuts) Cuts(context.Context, string) (map[domain.MAC]string, error) {
	out := map[domain.MAC]string{}
	for k, v := range c.m {
		out[k] = v
	}
	return out, nil
}
func (c *memCuts) RecordCut(_ context.Context, _ string, mac domain.MAC, pid string) error {
	c.m[mac] = pid
	return nil
}
func (c *memCuts) RemoveCut(_ context.Context, _ string, mac domain.MAC) error {
	delete(c.m, mac)
	return nil
}

type mutableClock struct{ t *time.Time }

func (c mutableClock) Now() time.Time { return *c.t }

// One member with two devices, one owner (protected), one device without member.
func billingFixture(t *testing.T) (*usecase.Billing, *usecase.Enforcement, *memPayments, *writeGateway, *memCuts, *memSettings, *time.Time) {
	t.Helper()
	awaPhone := domain.MustMAC("aa:bb:cc:00:00:11")
	awaLaptop := domain.MustMAC("aa:bb:cc:00:00:22")
	owner := domain.MustMAC("aa:bb:cc:00:00:33")
	stranger := domain.MustMAC("aa:bb:cc:00:00:44")
	routerID := usecase.RouterID("http://r")
	members := &memMembers{list: []domain.Member{
		{ID: "awa", RouterID: routerID, Name: "Awa", Active: true, MACs: []domain.MAC{awaPhone, awaLaptop}},
		{ID: "me", RouterID: routerID, Name: "Moi", Active: true, Protected: true, MACs: []domain.MAC{owner}},
	}}
	usage := usageOnly{bytes: map[domain.MAC]uint64{
		awaPhone: 1_000_000_000, awaLaptop: 3_000_000_000, owner: 9_000_000_000, stranger: 500_000_000,
	}}
	pays := &memPayments{byID: map[string]domain.Payment{}}
	set := &memSettings{s: domain.BillingSettings{PricePerGB: 500, MinAmount: 100, DueDays: 5, GraceDays: 3}}
	now := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	clk := mutableClock{&now}
	b := usecase.NewBilling(noHistory{}, usage, pays, set, members, &fakePay{status: domain.PaymentPending}, clk, nopLogger{}, "https://pay.example")
	vault := newVault()
	_ = vault.Store(context.Background(), routerID, domain.Credentials{Username: "u", Password: "p"})
	gw := &writeGateway{fakeGateway: fakeGateway{meta: f660Meta()}}
	cuts := &memCuts{m: map[domain.MAC]string{}}
	e := usecase.NewEnforcement(writeFactory{gw}, vault, fakeDisco{}, pays, members, set, cuts, newDeviceRepo(), usageOnly{bytes: map[domain.MAC]uint64{}}, clk, nopLogger{})
	return b, e, pays, gw, cuts, set, &now
}

func TestCloseMonth_InvoicesMembersOnceAndOnlyNewUsage(t *testing.T) {
	b, _, pays, _, _, _, _ := billingFixture(t)
	ctx := context.Background()

	rep, err := b.CloseMonth(ctx, "http://r", "") // previous month: 2026-10
	if err != nil {
		t.Fatal(err)
	}
	if rep.Period != "2026-10" || len(rep.Invoices) != 1 || rep.Unbilled != 1 {
		t.Fatalf("report = %+v; want one invoice (Awa), owner skipped, 1 device without member", rep)
	}
	inv := rep.Invoices[0]
	if inv.MemberID != "awa" || inv.Bytes != 4_000_000_000 || inv.AmountXOF != 2000 || len(inv.Items) != 2 {
		t.Errorf("invoice = %+v; want 4 GB on 2 devices = 2000 F", inv)
	}
	if want := time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC); !inv.DueAt.Equal(want) {
		t.Errorf("due = %v, want %v", inv.DueAt, want)
	}

	again, _ := b.CloseMonth(ctx, "http://r", "2026-10")
	if len(again.Invoices) != 0 || len(pays.byID) != 1 {
		t.Errorf("second close invoiced again: %+v", again)
	}
	if _, err := b.CloseMonth(ctx, "http://r", "2026-12"); err == nil {
		t.Error("closing a future month must fail")
	}
}

func TestEnforcement_SimulateThenCutThenRestore(t *testing.T) {
	b, e, _, gw, cuts, set, now := billingFixture(t)
	ctx := context.Background()
	in := usecase.MACFilterInput{BaseURL: "http://r", AdapterID: "zte_f660"}
	rep, _ := b.CloseMonth(ctx, "http://r", "")
	inv := rep.Invoices[0]

	// Before due + grace: nothing.
	*now = inv.DueAt.Add(48 * time.Hour)
	if r, _ := e.Run(ctx, in); len(r.Cut) != 0 {
		t.Fatalf("cut during grace: %+v", r)
	}

	// Overdue, simulation: planned but not executed.
	*now = inv.DueAt.Add(4 * 24 * time.Hour)
	r, err := e.Run(ctx, in)
	if err != nil || !r.Simulated || len(r.Cut) != 2 || len(gw.blocked) != 0 || len(cuts.m) != 0 {
		t.Fatalf("simulation: %+v, %v, blocked %v", r, err, gw.blocked)
	}

	// Active: both of Awa's devices cut, the owner never.
	set.s.EnforceCuts = true
	r, _ = e.Run(ctx, in)
	if len(gw.blocked) != 2 || len(cuts.m) != 2 {
		t.Fatalf("active: %+v, blocked %v", r, gw.blocked)
	}
	for _, m := range gw.blocked {
		if m.String() == "aa:bb:cc:00:00:33" {
			t.Fatal("protected owner device was cut")
		}
	}
	if r, _ = e.Run(ctx, in); len(r.Cut) != 0 || len(gw.blocked) != 2 {
		t.Errorf("second run cut again: %+v", r)
	}

	// Paid: restored, and only kergui's own cuts.
	if _, err := b.MarkPaid(ctx, inv.ID); err != nil {
		t.Fatal(err)
	}
	r, _ = e.Run(ctx, in)
	if len(r.Restored) != 2 || len(gw.unblocked) != 2 || len(cuts.m) != 0 {
		t.Errorf("restore: %+v, unblocked %v", r, gw.unblocked)
	}
}

type switchGateway struct {
	writeGateway
	filterOn bool
}

func (g *switchGateway) MACFilterEnabled(context.Context) (bool, error) { return g.filterOn, nil }
func (g *switchGateway) SetMACFilterEnabled(_ context.Context, on bool) error {
	g.filterOn = on
	return nil
}

type switchFactory struct{ gw *switchGateway }

func (f switchFactory) New(string, string, port.RouterOptions) (port.RouterPort, error) {
	return f.gw, nil
}
func (f switchFactory) Available() []domain.AdapterMeta          { return nil }
func (f switchFactory) Lookup(string) (domain.AdapterMeta, bool) { return f.gw.meta, true }

func TestBlockDevice_EnablesGlobalFilter(t *testing.T) {
	gw := &switchGateway{writeGateway: writeGateway{fakeGateway: fakeGateway{meta: f660Meta()}}}
	vault := newVault()
	_ = vault.Store(context.Background(), usecase.RouterID("http://r"), domain.Credentials{Username: "u", Password: "p"})
	b := usecase.NewBlockDevice(switchFactory{gw}, vault, fakeDisco{}, fakeClock{}, nopLogger{})
	err := b.Execute(context.Background(), usecase.ChangeAccessInput{
		BaseURL: "http://r", AdapterID: "zte_f660", MAC: domain.MustMAC("aa:bb:cc:00:00:11"), Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(gw.blocked) != 1 || !gw.filterOn {
		t.Errorf("blocked=%v filterOn=%v; a cut must switch the global filter on", gw.blocked, gw.filterOn)
	}
}

func TestEnforcement_TestLinkRestoresManualCutOnlyWhenPaid(t *testing.T) {
	ctx := context.Background()
	mac := domain.MustMAC("aa:bb:cc:00:00:55")
	routerID := usecase.RouterID("http://r")
	devs := newDeviceRepo()
	_, _ = devs.Upsert(ctx, domain.Device{RouterID: routerID, MAC: mac, Blocked: true})
	pays := &memPayments{byID: map[string]domain.Payment{}}
	gw := &writeGateway{fakeGateway: fakeGateway{meta: f660Meta()}}
	vault := newVault()
	_ = vault.Store(ctx, routerID, domain.Credentials{Username: "u", Password: "p"})
	cuts := &memCuts{m: map[domain.MAC]string{}}
	now := time.Now()
	e := usecase.NewEnforcement(writeFactory{gw}, vault, fakeDisco{}, pays, &memMembers{},
		&memSettings{s: domain.DefaultBillingSettings}, cuts, devs, usageOnly{bytes: map[domain.MAC]uint64{}}, mutableClock{&now}, nopLogger{})
	in := usecase.MACFilterInput{BaseURL: "http://r", AdapterID: "zte_f660"}

	link := func(id string) domain.Payment {
		p := domain.Payment{ID: id, RouterID: routerID, Kind: domain.PaymentTest, MAC: mac, Status: domain.PaymentPending,
			Items: []domain.PaymentItem{{MAC: mac, Bytes: 1}}}
		pays.byID[id] = p
		if err := e.LinkPayment(ctx, "http://r", p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Cancelled link: the device stays cut, kergui forgets the tie.
	p1 := link("t1")
	p1.Status = domain.PaymentCancelled
	pays.byID["t1"] = p1
	if r, _ := e.Run(ctx, in); len(r.Restored) != 0 || len(gw.unblocked) != 0 || len(cuts.m) != 0 {
		t.Fatalf("cancelled link restored the device: %+v", r)
	}

	// Pending link: still cut. Paid: restored.
	p2 := link("t2")
	if r, _ := e.Run(ctx, in); len(r.Restored) != 0 {
		t.Fatalf("pending link restored the device: %+v", r)
	}
	p2.Status = domain.PaymentPaid
	pays.byID["t2"] = p2
	if r, _ := e.Run(ctx, in); len(r.Restored) != 1 || len(gw.unblocked) != 1 || len(cuts.m) != 0 {
		t.Errorf("paid link did not restore: %+v, unblocked %v", r, gw.unblocked)
	}
}

func TestMembers_SiblingsFollowThePerson(t *testing.T) {
	a, b := domain.MustMAC("aa:bb:cc:00:00:11"), domain.MustMAC("aa:bb:cc:00:00:22")
	other := domain.MustMAC("aa:bb:cc:00:00:99")
	m := usecase.NewMembers(&memMembers{list: []domain.Member{{ID: "x", Name: "Awa", MACs: []domain.MAC{a, b}}}},
		fakeClock{}, nopLogger{})
	got, _ := m.Siblings(context.Background(), "http://r", b)
	if len(got) != 2 || !got[0].Equal(b) || !got[1].Equal(a) {
		t.Errorf("siblings of b = %v, want [b a]", got)
	}
	if got, _ := m.Siblings(context.Background(), "http://r", other); len(got) != 1 || !got[0].Equal(other) {
		t.Errorf("unassigned device = %v, want itself only", got)
	}
}

func TestCeiling_LinkThenCutThenRestoreOnPayment(t *testing.T) {
	ctx := context.Background()
	heavy := domain.MustMAC("ac:bb:cc:00:00:77")
	light := domain.MustMAC("ac:bb:cc:00:00:78")
	routerID := usecase.RouterID("http://r")
	usage := usageOnly{bytes: map[domain.MAC]uint64{heavy: 150_000_000_000, light: 40_000_000_000}}
	pays := &memPayments{byID: map[string]domain.Payment{}}
	set := &memSettings{s: domain.BillingSettings{PricePerGB: 100, MinAmount: 100, CapGB: 100, EnforceCuts: true}}
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	clk := mutableClock{&now}
	gwPay := &fakePay{status: domain.PaymentPending}
	b := usecase.NewBilling(noHistory{}, usage, pays, set, &memMembers{}, gwPay, clk, nopLogger{}, "https://pay.example")
	router := &writeGateway{fakeGateway: fakeGateway{meta: f660Meta()}}
	vault := newVault()
	_ = vault.Store(ctx, routerID, domain.Credentials{Username: "u", Password: "p"})
	cuts := &memCuts{m: map[domain.MAC]string{}}
	e := usecase.NewEnforcement(writeFactory{router}, vault, fakeDisco{}, pays, &memMembers{}, set, cuts, newDeviceRepo(), usage, clk, nopLogger{})
	in := usecase.MACFilterInput{BaseURL: "http://r", AdapterID: "zte_f660"}

	links, err := b.EnsureCapLinks(ctx, "http://r", nil)
	if err != nil || len(links) != 1 || !links[0].MAC.Equal(heavy) || links[0].AmountXOF != 15000 {
		t.Fatalf("links = %+v, %v; want one 150 GB link for the heavy user", links, err)
	}
	if again, _ := b.EnsureCapLinks(ctx, "http://r", nil); len(again) != 0 {
		t.Errorf("second pass created another link: %+v", again)
	}
	rep, err := e.Run(ctx, in)
	if err != nil || len(rep.Cut) != 1 || !rep.Cut[0].MAC.Equal(heavy) || len(router.blocked) != 1 {
		t.Fatalf("cut = %+v, %v, blocked %v", rep, err, router.blocked)
	}

	gwPay.status = domain.PaymentPaid
	if _, err := b.Refresh(ctx, links[0].ID); err != nil {
		t.Fatal(err)
	}
	if rep, _ = e.Run(ctx, in); len(rep.Restored) != 1 || len(router.unblocked) != 1 {
		t.Errorf("after payment: %+v, unblocked %v", rep, router.unblocked)
	}
	if rep, _ = e.Run(ctx, in); len(rep.Cut) != 0 {
		t.Errorf("paid consumption must not be cut again: %+v", rep)
	}
}
