package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
)

func TestRulePurgeRepo(t *testing.T) {
	ctx := context.Background()
	repo := sqlite.NewRulePurgeRepo(newDB(t))
	if at, err := repo.LastRulePurge(ctx, "r"); err != nil || !at.IsZero() {
		t.Fatalf("never purged = %v, %v; want zero", at, err)
	}
	for _, at := range []time.Time{time.Unix(1_800_000_000, 0), time.Unix(1_800_086_400, 0)} {
		if err := repo.SaveRulePurge(ctx, "r", at); err != nil {
			t.Fatal(err)
		}
		if got, err := repo.LastRulePurge(ctx, "r"); err != nil || !got.Equal(at) {
			t.Fatalf("last purge = %v, %v; want %v", got, err, at)
		}
	}
	if other, _ := repo.LastRulePurge(ctx, "other"); !other.IsZero() {
		t.Fatal("the purge date leaked to another box")
	}
}
