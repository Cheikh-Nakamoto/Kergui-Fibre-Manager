package sqlite_test

import (
	"context"
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
