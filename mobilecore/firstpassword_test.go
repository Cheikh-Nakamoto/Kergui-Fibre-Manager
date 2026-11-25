package mobilecore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// newBoxCore points a core at a new F6600P that demands a new password after
// the first sign-in with its label password (#92).
func newBoxCore(t *testing.T) (*Core, *mockrouter.F6600P, string) {
	t.Helper()
	box := mockrouter.NewF6600P("user", "Label123")
	box.RequirePasswordChange()
	srv := httptest.NewServer(box.Handler())
	t.Cleanup(srv.Close)
	c := newCore(t, nil)
	if err := c.Configure(srv.URL, "", ""); err != nil {
		t.Fatal(err)
	}
	return c, box, srv.URL
}

func boxPassword(t *testing.T, c *Core) map[string]any {
	t.Helper()
	out, err := c.BoxPassword()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// The app's flow, through Request exactly as api.login: the login answers
// 409 password_change_required and stores nothing, first-password sets and
// stores the new one, and BoxPassword reads it back for Réglages.
func TestFirstPassword_AppFlow(t *testing.T) {
	c, box, _ := newBoxCore(t)

	status, body := call(t, c, "POST", "/api/login", `{"username":"user","password":"Label123"}`)
	if status != http.StatusConflict || !strings.Contains(body, `"password_change_required"`) {
		t.Fatalf("login = %d %s", status, body)
	}
	if got := boxPassword(t, c); got["stored"] != false {
		t.Fatalf("the label password was stored: %v", got)
	}

	status, body = call(t, c, "POST", "/api/box/first-password", `{"username":"user","password":"Label123","newPassword":"Kergui2026"}`)
	if status != http.StatusOK || !strings.Contains(body, `"stored": true`) {
		t.Fatalf("first-password = %d %s", status, body)
	}
	if box.PasswordChangeRequired() || box.PasswordChangeCancels() != 0 {
		t.Fatal("the box was not given its new password")
	}
	if got := boxPassword(t, c); got["stored"] != true || got["username"] != "user" || got["password"] != "Kergui2026" {
		t.Fatalf("BoxPassword = %v", got)
	}
	if status, body := call(t, c, "GET", "/api/devices", ""); status != http.StatusOK {
		t.Fatalf("devices with the new password = %d %s", status, body)
	}
	// Nothing in the journal carries either password.
	j := journalText(t, c)
	for _, secret := range []string{"Label123", "Kergui2026"} {
		if strings.Contains(j, secret) {
			t.Fatalf("the journal carries %q", secret)
		}
	}
}

// The relays receive the new password: sealing after the change moves the
// credentials version, and the blob opens to the new password.
func TestFirstPassword_SealedVersionMoves(t *testing.T) {
	c := demoCore(t, nil) // an earlier box, its password sealed at v1
	r := newRelay(t)
	bundle := bundleWith(t, c, credTenant, r)
	if a, _ := seal(t, c, credTenant, bundle, ""); a.Version != 1 {
		t.Fatalf("first seal v%d", a.Version)
	}

	box := mockrouter.NewF6600P("user", "Label123")
	box.RequirePasswordChange()
	srv := httptest.NewServer(box.Handler())
	t.Cleanup(srv.Close)
	if err := c.Configure(srv.URL, "", ""); err != nil {
		t.Fatal(err)
	}
	if status, body := call(t, c, "POST", "/api/box/first-password", `{"username":"user","password":"Label123","newPassword":"Kergui2026"}`); status != http.StatusOK {
		t.Fatalf("first-password = %d %s", status, body)
	}
	a, _ := seal(t, c, credTenant, bundle, "")
	if a.Version != 2 || len(a.Relays) != 1 {
		t.Fatalf("after the change: v%d, %d relay(s); want v2", a.Version, len(a.Relays))
	}
	creds, err := trust.OpenCredentials(r, ownerX(t, c), a.Relays[0].Blob, credTenant, agentName(0))
	if err != nil || creds.Password.Reveal() != "Kergui2026" || creds.Username != "user" {
		t.Fatalf("the relay opened %q/%v", creds.Username, err)
	}
}

func TestBoxPassword_NotConfigured(t *testing.T) {
	c := newCore(t, nil)
	if got := boxPassword(t, c); got["stored"] != false || got["password"] != "" {
		t.Fatalf("BoxPassword = %v", got)
	}
}
