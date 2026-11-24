package mobilecore

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The PC's backup imported on the phone (owner request, 2026-11-23).
func TestImportBackupNamesAndApproves(t *testing.T) {
	c := approvalDemo(t)
	approvedList(t, c)
	call(t, c, "DELETE", "/api/approvals/"+laptop, "")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("appareils.csv")
	_, _ = w.Write([]byte("mac,nom,notes,premiere_vue,fabricant,hote\nAC:BB:CC:00:00:22,PC du bureau,,,,\n"))
	w, _ = zw.Create("conso.csv")
	_, _ = w.Write([]byte("mac,mois,octets\n"))
	_ = zw.Close()
	body, _ := json.Marshal(map[string]string{"archive": base64.StdEncoding.EncodeToString(buf.Bytes())})

	status, out := call(t, c, "POST", "/api/import", string(body))
	if status != http.StatusOK {
		t.Fatalf("import = %d %s", status, out)
	}
	d := approvedList(t, c)[laptop]
	if d.CustomName != "PC du bureau" || !d.Approved {
		t.Fatalf("after import = %+v, want named and approved", d)
	}

	bad, _ := json.Marshal(map[string]string{"archive": base64.StdEncoding.EncodeToString([]byte("not a zip"))})
	if status, out := call(t, c, "POST", "/api/import", string(bad)); status != http.StatusBadRequest {
		t.Fatalf("import of a non-backup = %d %s, want 400", status, out)
	}
}

// A backup made on the phone imports again: the round trip of a phone change.
func TestExportThenImport(t *testing.T) {
	c := approvalDemo(t)
	approvedList(t, c)
	call(t, c, "PATCH", "/api/devices/"+laptop, `{"custom_name":"Bureau"}`)
	status, out := call(t, c, "GET", "/api/export", "")
	if status != http.StatusOK {
		t.Fatalf("export = %d %s", status, out)
	}
	var exp struct{ Archive, Name string }
	if err := json.Unmarshal([]byte(out), &exp); err != nil || exp.Archive == "" || exp.Name == "" {
		t.Fatalf("export body = %s (%v)", out, err)
	}

	other := approvalDemo(t)
	approvedList(t, other)
	body, _ := json.Marshal(map[string]string{"archive": exp.Archive})
	if status, out := call(t, other, "POST", "/api/import", string(body)); status != http.StatusOK {
		t.Fatalf("import = %d %s", status, out)
	}
	if d := approvedList(t, other)[laptop]; d.CustomName != "Bureau" {
		t.Fatalf("after import on the new phone = %+v", d)
	}
}

// The automatic backup to the hub (issue #89): the export names the box the
// hub keys it by, and the archive is only the two CSVs — never the box's
// password, the vault or a key.
func TestExportNamesTheBoxAndHoldsNoSecret(t *testing.T) {
	c := approvalDemo(t)
	approvedList(t, c)
	call(t, c, "PATCH", "/api/devices/"+laptop, `{"custom_name":"Bureau","notes":"chez moi"}`)
	status, out := call(t, c, "GET", "/api/export", "")
	if status != http.StatusOK {
		t.Fatalf("export = %d %s", status, out)
	}
	var exp struct{ Archive, Name, BoxID string }
	if err := json.Unmarshal([]byte(out), &exp); err != nil {
		t.Fatal(err)
	}
	if exp.BoxID == "" || exp.BoxID != c.boxID() || !regexp.MustCompile(`^[a-z0-9-]{1,128}$`).MatchString(exp.BoxID) {
		t.Fatalf("boxId = %q, want the core's %q as a slug", exp.BoxID, c.boxID())
	}
	raw, err := base64.StdEncoding.DecodeString(exp.Archive)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		for _, secret := range []string{DemoPassword, "password", "vault", "ed25519", "x25519"} {
			if strings.Contains(strings.ToLower(string(b)), strings.ToLower(secret)) {
				t.Errorf("%s holds %q", f.Name, secret)
			}
		}
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "appareils.csv,conso.csv" {
		t.Fatalf("archive files = %v, want appareils.csv and conso.csv only", names)
	}
}
