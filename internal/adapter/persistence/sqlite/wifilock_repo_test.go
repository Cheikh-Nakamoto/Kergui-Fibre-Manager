package sqlite_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// The closed-Wi-Fi intent (RFC-007 D7) and the « déconnecté du Wi-Fi »
// flags (D5), per box.
func TestWiFiLockRepo_Intent(t *testing.T) {
	ctx := context.Background()
	repo := sqlite.NewWiFiLockRepo(newDB(t))
	if got, err := repo.WiFiLockIntent(ctx, "r"); err != nil || !reflect.DeepEqual(got, domain.WiFiLockIntent{}) {
		t.Fatalf("never saved = %+v, %v; want open, nothing else", got, err)
	}
	closed := domain.WiFiLockIntent{
		Closed:         true,
		HouseholdAPs:   []string{"DEV.WIFI.AP1", "DEV.WIFI.AP5"},
		VisitorAPs:     []string{"DEV.WIFI.AP7"},
		VisitorsClosed: true,
		WindowUntil:    time.Unix(1_800_000_300, 0),
		Since:          time.Unix(1_800_000_000, 0),
	}
	if err := repo.SaveWiFiLockIntent(ctx, "r", closed); err != nil {
		t.Fatal(err)
	}
	got, err := repo.WiFiLockIntent(ctx, "r")
	if err != nil || !intentEqual(got, closed) {
		t.Fatalf("read back = %+v, %v; want %+v", got, err, closed)
	}
	open := domain.WiFiLockIntent{HouseholdAPs: []string{"DEV.WIFI.AP1"}, Since: time.Unix(1_800_000_600, 0)}
	if err := repo.SaveWiFiLockIntent(ctx, "r", open); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.WiFiLockIntent(ctx, "r"); !intentEqual(got, open) {
		t.Fatalf("after opening = %+v, want %+v", got, open)
	}
	if other, _ := repo.WiFiLockIntent(ctx, "other"); other.Closed || len(other.HouseholdAPs) != 0 {
		t.Fatal("the intent leaked to another box")
	}
}

func intentEqual(a, b domain.WiFiLockIntent) bool {
	return a.Closed == b.Closed && a.VisitorsClosed == b.VisitorsClosed &&
		a.WindowUntil.Equal(b.WindowUntil) && a.Since.Equal(b.Since) &&
		reflect.DeepEqual(nonNil(a.HouseholdAPs), nonNil(b.HouseholdAPs)) &&
		reflect.DeepEqual(nonNil(a.VisitorAPs), nonNil(b.VisitorAPs))
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func TestWiFiLockRepo_WiFiCut(t *testing.T) {
	ctx := context.Background()
	repo := sqlite.NewWiFiLockRepo(newDB(t))
	tv, tab := domain.MustMAC("aa:bb:cc:00:00:01"), domain.MustMAC("02:00:00:00:00:02")
	if macs, err := repo.WiFiCutMACs(ctx, "r"); err != nil || len(macs) != 0 {
		t.Fatalf("none yet = %v, %v", macs, err)
	}
	for _, m := range []domain.MAC{tab, tv, tv} {
		if err := repo.SetWiFiCut(ctx, "r", m, true); err != nil {
			t.Fatal(err)
		}
	}
	macs, err := repo.WiFiCutMACs(ctx, "r")
	if err != nil || len(macs) != 2 || !macs[0].Equal(tab) || !macs[1].Equal(tv) {
		t.Fatalf("flags = %v, %v; want [%s %s], sorted, once each", macs, err, tab, tv)
	}
	if err := repo.SetWiFiCut(ctx, "r", tab, false); err != nil {
		t.Fatal(err)
	}
	if macs, _ := repo.WiFiCutMACs(ctx, "r"); len(macs) != 1 || !macs[0].Equal(tv) {
		t.Fatalf("after clearing one = %v", macs)
	}
	if macs, _ := repo.WiFiCutMACs(ctx, "other"); len(macs) != 0 {
		t.Fatal("the flags leaked to another box")
	}
	if err := repo.SetWiFiCut(ctx, "r", domain.MAC{}, true); err == nil {
		t.Fatal("an unknown MAC was flagged")
	}
}

// A database from before the closed Wi-Fi gets its tables on the next open,
// and keeps what it held.
func TestMigration_WiFiLockOnAnOlderDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kergui.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.NewRulePurgeRepo(db).SaveRulePurge(ctx, "r", time.Unix(1_800_000_000, 0)); err != nil {
		t.Fatal(err)
	}
	// Back to the schema before 0013.
	for _, q := range []string{
		`DROP TABLE wifi_lock`,
		`DROP TABLE wifi_cut_devices`,
		`DELETE FROM schema_migrations WHERE name='0013_wifi_lock.sql'`,
	} {
		if _, err := db.SQL().Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = db.Close()

	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()
	repo := sqlite.NewWiFiLockRepo(db)
	if got, err := repo.WiFiLockIntent(ctx, "r"); err != nil || got.Closed {
		t.Fatalf("intent after the migration = %+v, %v; want open", got, err)
	}
	if err := repo.SaveWiFiLockIntent(ctx, "r", domain.WiFiLockIntent{Closed: true}); err != nil {
		t.Fatal(err)
	}
	if at, _ := sqlite.NewRulePurgeRepo(db).LastRulePurge(ctx, "r"); at.Unix() != 1_800_000_000 {
		t.Fatalf("the older data was lost: %v", at)
	}
	// And once more: a migrated database opens as it is.
	_ = db.Close()
	db2, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if got, _ := sqlite.NewWiFiLockRepo(db2).WiFiLockIntent(ctx, "r"); !got.Closed {
		t.Fatal("the intent did not survive a reopen")
	}
}
