package backup_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/backup"
)

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func entries(t *testing.T, archive []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func mustRead(t *testing.T, archive []byte) backup.Archive {
	t.Helper()
	a, err := backup.Read(archive)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestReadSkipsBadRowsAndCanonicalisesMACs(t *testing.T) {
	a := mustRead(t, zipOf(t, map[string]string{
		"appareils.csv": "mac,nom,notes,premiere_vue,fabricant,hote\n" +
			"AA-BB-CC-00-00-01, Salon ,note,2026-01-02T03:04:05Z,Samsung,tv\n" +
			"not-a-mac,x,,,,\n" +
			"aa:bb:cc:00:00:02\n",
		"conso.csv": "mac,mois,octets\n" +
			"aa:bb:cc:00:00:01,2026-10,100\n" +
			"aa:bb:cc:00:00:01,octobre,5\n" +
			"aa:bb:cc:00:00:01,2026-11,-1\n" +
			"zz,2026-11,1\n",
	}))
	if len(a.Devices) != 2 {
		t.Fatalf("devices = %+v", a.Devices)
	}
	d := a.Devices[0]
	if d.MAC != "aa:bb:cc:00:00:01" || d.Name != "Salon" || d.Notes != "note" || d.Vendor != "Samsung" || d.Hostname != "tv" {
		t.Errorf("device = %+v", d)
	}
	if d.FirstSeen != time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Unix() {
		t.Errorf("first seen = %d", d.FirstSeen)
	}
	if a.Devices[1].MAC != "aa:bb:cc:00:00:02" || a.Devices[1].Name != "" {
		t.Errorf("short row = %+v", a.Devices[1])
	}
	if len(a.Usage) != 1 || a.Usage[0] != (backup.Usage{MAC: "aa:bb:cc:00:00:01", Month: "2026-10", Bytes: 100}) {
		t.Errorf("usage = %+v", a.Usage)
	}
}

func TestReadRefusesWhatIsNotABackup(t *testing.T) {
	for name, archive := range map[string][]byte{
		"not a zip":     []byte("hello"),
		"no conso":      zipOf(t, map[string]string{"appareils.csv": "mac,nom\n"}),
		"wrong header":  zipOf(t, map[string]string{"appareils.csv": "foo\n", "conso.csv": "mac,mois,octets\n"}),
		"empty devices": zipOf(t, map[string]string{"appareils.csv": "", "conso.csv": "mac,mois,octets\n"}),
	} {
		if _, err := backup.Read(archive); !errors.Is(err, backup.ErrBadBackup) {
			t.Errorf("%s: err = %v, want ErrBadBackup", name, err)
		}
	}
}

func TestReadAcceptsASpreadsheetsSave(t *testing.T) {
	a := mustRead(t, zipOf(t, map[string]string{
		"sauvegarde/appareils.csv": "\xef\xbb\xbfmac;nom;notes;premiere_vue;fabricant;hote\naa:bb:cc:00:00:01;Télé;;;;\n",
		"sauvegarde/conso.csv":     "mac;mois;octets\naa:bb:cc:00:00:01;2026-10;7\n",
	}))
	if len(a.Devices) != 1 || a.Devices[0].Name != "Télé" || len(a.Usage) != 1 || a.Usage[0].Bytes != 7 {
		t.Fatalf("archive = %+v", a)
	}
}

// The written archive is the canonical one: only the two files and their
// columns, sorted, so nothing else a sender put in it (a password, another
// file, another column) survives a round trip.
func TestWriteKeepsOnlyTheBackupsColumns(t *testing.T) {
	in := zipOf(t, map[string]string{
		"appareils.csv": "mac,nom,notes,premiere_vue,fabricant,hote,mot_de_passe\n" +
			"aa:bb:cc:00:00:02,B,,,,,s3cret\n" +
			"aa:bb:cc:00:00:01,A,n,2026-01-02T03:04:05Z,V,h,s3cret\n",
		"conso.csv":  "mac,mois,octets\naa:bb:cc:00:00:01,2026-11,2\naa:bb:cc:00:00:01,2026-10,1\n",
		"vault.json": `{"password":"s3cret"}`,
	})
	out, err := mustRead(t, in).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got := entries(t, out)
	if len(got) != 2 {
		t.Fatalf("entries = %v", got)
	}
	wantDevices := "mac,nom,notes,premiere_vue,fabricant,hote\n" +
		"aa:bb:cc:00:00:01,A,n,2026-01-02T03:04:05Z,V,h\n" +
		"aa:bb:cc:00:00:02,B,,,,\n"
	if got[backup.DevicesFile] != wantDevices {
		t.Errorf("appareils.csv =\n%s\nwant\n%s", got[backup.DevicesFile], wantDevices)
	}
	wantUsage := "mac,mois,octets\naa:bb:cc:00:00:01,2026-10,1\naa:bb:cc:00:00:01,2026-11,2\n"
	if got[backup.UsageFile] != wantUsage {
		t.Errorf("conso.csv =\n%s\nwant\n%s", got[backup.UsageFile], wantUsage)
	}
	if strings.Contains(string(out), "s3cret") {
		t.Error("the password survived the round trip")
	}
	again, err := mustRead(t, out).Bytes()
	if err != nil || !bytes.Equal(again, out) {
		t.Error("a canonical archive does not read and write back to itself")
	}
}

// Merge applies the import's rules (owner decision, 2026-11-23): the
// incoming backup's names and notes win, an empty one leaves them; a device
// only one side knows is kept; the earliest first sighting; the larger month.
func TestMergeFollowsTheImportRules(t *testing.T) {
	jan, mar := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix(), time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).Unix()
	stored := backup.Archive{
		Devices: []backup.Device{
			{MAC: "aa:bb:cc:00:00:01", Name: "Télé", Notes: "salon", FirstSeen: mar, Vendor: "LG", Hostname: "tv"},
			{MAC: "aa:bb:cc:00:00:02", Name: "Tablette", FirstSeen: jan},
			{MAC: "aa:bb:cc:00:00:03", Name: "Console"},
		},
		Usage: []backup.Usage{
			{MAC: "aa:bb:cc:00:00:01", Month: "2026-10", Bytes: 500},
			{MAC: "aa:bb:cc:00:00:01", Month: "2026-11", Bytes: 10},
		},
	}
	incoming := backup.Archive{
		Devices: []backup.Device{
			{MAC: "aa:bb:cc:00:00:01", Name: "Télé du salon", FirstSeen: jan, Vendor: "Other", Hostname: "other"},
			{MAC: "aa:bb:cc:00:00:02", Name: "", Notes: "à Awa", FirstSeen: mar, Hostname: "tab"},
			{MAC: "aa:bb:cc:00:00:04", Name: "PC", FirstSeen: mar},
		},
		Usage: []backup.Usage{
			{MAC: "aa:bb:cc:00:00:01", Month: "2026-10", Bytes: 300},
			{MAC: "aa:bb:cc:00:00:01", Month: "2026-11", Bytes: 40},
			{MAC: "aa:bb:cc:00:00:04", Month: "2026-11", Bytes: 9},
		},
	}
	got := backup.Merge(stored, incoming)
	want := backup.Archive{
		Devices: []backup.Device{
			// The incoming name wins, the stored notes stay (none came);
			// the earliest first sighting; vendor and host keep the first known.
			{MAC: "aa:bb:cc:00:00:01", Name: "Télé du salon", Notes: "salon", FirstSeen: jan, Vendor: "LG", Hostname: "tv"},
			// An empty name leaves the stored one; the notes come in.
			{MAC: "aa:bb:cc:00:00:02", Name: "Tablette", Notes: "à Awa", FirstSeen: jan, Hostname: "tab"},
			// Only the stored side knew it: kept.
			{MAC: "aa:bb:cc:00:00:03", Name: "Console"},
			// Only the incoming side knew it: added.
			{MAC: "aa:bb:cc:00:00:04", Name: "PC", FirstSeen: mar},
		},
		Usage: []backup.Usage{
			{MAC: "aa:bb:cc:00:00:01", Month: "2026-10", Bytes: 500},
			{MAC: "aa:bb:cc:00:00:01", Month: "2026-11", Bytes: 40},
			{MAC: "aa:bb:cc:00:00:04", Month: "2026-11", Bytes: 9},
		},
	}
	if len(got.Devices) != len(want.Devices) || len(got.Usage) != len(want.Usage) {
		t.Fatalf("merge =\n%+v\nwant\n%+v", got, want)
	}
	for i := range want.Devices {
		if got.Devices[i] != want.Devices[i] {
			t.Errorf("device %d = %+v, want %+v", i, got.Devices[i], want.Devices[i])
		}
	}
	for i := range want.Usage {
		if got.Usage[i] != want.Usage[i] {
			t.Errorf("usage %d = %+v, want %+v", i, got.Usage[i], want.Usage[i])
		}
	}
}

// A device listed twice in one archive folds the way a sequential import
// does: a later non-empty name wins.
func TestMergeFoldsDuplicatesInOrder(t *testing.T) {
	got := backup.Merge(backup.Archive{}, backup.Archive{Devices: []backup.Device{
		{MAC: "aa:bb:cc:00:00:01", Name: "A"},
		{MAC: "aa:bb:cc:00:00:01", Name: "B"},
		{MAC: "aa:bb:cc:00:00:01", Name: ""},
	}})
	if len(got.Devices) != 1 || got.Devices[0].Name != "B" {
		t.Fatalf("devices = %+v", got.Devices)
	}
}
