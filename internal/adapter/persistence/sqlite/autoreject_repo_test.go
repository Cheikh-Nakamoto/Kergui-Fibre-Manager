package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

func TestAutoRejectRepo(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	repo := sqlite.NewAutoRejectRepo(db)

	if s, err := repo.AutoRejectSettings(ctx, "r"); err != nil || s.Enabled {
		t.Fatalf("default settings = %+v, %v; want off", s, err)
	}
	since := time.Unix(1_800_000_000, 0)
	if err := repo.SaveAutoRejectSettings(ctx, "r", domain.AutoRejectSettings{Enabled: true, Since: since}); err != nil {
		t.Fatal(err)
	}
	s, err := repo.AutoRejectSettings(ctx, "r")
	if err != nil || !s.Enabled || !s.Since.Equal(since) {
		t.Fatalf("settings = %+v, %v", s, err)
	}
	if other, _ := repo.AutoRejectSettings(ctx, "other"); other.Enabled {
		t.Fatal("the setting leaked to another box")
	}

	mac := domain.MustMAC("aa:bb:cc:00:00:01")
	d := domain.AutoRejectDevice{MAC: mac, FirstSeen: since, State: domain.AutoRejectPending}
	if err := repo.SaveAutoRejectDevice(ctx, "r", d); err != nil {
		t.Fatal(err)
	}
	d.State, d.DecidedAt = domain.AutoRejectRefused, since.Add(15*time.Minute)
	if err := repo.SaveAutoRejectDevice(ctx, "r", d); err != nil {
		t.Fatal(err)
	}
	got, err := repo.AutoRejectDevices(ctx, "r")
	if err != nil || len(got) != 1 {
		t.Fatalf("devices = %+v, %v", got, err)
	}
	if g := got[mac]; g.State != domain.AutoRejectRefused || !g.FirstSeen.Equal(since) || !g.DecidedAt.Equal(d.DecidedAt) {
		t.Fatalf("device = %+v", g)
	}
	if err := repo.ClearAutoRejectDevices(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.AutoRejectDevices(ctx, "r"); len(got) != 0 {
		t.Fatalf("after clear: %+v", got)
	}
}
