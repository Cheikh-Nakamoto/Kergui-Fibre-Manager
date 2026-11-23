package sqlite_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// A backup moves what a household told Kergui on the PC to the phone
// (owner request, 2026-11-23): the PC's names and notes win, unknown devices
// are added, the earliest first sighting and the larger month are kept.

func mustMAC(t *testing.T, s string) domain.MAC {
	t.Helper()
	m, err := domain.ParseMAC(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func seedDevice(t *testing.T, db *sqlite.DB, router, mac, name, notes string, first time.Time) {
	t.Helper()
	m := mustMAC(t, mac)
	d := domain.Device{ID: router + "|" + m.String(), RouterID: router, MAC: m, CustomName: name, Notes: notes, FirstSeen: first, LastSeen: first}
	if _, err := sqlite.NewDeviceRepo(db).Upsert(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}

func seedUsage(t *testing.T, db *sqlite.DB, router, mac, period string, n int64) {
	t.Helper()
	if _, err := db.SQL().Exec(`INSERT INTO usage (router_id, mac, period, bytes) VALUES (?,?,?,?)
		ON CONFLICT(router_id, mac, period) DO UPDATE SET bytes=excluded.bytes`, router, mustMAC(t, mac).String(), period, n); err != nil {
		t.Fatal(err)
	}
}

func device(t *testing.T, db *sqlite.DB, router, mac string) domain.Device {
	t.Helper()
	d, ok, err := sqlite.NewDeviceRepo(db).FindByMAC(context.Background(), router, mustMAC(t, mac))
	if err != nil || !ok {
		t.Fatalf("device %s: ok=%v err=%v", mac, ok, err)
	}
	return d
}

func usageOf(t *testing.T, db *sqlite.DB, router, mac, period string) int64 {
	t.Helper()
	var n int64
	if err := db.SQL().QueryRow(`SELECT bytes FROM usage WHERE router_id=? AND mac=? AND period=?`,
		router, mustMAC(t, mac).String(), period).Scan(&n); err != nil {
		t.Fatalf("usage %s %s: %v", mac, period, err)
	}
	return n
}

func TestBackupRoundTripThePCWins(t *testing.T) {
	ctx := context.Background()
	early, late := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)

	pc := newDB(t)
	seedDevice(t, pc, "192-168-1-1", "aa:bb:cc:00:00:01", "Télé du salon", "achetée en 2024", early)
	seedDevice(t, pc, "192-168-1-1", "aa:bb:cc:00:00:02", "Phone de Awa", "", late)
	seedDevice(t, pc, "192-168-1-1", "aa:bb:cc:00:00:03", "", "", early)
	seedUsage(t, pc, "192-168-1-1", "aa:bb:cc:00:00:01", "2026-10", 5000)
	seedUsage(t, pc, "192-168-1-1", "aa:bb:cc:00:00:01", "2026-11", 100)
	archive, err := sqlite.ExportBackup(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}

	phone := newDB(t)
	const box = "192-168-1-1"
	seedDevice(t, phone, box, "aa:bb:cc:00:00:01", "TV", "note du téléphone", late)
	seedDevice(t, phone, box, "aa:bb:cc:00:00:03", "Nommé sur le téléphone", "", late)
	seedUsage(t, phone, box, "aa:bb:cc:00:00:01", "2026-11", 900)

	res, err := sqlite.ImportBackup(ctx, phone, box, archive)
	if err != nil {
		t.Fatal(err)
	}
	if res.Devices != 3 || res.Named != 2 || res.Usage != 2 || len(res.NamedMACs) != 2 {
		t.Fatalf("result = %+v", res)
	}
	tv := device(t, phone, box, "aa:bb:cc:00:00:01")
	if tv.CustomName != "Télé du salon" || tv.Notes != "achetée en 2024" {
		t.Errorf("the PC's name and notes did not win: %+v", tv)
	}
	if !tv.FirstSeen.Equal(early) {
		t.Errorf("first seen = %v, want the earlier %v", tv.FirstSeen, early)
	}
	if got := device(t, phone, box, "aa:bb:cc:00:00:03").CustomName; got != "Nommé sur le téléphone" {
		t.Errorf("an empty name on the PC erased the phone's: %q", got)
	}
	awa := device(t, phone, box, "aa:bb:cc:00:00:02")
	if awa.CustomName != "Phone de Awa" || awa.Connected {
		t.Errorf("a device only on the PC = %+v, want added, offline", awa)
	}
	if n := usageOf(t, phone, box, "aa:bb:cc:00:00:01", "2026-10"); n != 5000 {
		t.Errorf("October = %d, want the PC's 5000", n)
	}
	if n := usageOf(t, phone, box, "aa:bb:cc:00:00:01", "2026-11"); n != 900 {
		t.Errorf("November = %d, want the larger 900", n)
	}

	// Importing twice changes nothing more.
	if _, err := sqlite.ImportBackup(ctx, phone, box, archive); err != nil {
		t.Fatal(err)
	}
	if n := usageOf(t, phone, box, "aa:bb:cc:00:00:01", "2026-10"); n != 5000 {
		t.Errorf("October after a second import = %d", n)
	}
}

func TestBackupFromASpreadsheet(t *testing.T) {
	// Saved again by a spreadsheet: a BOM and « ; » separators.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("sauvegarde/appareils.csv")
	_, _ = w.Write([]byte("\xef\xbb\xbfmac;nom;notes;premiere_vue;fabricant;hote\nAA-BB-CC-00-00-09;Imprimante;;;;\n"))
	w, _ = zw.Create("sauvegarde/conso.csv")
	_, _ = w.Write([]byte("mac;mois;octets\naa:bb:cc:00:00:09;2026-11;42\n"))
	_ = zw.Close()

	db := newDB(t)
	res, err := sqlite.ImportBackup(context.Background(), db, "box", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if res.Named != 1 || device(t, db, "box", "aa:bb:cc:00:00:09").CustomName != "Imprimante" {
		t.Fatalf("result = %+v", res)
	}
	if n := usageOf(t, db, "box", "aa:bb:cc:00:00:09", "2026-11"); n != 42 {
		t.Fatalf("usage = %d", n)
	}
}

func TestBackupRefusesOtherFiles(t *testing.T) {
	db := newDB(t)
	for name, archive := range map[string][]byte{
		"not a zip": []byte("hello"),
		"zip without the CSVs": func() []byte {
			var buf bytes.Buffer
			zw := zip.NewWriter(&buf)
			w, _ := zw.Create("photo.txt")
			_, _ = w.Write([]byte("x"))
			_ = zw.Close()
			return buf.Bytes()
		}(),
	} {
		if _, err := sqlite.ImportBackup(context.Background(), db, "box", archive); !errors.Is(err, sqlite.ErrBadBackup) {
			t.Errorf("%s: err = %v, want ErrBadBackup", name, err)
		}
	}
}
