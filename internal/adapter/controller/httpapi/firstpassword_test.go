package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
)

func postAPI(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// A new box (#92): /api/login answers 409 password_change_required, then
// /api/box/first-password sets the new password and stores it, and the rest
// of the API works with it.
func TestAPI_FirstPassword(t *testing.T) {
	box := mockrouter.NewF6600P("user", "Label123")
	box.RequirePasswordChange()
	// Nothing usable in the vault: only the new password can make reads work.
	api := newAPIFor(t, box.Handler(), domain.Credentials{Username: "user", Password: "none"})

	code, body := postAPI(t, api.URL+"/api/login", `{"username":"user","password":"Label123"}`)
	if code != http.StatusConflict || body["code"] != "password_change_required" {
		t.Fatalf("login = %d %v, want 409 password_change_required", code, body)
	}

	code, body = postAPI(t, api.URL+"/api/box/first-password", `{"username":"user","password":"Label123","newPassword":"kergui"}`)
	if code != http.StatusBadRequest || body["code"] != "weak_password" {
		t.Fatalf("weak = %d %v, want 400 weak_password", code, body)
	}
	if !box.PasswordChangeRequired() {
		t.Fatal("a weak password reached the box")
	}

	code, body = postAPI(t, api.URL+"/api/box/first-password", `{"username":"user","password":"Label123","newPassword":"Kergui2026"}`)
	if code != http.StatusOK || body["stored"] != true || body["adapter"] != "zte_f6600p" {
		t.Fatalf("first-password = %d %v", code, body)
	}
	if strings.Contains(func() string { b, _ := json.Marshal(body); return string(b) }(), "Kergui2026") {
		t.Fatal("the answer echoes the password")
	}
	if box.PasswordChangeRequired() {
		t.Fatal("the box still demands a password")
	}
	if code, _ := getJSON(t, api.URL+"/api/inspect"); code != http.StatusOK {
		t.Fatalf("inspect with the stored new password = %d", code)
	}

	code, body = postAPI(t, api.URL+"/api/box/first-password", `{"username":"user","password":"Kergui2026","newPassword":"Encore2027"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("when the box does not ask = %d %v, want 400", code, body)
	}
}
