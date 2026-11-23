package mobilecore

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
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
