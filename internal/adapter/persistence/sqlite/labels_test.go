package sqlite_test

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// Issue #95: the names and notes carry when they were set, so the owner's
// phone and the relay can exchange them and the later edit wins.

func str(s string) *string { return &s }

var labelT0 = time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)

func labelOf(t *testing.T, repo *sqlite.DeviceRepo, router, mac string) (domain.Device, bool) {
	t.Helper()
	d, ok, err := repo.FindByMAC(context.Background(), router, mustMAC(t, mac))
	if err != nil {
		t.Fatal(err)
	}
	return d, ok
}

func TestLabels_LastWriteWins(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	repo := sqlite.NewDeviceRepo(db)
	const mac = "ac:bb:cc:00:00:22"
	seedDevice(t, db, "r", mac, "", "", labelT0)
	m := mustMAC(t, mac)

	// A label from elsewhere, newer than nothing: applied, with its time.
	changed, err := repo.ApplyLabels(ctx, "r", []domain.Label{{MAC: m, Name: str("Tablette de Awa"), Notes: str("salon"), At: labelT0.Add(2 * time.Minute)}})
	if err != nil || len(changed) != 1 {
		t.Fatalf("first apply: changed %v, err %v", changed, err)
	}
	d, _ := labelOf(t, repo, "r", mac)
	if d.CustomName != "Tablette de Awa" || d.Notes != "salon" || !d.LabelAt.Equal(labelT0.Add(2*time.Minute)) {
		t.Fatalf("after apply: %q %q %v", d.CustomName, d.Notes, d.LabelAt)
	}

	// An older edit arriving late loses; the same one twice changes nothing.
	for _, l := range []domain.Label{
		{MAC: m, Name: str("Vieux nom"), At: labelT0.Add(time.Minute)},
		{MAC: m, Name: str("Autre nom"), At: labelT0.Add(2 * time.Minute)}, // a tie keeps what is here
	} {
		changed, err := repo.ApplyLabels(ctx, "r", []domain.Label{l})
		if err != nil || len(changed) != 0 {
			t.Fatalf("stale label %+v applied: %v %v", l, changed, err)
		}
	}
	if d, _ := labelOf(t, repo, "r", mac); d.CustomName != "Tablette de Awa" {
		t.Fatalf("an older edit won: %q", d.CustomName)
	}

	// A later rename without notes keeps the notes.
	if _, err := repo.ApplyLabels(ctx, "r", []domain.Label{{MAC: m, Name: str("Tablette"), At: labelT0.Add(3 * time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	if d, _ := labelOf(t, repo, "r", mac); d.CustomName != "Tablette" || d.Notes != "salon" {
		t.Fatalf("after a rename alone: %q %q", d.CustomName, d.Notes)
	}

	// A device never seen here gets its label anyway, offline: its name is
	// there the day it joins.
	const other = "ac:bb:cc:00:00:99"
	if _, err := repo.ApplyLabels(ctx, "r", []domain.Label{{MAC: mustMAC(t, other), Name: str("TV"), At: labelT0}}); err != nil {
		t.Fatal(err)
	}
	if d, ok := labelOf(t, repo, "r", other); !ok || d.CustomName != "TV" || d.Connected {
		t.Fatalf("unknown device: ok %v, %+v", ok, d)
	}

	labels, err := repo.Labels(ctx, "r")
	if err != nil || len(labels) != 2 {
		t.Fatalf("Labels = %+v, %v", labels, err)
	}
}

func TestLabels_RenameHereIsStampedAndNeverEarlier(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	now := labelT0
	repo := sqlite.NewDeviceRepo(db).WithClock(func() time.Time { return now })
	const mac = "ac:bb:cc:00:00:22"
	seedDevice(t, db, "r", mac, "", "", labelT0)
	m := mustMAC(t, mac)

	if err := repo.SetCustomName(ctx, "r", m, "Laptop"); err != nil {
		t.Fatal(err)
	}
	if d, _ := labelOf(t, repo, "r", mac); !d.LabelAt.Equal(labelT0) {
		t.Fatalf("rename stamped %v, want %v", d.LabelAt, labelT0)
	}

	// A label from a phone whose clock runs ahead came in; a rename made
	// here afterwards is still the later one.
	ahead := labelT0.Add(time.Hour)
	if _, err := repo.ApplyLabels(ctx, "r", []domain.Label{{MAC: m, Name: str("Ahead"), At: ahead}}); err != nil {
		t.Fatal(err)
	}
	now = labelT0.Add(time.Minute)
	if err := repo.SetCustomName(ctx, "r", m, "Laptop de Moussa"); err != nil {
		t.Fatal(err)
	}
	d, _ := labelOf(t, repo, "r", mac)
	if d.CustomName != "Laptop de Moussa" || !d.LabelAt.After(ahead) {
		t.Fatalf("rename after a fast label: %q at %v, want after %v", d.CustomName, d.LabelAt, ahead)
	}
}

func TestLabels_ImportStampsWhatItChanges(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	repo := sqlite.NewDeviceRepo(db)
	seedDevice(t, db, "r", "ac:bb:cc:00:00:22", "Same", "", labelT0)
	seedDevice(t, db, "r", "ac:bb:cc:00:00:33", "Old", "", labelT0)
	archive := buildArchive(t,
		"mac,nom,notes,premiere_vue,fabricant,hote\nac:bb:cc:00:00:22,Same,,,,\nac:bb:cc:00:00:33,New,,,,\n",
		"mac,mois,octets\n")
	if _, err := sqlite.ImportBackup(ctx, db, "r", archive); err != nil {
		t.Fatal(err)
	}
	if d, _ := labelOf(t, repo, "r", "ac:bb:cc:00:00:22"); !d.LabelAt.IsZero() {
		t.Errorf("an unchanged name was stamped: %v", d.LabelAt)
	}
	if d, _ := labelOf(t, repo, "r", "ac:bb:cc:00:00:33"); d.CustomName != "New" || d.LabelAt.IsZero() {
		t.Errorf("a changed name: %q at %v, want stamped", d.CustomName, d.LabelAt)
	}
}

func buildArchive(t *testing.T, devices, usage string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"appareils.csv": devices, "conso.csv": usage} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
