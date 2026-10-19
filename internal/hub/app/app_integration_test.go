package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/config"
)

// testDatabaseEnv names a throwaway Postgres database. The test is skipped
// when it is unset; CI (hub-ci.yml) and `make hub-test` set it.
const testDatabaseEnv = "KERGUI_HUB_TEST_DATABASE_URL"

func newTestHub(t *testing.T, opts ...Option) *httptest.Server {
	t.Helper()
	dsn := os.Getenv(testDatabaseEnv)
	if dsn == "" {
		t.Skipf("%s not set: skipping the Postgres integration test", testDatabaseEnv)
	}
	cfg := config.Config{
		DatabaseURL: dsn,
		JWTSecret:   strings.Repeat("k", 48),
		TokenTTL:    15 * time.Minute,
		RefreshTTL:  time.Hour,
		PublicURL:   "http://hub.test",
		MailFrom:    "Kergui <no-reply@hub.test>",
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a, err := New(ctx, cfg, "test", log, opts...)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(func() {
		srv.Close()
		a.Close()
	})
	return srv
}

type client struct {
	t     *testing.T
	base  string
	token string
}

func (c client) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

// verificationToken reads the email-verification token of userID from the
// outbox, standing in for the user clicking the emailed link.
func verificationToken(t *testing.T, userID string) string {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), os.Getenv(testDatabaseEnv))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var token string
	err = pool.QueryRow(context.Background(),
		`SELECT payload->>'verification_token' FROM event_outbox
		 WHERE event_type = 'auth.signup_owner' AND payload->>'user_id' = $1`, userID).Scan(&token)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestHub_OwnerSignupLoginAndTenantAccess(t *testing.T) {
	srv := newTestHub(t)
	anon := client{t: t, base: srv.URL}

	for _, p := range []string{"/health", "/ready"} {
		if code, _ := anon.do("GET", p, nil); code != http.StatusOK {
			t.Fatalf("GET %s = %d", p, code)
		}
	}

	// Kernel routes need a token, and the super-admin route is not exposed.
	if code, _ := anon.do("GET", "/api/v1/tenants/00000000-0000-0000-0000-000000000000", nil); code != http.StatusUnauthorized {
		t.Fatalf("tenant without token = %d, want 401", code)
	}
	if code, _ := anon.do("POST", "/admin/v1/tenants", map[string]any{"name": "x"}); code != http.StatusNotFound {
		t.Fatalf("super-admin route = %d, want 404", code)
	}

	run := time.Now().UnixNano()
	email := fmt.Sprintf("owner-%d@hub.test", run)
	const password = "Correct-Horse-9-Battery"
	code, signup := anon.do("POST", "/api/v1/auth/signup-owner", map[string]any{
		"email": email, "password": password, "tenantName": "Maison Diallo", "tenantSlug": fmt.Sprintf("diallo-%d", run),
	})
	if code != http.StatusCreated {
		t.Fatalf("signup-owner = %d %v", code, signup)
	}
	tenantID, userID := str(signup, "tenantId"), str(signup, "userId")
	if tenantID == "" || userID == "" || str(signup, "accessToken") == "" {
		t.Fatalf("signup response incomplete: %v", signup)
	}

	// Logging in again needs a verified email. The link's token travels in
	// the auth.signup_owner outbox event, as the notification module sees it.
	if code, _ := anon.do("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": password}); code == http.StatusOK {
		t.Fatal("login succeeded before the email was verified")
	}
	if code, _ := anon.do("POST", "/api/v1/auth/verify-email", map[string]any{"token": verificationToken(t, userID)}); code != http.StatusNoContent {
		t.Fatalf("verify-email = %d", code)
	}

	code, login := anon.do("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": password})
	if code != http.StatusOK {
		t.Fatalf("login = %d %v", code, login)
	}
	owner := client{t: t, base: srv.URL, token: str(login, "accessToken")}

	code, tnt := owner.do("GET", "/api/v1/tenants/"+tenantID, nil)
	if code != http.StatusOK || str(tnt, "name") != "Maison Diallo" {
		t.Fatalf("GET tenant = %d %v", code, tnt)
	}
	code, members := owner.do("GET", "/api/v1/memberships/tenant/"+tenantID, nil)
	if code != http.StatusOK || !strings.Contains(fmt.Sprint(members), "ROLE_OWNER") {
		t.Fatalf("members = %d %v", code, members)
	}

	// Refresh rotates the token; the old one is then rejected.
	oldRefresh := str(login, "refreshToken")
	if code, ref := anon.do("POST", "/api/v1/auth/refresh", map[string]any{"refreshToken": oldRefresh}); code != http.StatusOK || str(ref, "refreshToken") == "" {
		t.Fatalf("refresh = %d %v", code, ref)
	}
	if code, _ := anon.do("POST", "/api/v1/auth/refresh", map[string]any{"refreshToken": oldRefresh}); code != http.StatusUnauthorized {
		t.Fatalf("reused refresh token = %d, want 401", code)
	}

	// Another household may carry the same family name (the kernel's
	// tenant/0003 migration drops the unique constraint), and
	// its owner's token does not open the first household.
	code, other := anon.do("POST", "/api/v1/auth/signup-owner", map[string]any{
		"email": "other-" + email, "password": password, "tenantName": "Maison Diallo", "tenantSlug": fmt.Sprintf("diallo-bis-%d", run),
	})
	if code != http.StatusCreated {
		t.Fatalf("second signup = %d %v", code, other)
	}
	stranger := client{t: t, base: srv.URL, token: str(other, "accessToken")}
	if code, _ := stranger.do("GET", "/api/v1/tenants/"+tenantID, nil); code != http.StatusForbidden && code != http.StatusNotFound {
		t.Fatalf("stranger reading the tenant = %d, want 403/404", code)
	}
}

func TestHub_AuthIsRateLimited(t *testing.T) {
	srv := newTestHub(t)
	anon := client{t: t, base: srv.URL}
	limited := false
	for i := 0; i < authBurst+5; i++ {
		code, _ := anon.do("POST", "/api/v1/auth/login", map[string]any{"email": "nobody@hub.test", "password": "wrong-password-123"})
		if code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("login was never rate limited")
	}
}
