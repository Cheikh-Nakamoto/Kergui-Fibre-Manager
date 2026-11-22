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

// This is the acceptance test of the pass rules (issue #29, RFC-006): the
// whole hub on Postgres, a fake NabooPay, the console WhatsApp provider and
// a clock the test moves. Passes bought on the hub's page for one box:
//
//  1. stack: a pass bought while one runs extends it, from
//     max(now, current end), instead of overlapping it;
//  2. upgrade: Max bought on a running Basic converts the remaining Basic
//     time at 650 → 2 000, and the token says Max;
//  3. reminder: 3 days before the end, one WhatsApp message with a link to
//     the purchase page for that box, once per pass.

// fakeClock is the hub's clock; the test moves it.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

const day = 24 * time.Hour

func TestE2E_PassStackingUpgradeAndReminder(t *testing.T) {
	dsn := os.Getenv(testDatabaseEnv)
	if dsn == "" {
		t.Skipf("%s not set: skipping the end-to-end test", testDatabaseEnv)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	naboo := newFakeNabooPay(t)
	// Whole seconds: Postgres keeps microseconds, the comparisons below are
	// exact.
	clock := &fakeClock{now: time.Now().UTC().Truncate(time.Second)}
	t0 := clock.Now()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	a, err := app.New(ctx, config.Config{
		DatabaseURL:     dsn,
		JWTSecret:       strings.Repeat("k", 48),
		TokenTTL:        15 * time.Minute,
		RefreshTTL:      time.Hour,
		PublicURL:       "http://hub.test",
		MailFrom:        "Kergui <no-reply@hub.test>",
		TrustCloudflare: true,
		WhatsApp:        config.WhatsApp{Provider: config.NotifierConsole, Number: whatsAppNumber},
		Licensing: config.Licensing{
			SigningKey:            hex.EncodeToString(priv.Seed()),
			NabooPayAPIKey:        "e2e-naboopay-key",
			NabooPayBaseURL:       naboo.srv.URL,
			NabooPayWebhookSecret: licensingWebhookSecret,
			// The test sweeps itself, on its own clock.
			SweepInterval: time.Hour,
		},
	}, "e2e", log, app.WithClock(clock.Now))
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
	number := alertsdomain.Phone("+221" + local)
	box := "ZTEG" + randomDigits(t, 8)

	// Every call comes from an address of its own, so no rate limit of the
	// real hub gets in the way of a test that makes many.
	ips := 0
	client := func(jar http.CookieJar) *http.Client {
		ips++
		return from(fmt.Sprintf("198.51.%d.%d", 100+ips/250, 1+ips%250), jar)
	}

	// buy pays a pass on the hub's page for the box, and NabooPay's signed
	// webhook credits it.
	buy := func(pass string, price int64) {
		t.Helper()
		jar, _ := cookiejar.New(nil)
		browser := client(jar)
		resp, err := browser.Get(hub.URL + "/licensing/v1/buy?box=" + box)
		if err != nil {
			t.Fatal(err)
		}
		page, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		m := csrfInput.FindSubmatch(page)
		if resp.StatusCode != http.StatusOK || m == nil {
			t.Fatalf("buy page = %d\n%s", resp.StatusCode, page)
		}
		form := url.Values{"csrf": {string(m[1])}, "phone": {local}, "box": {box}, "pass": {pass}}
		resp, err = browser.PostForm(hub.URL+"/licensing/v1/buy", form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		loc := resp.Header.Get("Location")
		if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "https://pay.naboopay.test/") {
			t.Fatalf("purchase of %s = %d %q", pass, resp.StatusCode, loc)
		}
		order := strings.TrimPrefix(loc, "https://pay.naboopay.test/")
		if got := naboo.price(order); got != price {
			t.Fatalf("%s charged %d F, want %d", pass, got, price)
		}
		naboo.pay(order)
		body := []byte(`{"order_id":"` + order + `","transaction_status":"paid"}`)
		req, _ := http.NewRequest(http.MethodPost, hub.URL+"/licensing/v1/webhooks/naboopay", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(licensinghandler.SignatureHeader, licensinghandler.Sign(body, licensingWebhookSecret))
		resp, err = client(nil).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			t.Fatalf("webhook for %s = %d", pass, resp.StatusCode)
		}
	}

	// The app: the number signs in over WhatsApp.
	call := func(method, path, token string, in any) (int, map[string]any) {
		t.Helper()
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
		resp, err := client(nil).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	if st, body := call("POST", "/licensing/v1/otp", "", map[string]any{"phone": string(number)}); st != http.StatusAccepted {
		t.Fatalf("otp = %d %v", st, body)
	}
	otp, _ := notifier.LastTo(number)
	st, body := call("POST", "/licensing/v1/otp/verify", "", map[string]any{
		"phone": string(number), "code": sixDigit.FindString(otp.Text), "device": "e2e",
	})
	session, _ := body["sessionToken"].(string)
	if st != http.StatusOK || session == "" {
		t.Fatalf("verify = %d %v", st, body)
	}

	// subscription is the account's pass on the box, as the app reads it.
	subscription := func() (string, time.Time) {
		t.Helper()
		st, body := call("GET", "/licensing/v1/subscriptions", session, nil)
		list, _ := body["subscriptions"].([]any)
		if st != http.StatusOK || len(list) != 1 {
			t.Fatalf("subscriptions = %d %v", st, body)
		}
		sub := list[0].(map[string]any)
		exp, err := time.Parse(time.RFC3339, fmt.Sprint(sub["expiresAt"]))
		if err != nil {
			t.Fatalf("subscription %v: %v", sub, err)
		}
		return fmt.Sprint(sub["tier"]), exp.UTC()
	}
	// token is the entitlement token the app verifies offline.
	token := func() licensingdomain.Tier {
		t.Helper()
		st, body := call("POST", "/licensing/v1/token", session, map[string]any{"boxId": box})
		if st != http.StatusOK {
			t.Fatalf("token = %d %v", st, body)
		}
		tok, _ := body["token"].(string)
		if tok == "" {
			return licensingdomain.Tier(fmt.Sprint(body["tier"]))
		}
		claims, _, err := licensingdomain.Verify(pub, tok, clock.Now())
		if err != nil || string(claims.BoxID) != box || string(claims.Tier) != body["tier"] {
			t.Fatalf("token %v: %+v %v", body, claims, err)
		}
		return claims.Tier
	}
	expect := func(step, tier string, want time.Time) {
		t.Helper()
		gotTier, got := subscription()
		if gotTier != tier || !got.Equal(want) {
			t.Fatalf("%s: %s until %s, want %s until %s (%s off)", step, gotTier, got, tier, want, got.Sub(want))
		}
	}
	// reminders runs the sweep's reminder step until nothing is left to
	// send (the shared database may hold other runs' passes ending in the
	// same days, a hundred per sweep) and returns the reminders this number
	// got. The sweep's other step, re-asking NabooPay about pending orders,
	// would also meet other runs' orders, unknown to this fake NabooPay.
	reminders := func() []string {
		t.Helper()
		for i := 0; i < 20; i++ {
			n, err := a.Licensing().Service.SendReminders(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				break
			}
		}
		var out []string
		for _, s := range notifier.Sent() {
			if s.To == number && strings.Contains(s.Text, "expire le") {
				out = append(out, s.Text)
			}
		}
		return out
	}

	// 1. Stacking. Basic for a month, then ten days later another month:
	// it adds to the end of the first, it does not restart from now.
	buy("basic_1m", 650)
	expect("first Basic month", "basic", t0.Add(30*day))
	clock.advance(10 * day)
	buy("basic_1m", 650)
	expect("second Basic month", "basic", t0.Add(60*day))
	if got := token(); got != licensingdomain.TierBasic {
		t.Fatalf("Basic token says %s", got)
	}

	// 2. Upgrade. 50 Basic days are left: at 650 → 2 000 they are worth
	// 16.25 Max days (390 h), and the Max month comes on top.
	buy("max_1m", 2000)
	upgradedEnd := clock.Now().Add(390*time.Hour + 30*day)
	expect("upgrade to Max", "max", upgradedEnd)
	if got := token(); got != licensingdomain.TierMax {
		t.Fatalf("token after the upgrade says %s", got)
	}

	// 3. Reminder. Not before J-3; at J-3, one WhatsApp message with the
	// purchase page for that box; never twice for the same end date.
	clock.Set(upgradedEnd.Add(-licensingdomain.ReminderLead - time.Hour))
	if got := reminders(); len(got) != 0 {
		t.Fatalf("reminded before J-3: %q", got)
	}
	clock.advance(time.Hour)
	got := reminders()
	link := "http://hub.test/licensing/v1/buy?box=" + box + "&pass=max_1m"
	if len(got) != 1 || !strings.Contains(got[0], box) || !strings.Contains(got[0], link) {
		t.Fatalf("J-3 reminders %q, want one with %s", got, link)
	}
	clock.advance(time.Hour)
	if again := reminders(); len(again) != 1 {
		t.Fatalf("reminded twice for one pass: %q", again)
	}
	// The link opens the page with the box and the pass already chosen.
	resp, err := client(nil).Get(strings.Replace(link, "http://hub.test", hub.URL, 1))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), `value="`+box+`"`) || !strings.Contains(string(page), `value="max_1m" required checked`) {
		t.Fatalf("reminder link page:\n%s", page)
	}

	// 1 again, after the end: the pass has run out, so a new one starts
	// from now, and its own end gets its own reminder.
	clock.Set(upgradedEnd.Add(5 * day))
	if got := token(); got != licensingdomain.TierFree {
		t.Fatalf("token after the end says %s", got)
	}
	buy("max_1m", 2000)
	renewedEnd := clock.Now().Add(30 * day)
	expect("Max after the end", "max", renewedEnd)
	clock.Set(renewedEnd.Add(-licensingdomain.ReminderLead))
	if got := reminders(); len(got) != 2 {
		t.Fatalf("the new pass got %d reminders in all, want 2: %q", len(got), got)
	}
}
