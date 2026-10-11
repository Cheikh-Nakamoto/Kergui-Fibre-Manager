package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

func newDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestDeviceRepo_UpsertCreatedFlag(t *testing.T) {
	ctx := context.Background()
	repo := sqlite.NewDeviceRepo(newDB(t))
	mac := domain.MustMAC("aa:bb:cc:00:00:11")
	d := domain.Device{
		ID: "r|" + mac.String(), RouterID: "r", MAC: mac, IP: domain.MustIP("192.168.1.10"),
		Hostname: "phone", Connected: true, FirstSeen: time.Unix(100, 0), LastSeen: time.Unix(200, 0),
	}

	created, err := repo.Upsert(ctx, d)
	if err != nil || !created {
		t.Fatalf("first upsert: created=%v err=%v", created, err)
	}
	d.Hostname = "phone-renamed"
	created, err = repo.Upsert(ctx, d)
	if err != nil || created {
		t.Fatalf("second upsert: created=%v err=%v (want false)", created, err)
	}

	got, ok, err := repo.FindByMAC(ctx, "r", mac)
	if err != nil || !ok {
		t.Fatalf("find: ok=%v err=%v", ok, err)
	}
	if got.Hostname != "phone-renamed" || got.IP.String() != "192.168.1.10" || !got.Connected {
		t.Errorf("round-trip wrong: %+v", got)
	}
	if !got.FirstSeen.Equal(time.Unix(100, 0)) {
		t.Errorf("first_seen = %v", got.FirstSeen)
	}

	list, err := repo.ListByRouter(ctx, "r")
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %d err=%v", len(list), err)
	}
}

func TestRouterRepo(t *testing.T) {
	ctx := context.Background()
	repo := sqlite.NewRouterRepo(newDB(t))
	r := domain.Router{ID: "192-168-1-1", Name: "home", BaseURL: "http://192.168.1.1", AdapterID: "zte_f660", Vendor: "ZTE", Model: "F660", CreatedAt: time.Unix(5, 0)}
	if err := repo.Upsert(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, ok, err := repo.Get(ctx, r.ID)
	if err != nil || !ok || got.AdapterID != "zte_f660" || got.Model != "F660" {
		t.Fatalf("get = (%+v, %v, %v)", got, ok, err)
	}
}

func TestAccessRuleRepo_Replace(t *testing.T) {
	ctx := context.Background()
	repo := sqlite.NewAccessRuleRepo(newDB(t))
	rules := []domain.AccessRule{
		{RouterID: "r", MAC: domain.MustMAC("aa:bb:cc:00:00:33"), Mode: domain.AccessBlock},
		{RouterID: "r", MAC: domain.MustMAC("aa:bb:cc:00:00:99"), Mode: domain.AccessBlock},
	}
	if err := repo.ReplaceForRouter(ctx, "r", rules); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ListByRouter(ctx, "r")
	if err != nil || len(got) != 2 {
		t.Fatalf("list = %d err=%v", len(got), err)
	}
	// Replace with a smaller set.
	if err := repo.ReplaceForRouter(ctx, "r", rules[:1]); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.ListByRouter(ctx, "r")
	if len(got) != 1 || got[0].Mode != domain.AccessBlock {
		t.Fatalf("after replace: %+v", got)
	}
}

func TestCredentialStore(t *testing.T) {
	ctx := context.Background()
	s := sqlite.NewCredentialStore(newDB(t))
	if err := s.Put(ctx, "r", []byte("salt"), []byte("nonce"), []byte("ct")); err != nil {
		t.Fatal(err)
	}
	salt, nonce, ct, ok, err := s.Get(ctx, "r")
	if err != nil || !ok || string(salt) != "salt" || string(nonce) != "nonce" || string(ct) != "ct" {
		t.Fatalf("get = (%q,%q,%q,%v,%v)", salt, nonce, ct, ok, err)
	}
	if _, _, _, ok, _ := s.Get(ctx, "missing"); ok {
		t.Error("missing row should report ok=false")
	}
	if err := s.Del(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok, _ := s.Get(ctx, "r"); ok {
		t.Error("row should be gone after delete")
	}
}

func TestUsageRepo_AccumulatesAndKeepsLastSample(t *testing.T) {
	ctx := context.Background()
	repo := sqlite.NewUsageRepo(newDB(t))
	mac := domain.MustMAC("aa:bb:cc:00:00:11")

	s1 := map[domain.MAC]domain.TrafficCounters{mac: {Rx: 10, Tx: 90, LinkSecs: 60}}
	if err := repo.RecordSamples(ctx, "r", "2026-10", s1, map[domain.MAC]uint64{mac: 100}); err != nil {
		t.Fatal(err)
	}
	s2 := map[domain.MAC]domain.TrafficCounters{mac: {Rx: 20, Tx: 180, LinkSecs: 360}}
	if err := repo.RecordSamples(ctx, "r", "2026-10", s2, map[domain.MAC]uint64{mac: 100}); err != nil {
		t.Fatal(err)
	}
	last, err := repo.LastSamples(ctx, "r")
	if err != nil || last[mac] != s2[mac] {
		t.Fatalf("LastSamples = %v, %v; want %v", last, err, s2)
	}
	u, err := repo.UsageByPeriod(ctx, "r", "2026-10")
	if err != nil || len(u) != 1 || u[0].Bytes != 200 {
		t.Fatalf("UsageByPeriod = %+v, %v; want 200 bytes", u, err)
	}
	if other, _ := repo.UsageByPeriod(ctx, "r", "2026-11"); len(other) != 0 {
		t.Errorf("usage leaked into another period: %+v", other)
	}
}

func TestHistoryRepo_PaginatesWithUsageAndSearch(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	devs := sqlite.NewDeviceRepo(db)
	for i, name := range []string{"Phone-Salon", "Laptop-Bureau", "TV-Salon"} {
		mac := domain.MustMAC([]string{"aa:bb:cc:00:00:11", "aa:bb:cc:00:00:22", "aa:bb:cc:00:00:33"}[i])
		if _, err := devs.Upsert(ctx, domain.Device{ID: "r|" + mac.String(), RouterID: "r", MAC: mac,
			Hostname: name, LastSeen: time.Unix(int64(100+i), 0)}); err != nil {
			t.Fatal(err)
		}
	}
	tv := domain.MustMAC("aa:bb:cc:00:00:33")
	if err := sqlite.NewUsageRepo(db).RecordSamples(ctx, "r", "2026-10", nil, map[domain.MAC]uint64{tv: 4242}); err != nil {
		t.Fatal(err)
	}
	h := sqlite.NewHistoryRepo(db)

	page, total, err := h.ListWithUsage(ctx, "r", "2026-10", "", 2, 0)
	if err != nil || total != 3 || len(page) != 2 {
		t.Fatalf("page 1 = %d items, total %d, %v", len(page), total, err)
	}
	if page[0].MAC != tv || page[0].Bytes != 4242 {
		t.Errorf("first item = %s with %d bytes, want most recent (TV) with usage", page[0].MAC, page[0].Bytes)
	}
	page, _, _ = h.ListWithUsage(ctx, "r", "2026-10", "", 2, 2)
	if len(page) != 1 || page[0].Bytes != 0 {
		t.Errorf("page 2 = %+v", page)
	}
	page, total, _ = h.ListWithUsage(ctx, "r", "2026-10", "salon", 10, 0)
	if total != 2 || len(page) != 2 {
		t.Errorf("search 'salon' = %d/%d, want 2", len(page), total)
	}
	if _, total, _ = h.ListWithUsage(ctx, "r", "2026-10", "100%", 10, 0); total != 0 {
		t.Errorf("LIKE wildcards must be escaped, got %d matches", total)
	}
}

func TestPaymentRepo_PaidBytesAndSettings(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	repo := sqlite.NewPaymentRepo(db)
	mac := domain.MustMAC("aa:bb:cc:00:00:11")
	now := time.Unix(1000, 0)
	for i, st := range []domain.PaymentStatus{domain.PaymentPaid, domain.PaymentPending, domain.PaymentPaid} {
		p := domain.Payment{ID: string(rune('a' + i)), RouterID: "r", MAC: mac, Period: "2026-10",
			Bytes: 100, AmountXOF: 100, Status: st, CreatedAt: now.Add(time.Duration(i) * time.Second)}
		if err := repo.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := repo.PaidBytes(ctx, "r", mac, "2026-10"); err != nil || n != 200 {
		t.Errorf("PaidBytes = %d, %v; want 200 (pending excluded)", n, err)
	}
	pending, _ := repo.ListByStatus(ctx, domain.PaymentPending, 10)
	if len(pending) != 1 || pending[0].ID != "b" {
		t.Errorf("pending = %+v", pending)
	}
	p := pending[0]
	p.Status, p.PaidAt = domain.PaymentPaid, now
	if err := repo.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := repo.Get(ctx, "b"); !ok || got.Status != domain.PaymentPaid || got.PaidAt.IsZero() {
		t.Errorf("after update = %+v", got)
	}

	set := sqlite.NewSettingsRepo(db)
	if s, _ := set.BillingSettings(ctx); s != domain.DefaultBillingSettings {
		t.Errorf("defaults = %+v", s)
	}
	want := domain.BillingSettings{PricePerGB: 750, MinAmount: 200}
	if err := set.SaveBillingSettings(ctx, want); err != nil {
		t.Fatal(err)
	}
	if s, _ := set.BillingSettings(ctx); s != want {
		t.Errorf("saved = %+v, want %+v", s, want)
	}
}

func TestMemberRepo_AssignMoveDelete(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	repo := sqlite.NewMemberRepo(db)
	a := domain.Member{ID: "m1", RouterID: "r", Name: "Awa", Kind: domain.MemberRelative, Active: true}
	b := domain.Member{ID: "m2", RouterID: "r", Name: "Bassirou", Kind: domain.MemberExternal, Active: true}
	for _, m := range []domain.Member{b, a} {
		if err := repo.Create(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	mac := domain.MustMAC("aa:bb:cc:00:00:11")
	if err := repo.Assign(ctx, "r", mac, "m1"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Assign(ctx, "r", mac, "m2"); err != nil { // a device has one owner
		t.Fatal(err)
	}
	ms, _ := repo.List(ctx, "r")
	if len(ms) != 2 || ms[0].Name != "Awa" || len(ms[0].MACs) != 0 || len(ms[1].MACs) != 1 {
		t.Fatalf("members = %+v", ms)
	}
	if err := repo.Assign(ctx, "r", mac, "nope"); !errors.Is(err, domain.ErrMemberNotFound) {
		t.Errorf("assign to unknown member: %v", err)
	}
	if err := repo.Delete(ctx, "r", "m2"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Assign(ctx, "r", mac, "m1"); err != nil {
		t.Fatal(err)
	}
	if m, ok, _ := repo.Get(ctx, "r", "m1"); !ok || len(m.MACs) != 1 {
		t.Errorf("after delete, device should be free to reassign: %+v", m)
	}

	cuts := sqlite.NewCutRepo(db)
	_ = cuts.RecordCut(ctx, "r", mac, "pay-1")
	if c, _ := cuts.Cuts(ctx, "r"); c[mac] != "pay-1" {
		t.Errorf("cuts = %v", c)
	}
	_ = cuts.RemoveCut(ctx, "r", mac)
	if c, _ := cuts.Cuts(ctx, "r"); len(c) != 0 {
		t.Errorf("cut not removed: %v", c)
	}
}

func TestPaymentRepo_MultiDeviceInvoice(t *testing.T) {
	ctx := context.Background()
	repo := sqlite.NewPaymentRepo(newDB(t))
	a, b := domain.MustMAC("aa:bb:cc:00:00:11"), domain.MustMAC("aa:bb:cc:00:00:22")
	inv := domain.Payment{ID: "inv", RouterID: "r", Kind: domain.PaymentInvoice, MemberID: "m1", Period: "2026-09",
		Bytes: 300, Status: domain.PaymentPaid, DueAt: time.Unix(5000, 0),
		Items: []domain.PaymentItem{{MAC: a, Bytes: 100}, {MAC: b, Bytes: 200}}}
	if err := repo.Create(ctx, inv); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.PaidBytes(ctx, "r", b, "2026-09"); n != 200 {
		t.Errorf("PaidBytes(b) = %d, want 200 (its own share only)", n)
	}
	got, _ := repo.ListByPeriod(ctx, "r", "2026-09", domain.PaymentInvoice)
	if len(got) != 1 || len(got[0].Items) != 2 || got[0].MemberID != "m1" || got[0].DueAt.Unix() != 5000 {
		t.Errorf("invoice = %+v", got)
	}
	if byMAC, _ := repo.ListByMAC(ctx, "r", a, 10); len(byMAC) != 1 {
		t.Errorf("ListByMAC(a) = %d payments, want the invoice", len(byMAC))
	}
}

func TestIdentityRepo_StatesAndCuts(t *testing.T) {
	ctx := context.Background()
	repo := sqlite.NewIdentityRepo(newDB(t))
	at := time.Unix(1_000_000, 0)
	if err := repo.SetBlacklisted(ctx, "r", "oppo", at); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkShared(ctx, "r", "galaxy"); err != nil {
		t.Fatal(err)
	}
	st, _ := repo.Identities(ctx, "r")
	if !st["oppo"].Blacklisted() || st["oppo"].Shared || !st["galaxy"].Shared {
		t.Fatalf("states = %+v", st)
	}
	_ = repo.Lift(ctx, "r", "oppo", at.Add(time.Hour))
	if st, _ := repo.Identities(ctx, "r"); st["oppo"].Blacklisted() {
		t.Error("lift not stored")
	}
	mac := domain.MustMAC("9a:35:a2:d2:24:15")
	_ = repo.RecordBlacklistCut(ctx, "r", mac, "oppo")
	if c, _ := repo.BlacklistCuts(ctx, "r"); c[mac] != "oppo" {
		t.Errorf("cuts = %v", c)
	}
	_ = repo.RemoveBlacklistCut(ctx, "r", mac)
	if c, _ := repo.BlacklistCuts(ctx, "r"); len(c) != 0 {
		t.Errorf("cut not removed: %v", c)
	}
}

func TestReminderRepo_Snooze(t *testing.T) {
	ctx := context.Background()
	repo := sqlite.NewReminderRepo(newDB(t))
	until := time.Unix(2_000_000, 0)
	if err := repo.Snooze(ctx, "r", "pay-1", until); err != nil {
		t.Fatal(err)
	}
	if s, _ := repo.Snoozes(ctx, "r"); !s["pay-1"].Equal(until) {
		t.Errorf("snoozes = %v", s)
	}
}
