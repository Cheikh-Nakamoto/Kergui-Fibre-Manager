package handler_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	coredomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	alerts "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/handler"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/memstore"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

const (
	phone   = "+221771234567"
	box     = "ZTEG00000001"
	box2    = "ZTEG00000002"
	hookKey = "naboo-webhook-secret"
)

type sender struct {
	mu   sync.Mutex
	last string
}

func (s *sender) Send(_ context.Context, _ alerts.Phone, m alerts.OutboundMessage) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = m.Text
	return "", nil
}

type gateway struct {
	mu     sync.Mutex
	paid   map[string]bool
	opened int
}

func (g *gateway) CreateCheckout(_ context.Context, r port.CheckoutRequest) (port.CheckoutResult, error) {
	g.mu.Lock()
	g.opened++
	g.mu.Unlock()
	return port.CheckoutResult{OrderID: "ord-1", CheckoutURL: "https://pay.test/ord-1", AmountXOF: r.AmountXOF}, nil
}

func (g *gateway) TransactionStatus(_ context.Context, id string) (coredomain.PaymentStatus, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paid[id] {
		return coredomain.PaymentPaid, nil
	}
	return coredomain.PaymentPending, nil
}

func (g *gateway) CancelCheckout(context.Context, string) error { return nil }

type env struct {
	t      *testing.T
	srv    *httptest.Server
	sender *sender
	gw     *gateway
	pub    ed25519.PublicKey
	now    time.Time
}

func newEnv(t *testing.T) *env { return newEnvWith(t, nil) }

// newEnvWith lets a test add to the service and the handler (Play).
func newEnvWith(t *testing.T, wire func(*handler.Handler)) *env {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(nil)
	e := &env{t: t, sender: &sender{}, gw: &gateway{paid: map[string]bool{}}, pub: pub,
		now: time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)}
	svc := &usecase.Service{
		Store: memstore.New(), Sender: e.sender, Gateway: e.gw, SigningKey: priv,
		ReturnURL: "https://hub.test/licensing/v1/checkout/done", Now: func() time.Time { return e.now },
	}
	h := &handler.Handler{Svc: svc, WebhookSecret: hookKey}
	if wire != nil {
		wire(h)
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) do(method, path, token string, body any) (int, map[string]any, http.Header) {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out, resp.Header
}

var code6 = regexp.MustCompile(`\b\d{6}\b`)

func (e *env) signIn() string { return e.signInAs(phone) }

func (e *env) signInAs(number string) string {
	e.t.Helper()
	if st, body, _ := e.do("POST", "/licensing/v1/otp", "", map[string]any{"phone": number}); st != http.StatusAccepted {
		e.t.Fatalf("otp = %d %v", st, body)
	}
	e.sender.mu.Lock()
	c := code6.FindString(e.sender.last)
	e.sender.mu.Unlock()
	st, body, _ := e.do("POST", "/licensing/v1/otp/verify", "", map[string]any{"phone": number, "code": c, "device": "Pixel 7"})
	if st != http.StatusOK {
		e.t.Fatalf("verify = %d %v", st, body)
	}
	tok, _ := body["sessionToken"].(string)
	if !strings.HasPrefix(tok, "kgs_") || body["accountId"] == "" || body["phone"] != number {
		e.t.Fatalf("verify body %v", body)
	}
	return tok
}

// bindAs signs a number in and binds a box, as the app does.
func (e *env) bindAs(number, b string) error {
	e.t.Helper()
	st, body, _ := e.do("POST", "/licensing/v1/bind", e.signInAs(number), map[string]any{"boxId": b})
	if st != http.StatusOK {
		return fmt.Errorf("bind = %d %v", st, body)
	}
	return nil
}

func errCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func TestOTP_RequestVerifyAndRateLimit(t *testing.T) {
	e := newEnv(t)
	if st, body, _ := e.do("POST", "/licensing/v1/otp", "", map[string]any{"phone": "nope"}); st != 400 || errCode(body) != handler.CodeInvalidPhone {
		t.Fatalf("bad phone = %d %v", st, body)
	}
	if st, _, _ := e.do("POST", "/licensing/v1/otp", "", map[string]any{"phone": phone, "extra": 1}); st != 400 {
		t.Fatalf("unknown field = %d", st)
	}
	tok := e.signIn()
	st, body, h := e.do("POST", "/licensing/v1/otp", "", map[string]any{"phone": phone})
	if st != http.StatusTooManyRequests || errCode(body) != handler.CodeOTPRateLimited || h.Get("Retry-After") == "" {
		t.Fatalf("resend = %d %v retry=%q", st, body, h.Get("Retry-After"))
	}
	if st, body, _ := e.do("POST", "/licensing/v1/otp/verify", "", map[string]any{"phone": phone, "code": "000000"}); st != 401 || errCode(body) != handler.CodeOTPExpired {
		t.Fatalf("spent code = %d %v", st, body)
	}
	// Signing out ends that session only.
	if st, _, _ := e.do("DELETE", "/licensing/v1/session", tok, nil); st != http.StatusNoContent {
		t.Fatalf("sign out = %d", st)
	}
	if st, body, _ := e.do("GET", "/licensing/v1/subscriptions", tok, nil); st != 401 || errCode(body) != handler.CodeUnauthorized {
		t.Fatalf("after sign out = %d %v", st, body)
	}
}

// TestOTP_WrongExpiredAndLockedHaveTheirOwnCodes: the app words each
// refusal, so each one has its code.
func TestOTP_WrongExpiredAndLockedHaveTheirOwnCodes(t *testing.T) {
	verify := func(e *env, code string) (int, string) {
		st, body, _ := e.do("POST", "/licensing/v1/otp/verify", "", map[string]any{"phone": phone, "code": code})
		return st, errCode(body)
	}
	request := func(e *env) string {
		if st, body, _ := e.do("POST", "/licensing/v1/otp", "", map[string]any{"phone": phone}); st != http.StatusAccepted {
			t.Fatalf("otp = %d %v", st, body)
		}
		e.sender.mu.Lock()
		defer e.sender.mu.Unlock()
		return code6.FindString(e.sender.last)
	}
	miss := func(c string) string {
		if c == "000000" {
			return "111111"
		}
		return "000000"
	}

	e := newEnv(t)
	if st, code := verify(e, "123456"); st != 401 || code != handler.CodeOTPExpired {
		t.Fatalf("no code sent = %d %s", st, code)
	}
	c := request(e)
	if st, code := verify(e, miss(c)); st != 401 || code != handler.CodeOTPInvalid {
		t.Fatalf("wrong = %d %s", st, code)
	}
	e.now = e.now.Add(usecase.OTPTTL)
	if st, code := verify(e, c); st != 401 || code != handler.CodeOTPExpired {
		t.Fatalf("expired = %d %s", st, code)
	}

	c = request(e)
	last := ""
	for i := 0; i < usecase.OTPMaxAttempts; i++ {
		_, last = verify(e, miss(c))
	}
	if last != handler.CodeOTPLocked {
		t.Fatalf("last allowed miss = %s", last)
	}
	if st, code := verify(e, c); st != 401 || code != handler.CodeOTPLocked {
		t.Fatalf("right code after lock-out = %d %s", st, code)
	}
}

func TestAuthenticatedRoutesNeedASession(t *testing.T) {
	e := newEnv(t)
	for _, r := range []struct{ m, p string }{
		{"GET", "/licensing/v1/subscriptions"}, {"POST", "/licensing/v1/bind"}, {"POST", "/licensing/v1/checkout"},
		{"POST", "/licensing/v1/token"}, {"POST", "/licensing/v1/transfer"}, {"DELETE", "/licensing/v1/session"},
	} {
		if st, _, _ := e.do(r.m, r.p, "kgs_forged", map[string]any{}); st != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", r.m, r.p, st)
		}
	}
}

func TestTrialTokenAndPublicKey(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn()

	st, body, _ := e.do("POST", "/licensing/v1/token", tok, map[string]any{"boxId": box})
	if st != 200 || body["tier"] != "free" || body["token"] != nil {
		t.Fatalf("free token = %d %v", st, body)
	}
	// The trial is asked for on the web page, then binds to the box the app
	// signs in on.
	if got := e.requestTrial(phone); got.status != http.StatusSeeOther {
		t.Fatalf("web trial = %d\n%s", got.status, got.body)
	}
	if st, body, _ := e.do("POST", "/licensing/v1/bind", tok, map[string]any{"boxId": "bad box"}); st != 400 || errCode(body) != handler.CodeInvalidBox {
		t.Fatalf("bad box = %d %v", st, body)
	}
	st, body, _ = e.do("POST", "/licensing/v1/bind", tok, map[string]any{"boxId": strings.ToLower(box)})
	sub, _ := body["subscription"].(map[string]any)
	if st != http.StatusOK || body["trial"] != "started" || body["boxId"] != box || sub["tier"] != "max" || sub["active"] != true {
		t.Fatalf("bind = %d %v", st, body)
	}
	if st, body, _ := e.do("POST", "/licensing/v1/bind", tok, map[string]any{"boxId": box2}); st != 200 || body["trial"] != "none" {
		t.Fatalf("second box = %d %v", st, body)
	}

	st, key, _ := e.do("GET", "/licensing/v1/public-key", "", nil)
	if st != 200 || key["algorithm"] != "Ed25519" || key["keyId"] == "" {
		t.Fatalf("public key = %d %v", st, key)
	}
	raw, _ := base64.StdEncoding.DecodeString(key["publicKey"].(string))
	if !ed25519.PublicKey(raw).Equal(e.pub) {
		t.Fatal("published key is not the signing key's")
	}

	st, body, _ = e.do("POST", "/licensing/v1/token", tok, map[string]any{"boxId": box})
	if st != 200 || body["tier"] != "max" || body["keyId"] != key["keyId"] {
		t.Fatalf("token = %d %v", st, body)
	}
	c, _, err := domain.Verify(ed25519.PublicKey(raw), body["token"].(string), e.now)
	if err != nil || c.BoxID != box || c.Tier != domain.TierMax {
		t.Fatalf("verify: %+v %v", c, err)
	}

	st, body, _ = e.do("GET", "/licensing/v1/subscriptions", tok, nil)
	list, _ := body["subscriptions"].([]any)
	if st != 200 || len(list) != 1 {
		t.Fatalf("subscriptions = %d %v", st, body)
	}
}

func TestCheckoutWebhookAndTransfer(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn()

	if st, body, _ := e.do("POST", "/licensing/v1/checkout", tok, map[string]any{"boxId": box, "pass": "gold"}); st != 400 || errCode(body) != handler.CodeUnknownPass {
		t.Fatalf("unknown pass = %d %v", st, body)
	}
	st, body, _ := e.do("POST", "/licensing/v1/checkout", tok, map[string]any{"boxId": box, "pass": "basic_3m"})
	if st != http.StatusCreated || body["checkoutUrl"] != "https://pay.test/ord-1" || body["amountXof"] != float64(1850) {
		t.Fatalf("checkout = %d %v", st, body)
	}

	hook := func(payload, sig string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", e.srv.URL+"/licensing/v1/webhooks/naboopay", strings.NewReader(payload))
		req.Header.Set(handler.SignatureHeader, sig)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	payload := `{"order_id":"ord-1","transaction_status":"paid"}`
	if st, _ := hook(payload, "deadbeef"); st != 401 {
		t.Fatalf("bad signature = %d", st)
	}
	// Signed, but NabooPay's API still says pending: the body's status is
	// not believed.
	if st, out := hook(payload, handler.Sign([]byte(payload), hookKey)); st != 200 || out["credited"] != false {
		t.Fatalf("pending = %d %v", st, out)
	}
	e.gw.mu.Lock()
	e.gw.paid["ord-1"] = true
	e.gw.mu.Unlock()
	if st, out := hook(payload, handler.Sign([]byte(payload), hookKey)); st != 200 || out["credited"] != true {
		t.Fatalf("paid = %d %v", st, out)
	}
	if st, out := hook(payload, handler.Sign([]byte(payload), hookKey)); st != 200 || out["credited"] != false {
		t.Fatalf("replay = %d %v", st, out)
	}
	st, body, _ = e.do("POST", "/licensing/v1/token", tok, map[string]any{"boxId": box})
	if st != 200 || body["tier"] != "basic" {
		t.Fatalf("token after payment = %d %v", st, body)
	}

	if st, body, _ := e.do("POST", "/licensing/v1/transfer", tok, map[string]any{"fromBoxId": box, "toBoxId": box}); st != 409 || errCode(body) != handler.CodeSameBox {
		t.Fatalf("same box = %d %v", st, body)
	}
	st, body, _ = e.do("POST", "/licensing/v1/transfer", tok, map[string]any{"fromBoxId": box, "toBoxId": box2})
	if st != 200 || body["boxId"] != box2 || body["tier"] != "basic" {
		t.Fatalf("transfer = %d %v", st, body)
	}
	_, _, _ = e.do("POST", "/licensing/v1/transfer", tok, map[string]any{"fromBoxId": box2, "toBoxId": box})
	if st, body, _ := e.do("POST", "/licensing/v1/transfer", tok, map[string]any{"fromBoxId": box, "toBoxId": box2}); st != 409 || errCode(body) != handler.CodeTransferLimit {
		t.Fatalf("third transfer = %d %v", st, body)
	}
}

func TestPublicRoutes(t *testing.T) {
	e := newEnv(t)
	st, body, _ := e.do("GET", "/licensing/v1/passes", "", nil)
	list, _ := body["passes"].([]any)
	if st != 200 || len(list) != 6 || body["trialDays"] != float64(7) {
		t.Fatalf("passes = %d %v", st, body)
	}
	resp, err := http.Get(e.srv.URL + "/licensing/v1/checkout/done?status=success")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "Paiement reçu") {
		t.Fatalf("done page = %d %s", resp.StatusCode, b)
	}
}

func TestWebhookOffWithoutSecret(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	mux := http.NewServeMux()
	(&handler.Handler{Svc: &usecase.Service{Store: memstore.New(), SigningKey: priv}}).RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/licensing/v1/webhooks/naboopay", strings.NewReader(`{}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("webhook without secret = %d", rec.Code)
	}
}

// The app has no trial route: the trial starts from the web page only.
func TestNoTrialRouteForTheApp(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn()
	if st, _, _ := e.do("POST", "/licensing/v1/trial", tok, map[string]any{"boxId": box}); st != http.StatusNotFound && st != http.StatusMethodNotAllowed {
		t.Fatalf("POST /trial = %d", st)
	}
}

func TestBind_RefusesTheTrialOnABoxThatHadOne(t *testing.T) {
	e := newEnv(t)
	if got := e.requestTrial("+221770000009"); got.status != http.StatusSeeOther {
		t.Fatalf("first trial = %d", got.status)
	}
	if err := e.bindAs("+221770000009", box); err != nil {
		t.Fatal(err)
	}
	if got := e.requestTrial(phone); got.status != http.StatusSeeOther {
		t.Fatalf("second number's trial = %d", got.status)
	}
	tok := e.signIn()
	st, body, _ := e.do("POST", "/licensing/v1/bind", tok, map[string]any{"boxId": box})
	sub, _ := body["subscription"].(map[string]any)
	if st != http.StatusOK || body["trial"] != "refused" || body["trialCode"] != handler.CodeTrialBoxUsed || sub["effectiveTier"] != "free" {
		t.Fatalf("bind on a trialled box = %d %v", st, body)
	}
	if st, body, _ := e.do("POST", "/licensing/v1/token", tok, map[string]any{"boxId": box}); st != 200 || body["tier"] != "free" || body["token"] != nil {
		t.Fatalf("token = %d %v", st, body)
	}
}
