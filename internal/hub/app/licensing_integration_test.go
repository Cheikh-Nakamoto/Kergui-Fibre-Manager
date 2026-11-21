package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	coredomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	licensingdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	licensinghandler "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/handler"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// testSigningKey is a fresh Ed25519 seed per test binary: the key is never
// in the repository, not even a test one.
var testSigningKey = func() string {
	seed := make([]byte, ed25519.SeedSize)
	_, _ = rand.Read(seed)
	return base64.StdEncoding.EncodeToString(seed)
}()

const testNabooPaySecret = "test-naboopay-secret"

// paidGateway is NabooPay for the tests: every checkout gets a fresh order,
// and an order is paid when the test says so.
type paidGateway struct {
	mu   sync.Mutex
	n    int
	paid map[string]bool
}

func (g *paidGateway) CreateCheckout(_ context.Context, r port.CheckoutRequest) (port.CheckoutResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
	id := fmt.Sprintf("it-%s-%d", randHex(6), g.n)
	return port.CheckoutResult{OrderID: id, CheckoutURL: "https://pay.test/" + id, AmountXOF: r.AmountXOF}, nil
}

func (g *paidGateway) TransactionStatus(_ context.Context, id string) (coredomain.PaymentStatus, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paid[id] {
		return coredomain.PaymentPaid, nil
	}
	return coredomain.PaymentPending, nil
}

func (g *paidGateway) CancelCheckout(context.Context, string) error { return nil }

func (g *paidGateway) pay(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.paid[id] = true
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b))
}

// randPhone is a Senegalese mobile number no other test run uses.
func randPhone() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	n := (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) % 10_000_000
	return fmt.Sprintf("+22178%07d", n)
}

var otpCode = regexp.MustCompile(`\b\d{6}\b`)

func TestLicensing_SignInTrialPayTokenAndReminder(t *testing.T) {
	gw := &paidGateway{paid: map[string]bool{}}
	clock := &movingClock{now: time.Now().UTC()}
	srv, app := newTestHubApp(t, WithPaymentGateway(gw), WithClock(clock.Now))
	notifier := consoleNotifier(t, app)
	anon := client{t: t, base: srv.URL}
	phone := randPhone()
	box := "ZTEG" + randHex(4)

	// Sign in over WhatsApp: the console provider "sends" the code.
	if code, out := anon.do("POST", "/licensing/v1/otp", map[string]any{"phone": phone}); code != http.StatusAccepted {
		t.Fatalf("otp = %d %v", code, out)
	}
	msg, ok := notifier.LastTo(alertsdomain.Phone(phone))
	if !ok {
		t.Fatal("no code sent")
	}
	code, out := anon.do("POST", "/licensing/v1/otp/verify", map[string]any{
		"phone": phone, "code": otpCode.FindString(msg.Text), "device": "test",
	})
	if code != http.StatusOK || !strings.HasPrefix(str(out, "sessionToken"), "kgs_") {
		t.Fatalf("verify = %d %v", code, out)
	}
	user := client{t: t, base: srv.URL, token: str(out, "sessionToken")}

	// The kernel's JWT routes do not accept a licensing session.
	if code, _ := user.do("GET", "/api/v1/tenants/00000000-0000-0000-0000-000000000000", nil); code != http.StatusUnauthorized {
		t.Fatalf("session token on a kernel route = %d", code)
	}

	if code, out := user.do("POST", "/licensing/v1/trial", map[string]any{"boxId": box}); code != http.StatusCreated || str(out, "tier") != "max" {
		t.Fatalf("trial = %d %v", code, out)
	}

	// Buy Max for 3 months, pay it, and let NabooPay's webhook arrive.
	code, out = user.do("POST", "/licensing/v1/checkout", map[string]any{"boxId": box, "pass": "max_3m"})
	if code != http.StatusCreated || out["amountXof"] != float64(5700) {
		t.Fatalf("checkout = %d %v", code, out)
	}
	order := str(out, "orderId")
	gw.pay(order)
	body := []byte(`{"order_id":"` + order + `","transaction_status":"paid"}`)
	for i := 0; i < 2; i++ { // the replay credits nothing
		req, _ := http.NewRequest("POST", srv.URL+"/licensing/v1/webhooks/naboopay", bytes.NewReader(body))
		req.Header.Set(licensinghandler.SignatureHeader, licensinghandler.Sign(body, testNabooPaySecret))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var ack map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&ack)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || ack["credited"] != (i == 0) {
			t.Fatalf("webhook %d = %d %v", i, resp.StatusCode, ack)
		}
	}

	// The token verifies offline with the published key.
	_, key := anon.do("GET", "/licensing/v1/public-key", nil)
	pub, _ := base64.StdEncoding.DecodeString(str(key, "publicKey"))
	code, out = user.do("POST", "/licensing/v1/token", map[string]any{"boxId": box})
	if code != http.StatusOK || str(out, "tier") != "max" {
		t.Fatalf("token = %d %v", code, out)
	}
	claims, _, err := licensingdomain.Verify(pub, str(out, "token"), clock.Now())
	if err != nil || string(claims.BoxID) != box {
		t.Fatalf("verify: %+v %v", claims, err)
	}
	// 7 trial days + 90 paid days, capped at 30 days per token.
	if got := claims.ExpiresAt.Sub(claims.IssuedAt); got != licensingdomain.TokenMaxLifetime {
		t.Fatalf("token lifetime %v", got)
	}

	// Three days before the end, the sweep sends one WhatsApp reminder with
	// a NabooPay link.
	clock.advance(97*24*time.Hour - licensingdomain.ReminderLead + time.Hour)
	if err := app.Licensing().Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	reminder, _ := notifier.LastTo(alertsdomain.Phone(phone))
	if !strings.Contains(reminder.Text, box) || !strings.Contains(reminder.Text, "https://pay.test/") {
		t.Fatalf("reminder %q", reminder.Text)
	}
	if err := app.Licensing().Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if again, _ := notifier.LastTo(alertsdomain.Phone(phone)); again.MessageID != reminder.MessageID {
		t.Fatalf("reminded twice: %q", again.Text)
	}

	code, out = user.do("GET", "/licensing/v1/subscriptions", nil)
	if list, _ := out["subscriptions"].([]any); code != http.StatusOK || len(list) != 1 {
		t.Fatalf("subscriptions = %d %v", code, out)
	}
}

// movingClock is a clock a test moves forward.
type movingClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *movingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *movingClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
