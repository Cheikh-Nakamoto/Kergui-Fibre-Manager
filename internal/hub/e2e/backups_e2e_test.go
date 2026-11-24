package e2e

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/backup"
)

// backupArchive is a Kergui backup as `kergui export` writes it.
func backupArchive(t *testing.T, devices []backup.Device, usage []backup.Usage) []byte {
	t.Helper()
	b, err := backup.Archive{Devices: devices, Usage: usage}.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// unzipped is every entry of an archive, by name.
func unzipped(t *testing.T, archive []byte) map[string]string {
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

// downloaded is GET router/backups/{box} as a phone reads it: the archive.
func downloaded(t *testing.T, call func(method, path string, in any) (int, map[string]any), path string) []byte {
	t.Helper()
	st, body := call(http.MethodGet, path, nil)
	if st != http.StatusOK {
		t.Fatalf("GET %s = %d %v", path, st, body)
	}
	raw, err := base64.StdEncoding.DecodeString(str(body, "archive"))
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	return raw
}

// relayPut is PUT /relay/v1/backups/{box} with a relay's kga_ token.
func relayPut(t *testing.T, hubURL, token, box string, archive []byte) (int, map[string]any) {
	t.Helper()
	return callFrom(t, hubURL, "198.51.100.40", http.MethodPut, "/relay/v1/backups/"+box, token,
		map[string]any{"archive": base64.StdEncoding.EncodeToString(archive)})
}

// TestE2E_BackupsPerHouseholdAndBox is issue #88 end to end: the owner's
// phone sends the household's backup of a box, a second owner phone gets
// the same archive back; a member reads it but cannot write it; another
// household cannot read it; the household's PC relay sends its own and the
// hub merges the two with the import's rules. A password slipped into an
// archive never reaches the hub's database.
func TestE2E_BackupsPerHouseholdAndBox(t *testing.T) {
	ownerNumber := "+22176" + randomDigits(t, 7)
	memberNumber := "+22175" + randomDigits(t, 7)
	strangerNumber := "+22177" + randomDigits(t, 7)
	const box, otherBox = "192-168-1-1", "192-168-100-1"
	secret := "pw-" + randomDigits(t, 12)

	clk := &fakeClock{now: time.Now().UTC()}
	w := newWorldWith(t, fastTimings, withPhoneOwner(ownerNumber), withClock(clk))
	owner := w.owner
	owner.registerNetwork(w.boxPin)
	owner.publishBundle(w.boxPin)
	path := owner.tenantPath("router/backups/" + box)

	// Nothing yet: 404 for the box, an empty list.
	if st, body := owner.call(http.MethodGet, path, nil); st != http.StatusNotFound {
		t.Fatalf("GET before any backup = %d %v, want 404", st, body)
	}

	// 1. The owner's phone sends its backup.
	jan := time.Date(2026, 1, 5, 8, 0, 0, 0, time.UTC).Unix()
	mar := time.Date(2026, 3, 5, 8, 0, 0, 0, time.UTC).Unix()
	phoneBackup := backupArchive(t,
		[]backup.Device{
			{MAC: "aa:bb:cc:00:00:01", Name: "Télé", Notes: "salon", FirstSeen: mar, Vendor: "LG", Hostname: "tv"},
			{MAC: "aa:bb:cc:00:00:02", Name: "Tablette d'Awa", FirstSeen: jan},
		},
		[]backup.Usage{
			{MAC: "aa:bb:cc:00:00:01", Month: "2026-10", Bytes: 500},
			{MAC: "aa:bb:cc:00:00:01", Month: "2026-11", Bytes: 10},
		})
	st, body := owner.call(http.MethodPut, path, map[string]any{"archive": base64.StdEncoding.EncodeToString(phoneBackup)})
	if st != http.StatusOK || body["merged"] != false || body["devices"] != float64(2) || body["months"] != float64(2) {
		t.Fatalf("owner PUT = %d %v", st, body)
	}
	if src, _ := body["source"].(map[string]any); str(src, "kind") != "phone" || str(src, "id") != owner.userID {
		t.Fatalf("source = %v", body["source"])
	}

	// 2. A second phone of the owner (same WhatsApp number) gets it back,
	// byte for byte.
	clk.advance(2 * time.Minute) // past the code resend delay
	second := otpSignIn(t, w.hub.URL, w.notifier, "198.51.100.31", ownerNumber, "second owner phone")
	if second.tenantID != owner.tenantID {
		t.Fatalf("the second phone signed in to %s, want %s", second.tenantID, owner.tenantID)
	}
	secondCall := func(method, p string, in any) (int, map[string]any) {
		return callFrom(t, w.hub.URL, "198.51.100.31", method, p, second.access, in)
	}
	if got := downloaded(t, secondCall, path); !bytes.Equal(got, phoneBackup) {
		t.Fatalf("the second owner phone downloaded another archive:\n%v", unzipped(t, got))
	}
	if st, body := secondCall(http.MethodGet, owner.tenantPath("router/backups"), nil); st != http.StatusOK {
		t.Fatalf("list = %d %v", st, body)
	} else if list, _ := body["backups"].([]any); len(list) != 1 || str(list[0].(map[string]any), "boxId") != box {
		t.Fatalf("list = %v", body)
	}

	// 3. A member reads it, and cannot write it.
	if st, body := owner.call(http.MethodPost, membersPath(owner.tenantID, "invites"), map[string]any{"phone": memberNumber, "role": "viewer"}); st != http.StatusCreated {
		t.Fatalf("invite = %d %v", st, body)
	}
	m := otpSignIn(t, w.hub.URL, w.notifier, "198.51.100.32", memberNumber, "Tecno Spark")
	if m.tenantID != owner.tenantID {
		t.Fatalf("the member signed in to %s, want %s", m.tenantID, owner.tenantID)
	}
	memberCall := func(method, p string, in any) (int, map[string]any) {
		return callFrom(t, w.hub.URL, "198.51.100.32", method, p, m.access, in)
	}
	if got := downloaded(t, memberCall, path); !bytes.Equal(got, phoneBackup) {
		t.Fatal("the member downloaded another archive")
	}
	if st, body := memberCall(http.MethodPut, path, map[string]any{"archive": base64.StdEncoding.EncodeToString(phoneBackup)}); st != http.StatusForbidden {
		t.Fatalf("a member's PUT = %d %v, want 403", st, body)
	}

	// 4. Another household reads nothing of it, and its own box is empty.
	stranger := otpSignIn(t, w.hub.URL, w.notifier, "198.51.100.33", strangerNumber, "stranger")
	if stranger.tenantID == owner.tenantID {
		t.Fatal("the stranger signed in to the owner's household")
	}
	strangerCall := func(method, p string, in any) (int, map[string]any) {
		return callFrom(t, w.hub.URL, "198.51.100.33", method, p, stranger.access, in)
	}
	for _, p := range []string{path, owner.tenantPath("router/backups")} {
		if st, body := strangerCall(http.MethodGet, p, nil); st != http.StatusForbidden {
			t.Fatalf("another household's GET %s = %d %v, want 403", p, st, body)
		}
	}
	if st, _ := strangerCall(http.MethodPut, path, map[string]any{"archive": base64.StdEncoding.EncodeToString(phoneBackup)}); st != http.StatusForbidden {
		t.Fatalf("another household's PUT = %d, want 403", st)
	}
	if st, _ := strangerCall(http.MethodGet, "/api/v1/tenants/"+stranger.tenantID+"/router/backups/"+box, nil); st != http.StatusNotFound {
		t.Fatalf("the stranger's own box = %d, want 404", st)
	}

	// 5. The household's PC relay sends its backup of the same box, with a
	// password slipped in (a column and a file): the hub merges, the
	// backup's names win, unknown devices are added, the earliest first
	// sighting and the larger month are kept, and the password is dropped.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	state, _, err := agent.Enroll(ctx, w.hub.URL, owner.invite("pc"), "PC du salon", "pc", http.DefaultClient)
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	pcBackup := zipFiles(t, map[string]string{
		"appareils.csv": "mac,nom,notes,premiere_vue,fabricant,hote,mot_de_passe\n" +
			"AA:BB:CC:00:00:01,Télé du salon,,2026-01-05T08:00:00Z,Other,other," + secret + "\n" +
			"aa:bb:cc:00:00:03,PC,,2026-03-05T08:00:00Z,,,\n",
		"conso.csv":  "mac,mois,octets\naa:bb:cc:00:00:01,2026-10,300\naa:bb:cc:00:00:01,2026-11,40\n",
		"vault.json": `{"password":"` + secret + `"}`,
	})
	st, body = relayPut(t, w.hub.URL, state.Token, box, pcBackup)
	if st != http.StatusOK || body["merged"] != true || body["devices"] != float64(3) {
		t.Fatalf("relay PUT = %d %v", st, body)
	}
	if src, _ := body["source"].(map[string]any); str(src, "kind") != "relay" || str(src, "id") != state.AgentID {
		t.Fatalf("source = %v", body["source"])
	}
	merged := downloaded(t, secondCall, path)
	want := backupArchive(t,
		[]backup.Device{
			{MAC: "aa:bb:cc:00:00:01", Name: "Télé du salon", Notes: "salon", FirstSeen: jan, Vendor: "LG", Hostname: "tv"},
			{MAC: "aa:bb:cc:00:00:02", Name: "Tablette d'Awa", FirstSeen: jan},
			{MAC: "aa:bb:cc:00:00:03", Name: "PC", FirstSeen: mar},
		},
		[]backup.Usage{
			{MAC: "aa:bb:cc:00:00:01", Month: "2026-10", Bytes: 500},
			{MAC: "aa:bb:cc:00:00:01", Month: "2026-11", Bytes: 40},
		})
	if !bytes.Equal(merged, want) {
		t.Fatalf("merged archive:\n%v\nwant\n%v", unzipped(t, merged), unzipped(t, want))
	}
	// The phone that sent first now merges on top of the PC's: its
	// earlier archive was kept as a version.
	if n := countRows(t, `SELECT count(*) FROM hub_backup_versions WHERE tenant_id = $1 AND box_id = $2`, owner.tenantID, box); n != 1 {
		t.Fatalf("%d versions kept, want the phone's one", n)
	}

	// No archive the hub keeps carries the password, in any form.
	pool := e2ePool(t)
	rows, err := pool.Query(context.Background(),
		`SELECT archive FROM hub_backups WHERE tenant_id = $1 UNION ALL SELECT archive FROM hub_backup_versions WHERE tenant_id = $1`, owner.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	var archives [][]byte
	for rows.Next() {
		var a []byte
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		archives = append(archives, a)
	}
	rows.Close()
	for _, a := range archives {
		files := unzipped(t, a)
		if len(files) != 2 || files[backup.DevicesFile] == "" || files[backup.UsageFile] == "" {
			t.Errorf("a stored archive has entries %v", files)
		}
		for name, text := range files {
			if strings.Contains(text, secret) || strings.Contains(text, "mot_de_passe") {
				t.Errorf("the stored %s carries the password", name)
			}
		}
	}
	if found := databaseRowsContaining(t, secret); len(found) != 0 {
		t.Errorf("the password is in %v", found)
	}

	// Two boxes never mix: the relay's backup of another box is its own.
	if st, body := relayPut(t, w.hub.URL, state.Token, otherBox, phoneBackup); st != http.StatusOK || body["merged"] != false {
		t.Fatalf("relay PUT other box = %d %v", st, body)
	}
	if got := downloaded(t, owner.call, path); !bytes.Equal(got, want) {
		t.Fatal("the other box's backup changed this one")
	}

	// What is refused: no token, not a backup, not a box id, too large.
	if st, _ := relayPut(t, w.hub.URL, "", box, phoneBackup); st != http.StatusUnauthorized {
		t.Fatalf("relay PUT without a token = %d, want 401", st)
	}
	if st, body := owner.call(http.MethodPut, path, map[string]any{"archive": base64.StdEncoding.EncodeToString([]byte("hello"))}); st != http.StatusBadRequest {
		t.Fatalf("PUT not a backup = %d %v, want 400", st, body)
	}
	if st, _ := owner.call(http.MethodPut, owner.tenantPath("router/backups/Not_A_Box"), map[string]any{"archive": base64.StdEncoding.EncodeToString(phoneBackup)}); st != http.StatusBadRequest {
		t.Fatalf("PUT a bad box id = %d, want 400", st)
	}
	huge := base64.StdEncoding.EncodeToString(make([]byte, 3<<20))
	if st, _ := owner.call(http.MethodPut, path, map[string]any{"archive": huge}); st != http.StatusRequestEntityTooLarge {
		t.Fatalf("PUT a huge body = %d, want 413", st)
	}
}

// zipFiles writes a zip by hand, as a spreadsheet or a person would.
func zipFiles(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, text := range files {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, text); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
