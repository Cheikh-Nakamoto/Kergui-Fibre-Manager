package e2e

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/adapters/console"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/app"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/config"
	licensingdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	licensinghandler "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/handler"
)

// This is the acceptance test of the web pass (issue #27, RFC-006): the
// whole hub on Postgres, with the real NabooPay adapter talking to a fake
// NabooPay. Someone buys a pass on the hub's page with a number, a tier and
// a duration; NabooPay calls the webhook, twice; the number signs in from
// the app, binds its box, and gets a token with the tier and an expiry the
// app verifies offline.

const licensingWebhookSecret = "e2e-naboopay-webhook-secret"

// fakeNabooPay answers the three calls the adapter makes.
type fakeNabooPay struct {
	mu     sync.Mutex
	srv    *httptest.Server
	orders map[string]string // order id -> transaction_status
	amount map[string]int64
}

func newFakeNabooPay(t *testing.T) *fakeNabooPay {
	f := &fakeNabooPay{orders: map[string]string{}, amount: map[string]int64{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeNabooPay) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer e2e-naboopay-key" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v2/transactions":
		var in struct {
			Products []struct {
				Price int64 `json:"price"`
			} `json:"products"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		// Unique across runs: the database, and its order ids, are shared.
		id := fmt.Sprintf("ord-%d-%d", time.Now().UnixNano(), len(f.orders)+1)
		f.orders[id] = "pending"
		if len(in.Products) > 0 {
			f.amount[id] = in.Products[0].Price
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"order_id": id, "checkout_url": "https://pay.naboopay.test/" + id,
			"amount": f.amount[id], "transaction_status": "pending",
		})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v2/transactions/"):
		id := strings.TrimPrefix(r.URL.Path, "/api/v2/transactions/")
		st, ok := f.orders[id]
		if !ok {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"order_id": id, "amount": f.amount[id], "transaction_status": st})
	default:
		http.Error(w, `{"error":"unexpected"}`, http.StatusNotFound)
	}
}

func (f *fakeNabooPay) pay(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orders[id] = "paid"
}

// order makes NabooPay know an order the hub recorded by other means.
func (f *fakeNabooPay) order(id, status string, amount int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orders[id], f.amount[id] = status, amount
}

func (f *fakeNabooPay) price(id string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.amount[id]
}

// randomDigits returns n random decimal digits.
// from is a client whose calls come from ip, as Cloudflare reports it: the
// payer's browser, NabooPay and the app are three addresses, each with its
// own rate limit, as in production.
func from(ip string, jar http.CookieJar) *http.Client {
	return &http.Client{
		Jar:           jar,
		Transport:     fromIP{ip},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type fromIP struct{ ip string }

func (f fromIP) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("CF-Connecting-IP", f.ip)
	return http.DefaultTransport.RoundTrip(r)
}

func randomDigits(t *testing.T, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	for i := range b {
		b[i] = '0' + b[i]%10
	}
	return string(b)
}

var (
	csrfInput = regexp.MustCompile(`name="csrf" value="([^"]+)"`)
	sixDigit  = regexp.MustCompile(`\b\d{6}\b`)
)

func TestE2E_WebPassPurchaseWebhookReplayAndToken(t *testing.T) {
	dsn := os.Getenv(testDatabaseEnv)
	if dsn == "" {
		t.Skipf("%s not set: skipping the end-to-end test", testDatabaseEnv)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	naboo := newFakeNabooPay(t)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a, err := app.New(ctx, config.Config{
		DatabaseURL: dsn,
		JWTSecret:   strings.Repeat("k", 48),
		TokenTTL:    15 * time.Minute,
		RefreshTTL:  time.Hour,
		PublicURL:   "http://hub.test",
		MailFrom:    "Kergui <no-reply@hub.test>",
		// The callers are told apart by CF-Connecting-IP (see from).
		TrustCloudflare: true,
		WhatsApp:        config.WhatsApp{Provider: config.NotifierConsole, Number: whatsAppNumber},
		Licensing: config.Licensing{
			SigningKey:            hex.EncodeToString(priv.Seed()),
			NabooPayAPIKey:        "e2e-naboopay-key",
			NabooPayBaseURL:       naboo.srv.URL,
			NabooPayWebhookSecret: licensingWebhookSecret,
			// The sweep must not settle the order before the webhook does.
			SweepInterval: time.Hour,
		},
	}, "e2e", log)
	if err != nil {
		t.Fatal(err)
	}
	if a.Licensing() == nil {
		t.Fatal("the licensing module is off")
	}
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = a.Run(runCtx) }()
	hub := httptest.NewServer(a.Handler())
	t.Cleanup(func() {
		hub.Close()
		stop()
		<-done
		a.Close()
	})
	notifier, ok := a.Notifier().(*console.Notifier)
	if !ok {
		t.Fatalf("the bench is wired with %T, want the console provider", a.Notifier())
	}

	// A fresh number and box on every run: the database is shared.
	local := "77" + randomDigits(t, 7)
	number := "+221" + local
	box := "ZTEG" + randomDigits(t, 8)

	// 1. The page: a number typed the local way, a tier and a duration, no
	// box serial. It sends the browser to NabooPay.
	jar, _ := cookiejar.New(nil)
	browser := from("198.51.100.10", jar)
	nabooPay, phoneApp := from("203.0.113.5", nil), from("198.51.100.20", nil)
	resp, err := browser.Get(hub.URL + "/licensing/v1/buy")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := csrfInput.FindSubmatch(page)
	if resp.StatusCode != http.StatusOK || m == nil {
		t.Fatalf("buy page = %d\n%s", resp.StatusCode, page)
	}
	form := url.Values{"csrf": {string(m[1])}, "phone": {local}, "box": {""}, "pass": {"max_3m"}}
	resp, err = browser.PostForm(hub.URL+"/licensing/v1/buy", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "https://pay.naboopay.test/") {
		t.Fatalf("purchase = %d %q", resp.StatusCode, loc)
	}
	order := strings.TrimPrefix(loc, "https://pay.naboopay.test/")
	if got := naboo.price(order); got != 5700 {
		t.Fatalf("Max 3 months charged %d F", got)
	}

	// 2. NabooPay: an unsigned call is refused; the signed one, sent twice
	// as NabooPay retries, credits the order once.
	hook := func(sig string) int {
		body := []byte(`{"order_id":"` + order + `","transaction_status":"paid"}`)
		req, _ := http.NewRequest(http.MethodPost, hub.URL+"/licensing/v1/webhooks/naboopay", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if sig == "" {
			sig = licensinghandler.Sign(body, licensingWebhookSecret)
		}
		req.Header.Set(licensinghandler.SignatureHeader, sig)
		resp, err := nabooPay.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	naboo.pay(order)
	if st := hook(strings.Repeat("0", 64)); st/100 == 2 {
		t.Fatalf("a forged signature was accepted (%d)", st)
	}
	for i := 0; i < 2; i++ {
		if st := hook(""); st/100 != 2 {
			t.Fatalf("webhook %d = %d", i, st)
		}
	}
	confirmations := 0
	for _, s := range notifier.Sent() {
		if s.To == alertsdomain.Phone(number) && strings.Contains(s.Text, "Max — 3 mois est payé") {
			confirmations++
		}
	}
	if confirmations != 1 {
		t.Fatalf("%d WhatsApp confirmations for one payment: %+v", confirmations, notifier.Sent())
	}

	// 3. The app: the number signs in over WhatsApp, binds its box, asks
	// for a token.
	call := func(method, path, token string, in any) (int, map[string]any) {
		var r io.Reader
		if in != nil {
			b, _ := json.Marshal(in)
			r = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, hub.URL+path, r)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := phoneApp.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	if st, body := call("POST", "/licensing/v1/otp", "", map[string]any{"phone": number}); st != http.StatusAccepted {
		t.Fatalf("otp = %d %v", st, body)
	}
	sent := notifier.Sent()
	code := sixDigit.FindString(sent[len(sent)-1].Text)
	st, body := call("POST", "/licensing/v1/otp/verify", "", map[string]any{"phone": number, "code": code, "device": "e2e"})
	session, _ := body["sessionToken"].(string)
	if st != http.StatusOK || session == "" {
		t.Fatalf("verify = %d %v", st, body)
	}
	bound := time.Now()
	if st, body := call("POST", "/licensing/v1/bind", session, map[string]any{"boxId": box}); st != http.StatusOK || body["passes"] != float64(1) {
		t.Fatalf("bind = %d %v", st, body)
	}
	st, body = call("POST", "/licensing/v1/token", session, map[string]any{"boxId": box})
	tok, _ := body["token"].(string)
	if st != http.StatusOK || body["tier"] != "max" || tok == "" {
		t.Fatalf("token = %d %v", st, body)
	}
	claims, _, err := licensingdomain.Verify(pub, tok, time.Now())
	if err != nil || claims.Tier != licensingdomain.TierMax || string(claims.BoxID) != box {
		t.Fatalf("token claims %+v %v", claims, err)
	}
	// The pass runs 90 days from the binding; the token is a shorter slice
	// of it that the app refreshes.
	st, body = call("GET", "/licensing/v1/subscriptions", session, nil)
	list, _ := body["subscriptions"].([]any)
	if st != http.StatusOK || len(list) != 1 {
		t.Fatalf("subscriptions = %d %v", st, body)
	}
	sub := list[0].(map[string]any)
	exp, err := time.Parse(time.RFC3339, fmt.Sprint(sub["expiresAt"]))
	want := bound.Add(90 * 24 * time.Hour)
	if err != nil || sub["tier"] != "max" || exp.Before(want.Add(-time.Minute)) || exp.After(want.Add(time.Minute)) {
		t.Fatalf("subscription %v, want expiry near %s", sub, want)
	}
	if claims.ExpiresAt.After(exp) {
		t.Fatalf("token outlives the pass: %s > %s", claims.ExpiresAt, exp)
	}

	// 4. A late replay of the webhook credits nothing more.
	if st := hook(""); st/100 != 2 {
		t.Fatalf("late replay = %d", st)
	}
	_, body = call("GET", "/licensing/v1/subscriptions", session, nil)
	again := body["subscriptions"].([]any)[0].(map[string]any)
	if again["expiresAt"] != sub["expiresAt"] {
		t.Fatalf("a replay moved the expiry: %v -> %v", sub["expiresAt"], again["expiresAt"])
	}
}
