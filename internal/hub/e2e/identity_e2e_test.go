package e2e

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/adapters/console"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/app"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/config"
)

// This is the acceptance test of the hub identity (issue #41): the number
// verified by a WhatsApp code is the hub account. Verifying the code signs
// the phone in twice in one go — the licensing session and the kernel's
// token pair — and the first sign-in creates the household, with the number
// as its owner. The tokens reach /api/v1/tenants/{id}/agents; another
// number cannot.

type identityHub struct {
	t        *testing.T
	srv      *httptest.Server
	clock    *fakeClock
	notifier *console.Notifier
}

func startIdentityHub(t *testing.T, dsn string) *identityHub {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Now().UTC()}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a, err := app.New(ctx, config.Config{
		DatabaseURL:     dsn,
		JWTSecret:       strings.Repeat("i", 48),
		TokenTTL:        15 * time.Minute,
		RefreshTTL:      time.Hour,
		PublicURL:       "http://hub.test",
		MailFrom:        "Kergui <no-reply@hub.test>",
		TrustCloudflare: true,
		WhatsApp:        config.WhatsApp{Provider: config.NotifierConsole, Number: whatsAppNumber},
		Licensing: config.Licensing{
			SigningKey:    hex.EncodeToString(priv.Seed()),
			SweepInterval: time.Hour,
		},
	}, "e2e", log, app.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = a.Run(runCtx) }()
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(func() {
		srv.Close()
		stop()
		<-done
		a.Close()
	})
	n, ok := a.Notifier().(*console.Notifier)
	if !ok {
		t.Fatalf("the bench is wired with %T, want the console provider", a.Notifier())
	}
	return &identityHub{t: t, srv: srv, clock: clock, notifier: n}
}

// call sends one request from ip with an optional bearer.
func (h *identityHub) call(ip, method, path, token string, in any) (int, map[string]any) {
	h.t.Helper()
	var r io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := from(ip, nil).Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

type phoneSignIn struct {
	session, accountID   string
	userID, tenantID     string
	access, refreshToken string
}

// signIn asks for a WhatsApp code and types it, as the app's sign-in
// screen does.
func (h *identityHub) signIn(ip, number, device string) phoneSignIn {
	h.t.Helper()
	if st, body := h.call(ip, "POST", "/licensing/v1/otp", "", map[string]any{"phone": number}); st != http.StatusAccepted {
		h.t.Fatalf("otp = %d %v", st, body)
	}
	var code string
	for _, s := range h.notifier.Sent() {
		if s.To == alertsdomain.Phone(number) {
			code = sixDigit.FindString(s.Text)
		}
	}
	st, body := h.call(ip, "POST", "/licensing/v1/otp/verify", "", map[string]any{"phone": number, "code": code, "device": device})
	if st != http.StatusOK {
		h.t.Fatalf("verify = %d %v", st, body)
	}
	hub, _ := body["hub"].(map[string]any)
	if hub == nil {
		h.t.Fatalf("verify returned no hub session: %v", body)
	}
	out := phoneSignIn{
		session: str(body, "sessionToken"), accountID: str(body, "accountId"),
		userID: str(hub, "userId"), tenantID: str(hub, "tenantId"),
		access: str(hub, "accessToken"), refreshToken: str(hub, "refreshToken"),
	}
	if !strings.HasPrefix(out.session, "kgs_") || out.tenantID == "" || out.userID == "" || out.access == "" || out.refreshToken == "" {
		h.t.Fatalf("sign-in %+v", out)
	}
	return out
}

func agentsPath(tenantID string) string { return "/api/v1/tenants/" + tenantID + "/agents" }

func TestE2E_WhatsAppNumberIsTheHubAccount(t *testing.T) {
	dsn := os.Getenv(testDatabaseEnv)
	if dsn == "" {
		t.Skipf("%s not set: skipping the end-to-end test", testDatabaseEnv)
	}
	h := startIdentityHub(t, dsn)
	// Fresh numbers on every run: the database is shared.
	numberA := "+22177" + randomDigits(t, 7)
	numberB := "+22178" + randomDigits(t, 7)
	const ipA, ipA2, ipB = "198.51.100.41", "198.51.100.42", "198.51.100.43"

	// 1. First sign-in: the household is created, the number owns it, and
	// the kernel token reaches the household's routes.
	a := h.signIn(ipA, numberA, "Pixel 7")
	if st, body := h.call(ipA, "GET", agentsPath(a.tenantID), a.access, nil); st != http.StatusOK {
		t.Fatalf("agents with the hub token = %d %v", st, body)
	}
	if n := countRows(t, `SELECT count(*) FROM memberships WHERE user_id = $1 AND tenant_id = $2
		AND role = 'ROLE_OWNER' AND status = 'ACTIVE'`, a.userID, a.tenantID); n != 1 {
		t.Fatalf("%d owner memberships", n)
	}
	// The licensing session of the same sign-in works as before.
	if st, body := h.call(ipA, "GET", "/licensing/v1/subscriptions", a.session, nil); st != http.StatusOK {
		t.Fatalf("subscriptions = %d %v", st, body)
	}

	// 2. Refresh rotation: the kernel's own route rotates the pair, the new
	// access token works, and the spent refresh token is refused.
	st, body := h.call(ipA, "POST", "/api/v1/auth/refresh", "", map[string]any{"refreshToken": a.refreshToken})
	rotated, newAccess := str(body, "refreshToken"), str(body, "accessToken")
	if st != http.StatusOK || rotated == "" || rotated == a.refreshToken || newAccess == "" {
		t.Fatalf("refresh = %d %v", st, body)
	}
	if st, body := h.call(ipA, "GET", agentsPath(a.tenantID), newAccess, nil); st != http.StatusOK {
		t.Fatalf("agents with the refreshed token = %d %v", st, body)
	}
	if st, _ := h.call(ipA, "POST", "/api/v1/auth/refresh", "", map[string]any{"refreshToken": a.refreshToken}); st != http.StatusUnauthorized {
		t.Fatalf("a spent refresh token = %d, want 401", st)
	}
	// Reusing a spent token revoked the user's refresh tokens (the kernel's
	// theft response); the licensing session is the way back in.
	if st, _ := h.call(ipA, "POST", "/api/v1/auth/refresh", "", map[string]any{"refreshToken": rotated}); st != http.StatusUnauthorized {
		t.Fatalf("refresh after reuse = %d, want 401", st)
	}
	st, body = h.call(ipA, "POST", "/licensing/v1/session/hub", a.session, nil)
	if st != http.StatusOK || str(body, "tenantId") != a.tenantID || str(body, "refreshToken") == "" {
		t.Fatalf("hub session from the licensing session = %d %v", st, body)
	}
	if st, _ := h.call(ipA, "POST", "/api/v1/auth/refresh", "", map[string]any{"refreshToken": str(body, "refreshToken")}); st != http.StatusOK {
		t.Fatalf("refresh of the reissued pair = %d", st)
	}

	// 3. Second sign-in of the number (another phone of the household):
	// the same user and the same household, nothing new created.
	h.clock.advance(2 * time.Minute) // past the code resend delay
	a2 := h.signIn(ipA2, numberA, "Tecno Spark")
	if a2.accountID != a.accountID || a2.userID != a.userID || a2.tenantID != a.tenantID {
		t.Fatalf("second sign-in got %+v, first %+v", a2, a)
	}
	if n := countRows(t, `SELECT count(*) FROM memberships WHERE user_id = $1`, a.userID); n != 1 {
		t.Fatalf("the number holds %d memberships after two sign-ins", n)
	}
	if st, body := h.call(ipA2, "GET", agentsPath(a.tenantID), a2.access, nil); st != http.StatusOK {
		t.Fatalf("agents from the second phone = %d %v", st, body)
	}

	// 4. Isolation: another number has its own household and cannot reach
	// this one, nor this one reach it.
	b := h.signIn(ipB, numberB, "Itel")
	if b.tenantID == a.tenantID || b.userID == a.userID {
		t.Fatalf("two numbers share a household: %+v %+v", a, b)
	}
	if st, _ := h.call(ipB, "GET", agentsPath(a.tenantID), b.access, nil); st != http.StatusForbidden {
		t.Fatalf("another number on the household = %d, want 403", st)
	}
	if st, _ := h.call(ipA, "GET", agentsPath(b.tenantID), a2.access, nil); st != http.StatusForbidden {
		t.Fatalf("the household on another number's = %d, want 403", st)
	}
	// The licensing session is not a kernel token.
	if st, _ := h.call(ipB, "GET", agentsPath(b.tenantID), b.session, nil); st != http.StatusUnauthorized {
		t.Fatalf("a kgs_ session on the kernel routes = %d, want 401", st)
	}
	// And the e-mail login cannot be used to get into a phone household:
	// its user has no password.
	if n := countRows(t, `SELECT count(*) FROM users WHERE id = $1 AND starts_with(password, '$')`, a.userID); n != 0 {
		t.Fatal("the phone user has a usable password hash")
	}
}
