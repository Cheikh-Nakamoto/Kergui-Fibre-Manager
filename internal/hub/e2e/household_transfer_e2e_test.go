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
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/adapters/console"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/app"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/config"
	licensingdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	licensinghandler "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/handler"
	licensingusecase "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

// This is the acceptance test of household phones and box transfer (issue
// #30, RFC-006, Accounts): the whole hub on Postgres, a fake NabooPay, the
// console WhatsApp provider and a clock the test moves.
//
//  1. household: two phones signed in to the same number each get a valid
//     entitlement token for the box the pass is on;
//  2. transfer: the owner moves the pass to a new box; the old box's tokens
//     stop renewing (Free), the new box's say the tier; a third transfer
//     within 30 days is refused (TRANSFER_LIMIT, through support), and once
//     the oldest one leaves the rolling window a transfer goes through again.
func TestE2E_HouseholdPhonesAndBoxTransfer(t *testing.T) {
	dsn := os.Getenv(testDatabaseEnv)
	if dsn == "" {
		t.Skipf("%s not set: skipping the end-to-end test", testDatabaseEnv)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	naboo := newFakeNabooPay(t)
	clock := &fakeClock{now: time.Now().UTC().Truncate(time.Second)}

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
			SweepInterval:         time.Hour,
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

	// Fresh number and boxes on every run: the database is shared.
	local := "77" + randomDigits(t, 7)
	number := alertsdomain.Phone("+221" + local)
	oldBox := "ZTEG" + randomDigits(t, 8)
	newBox := "ZTEG" + randomDigits(t, 8)

	ips := 0
	client := func(jar http.CookieJar) *http.Client {
		ips++
		return from(fmt.Sprintf("198.51.%d.%d", 100+ips/250, 1+ips%250), jar)
	}

	call := func(method, path, session string, in any) (int, map[string]any) {
		t.Helper()
		var r io.Reader
		if in != nil {
			b, _ := json.Marshal(in)
			r = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, hub.URL+path, r)
		req.Header.Set("Content-Type", "application/json")
		if session != "" {
			req.Header.Set("Authorization", "Bearer "+session)
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

	// A three-month Max pass bought on the hub's page for the old box: long
	// enough to outlive the 30-day transfer window.
	{
		jar, _ := cookiejar.New(nil)
		browser := client(jar)
		resp, err := browser.Get(hub.URL + "/licensing/v1/buy?box=" + oldBox)
		if err != nil {
			t.Fatal(err)
		}
		page, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		m := csrfInput.FindSubmatch(page)
		if resp.StatusCode != http.StatusOK || m == nil {
			t.Fatalf("buy page = %d\n%s", resp.StatusCode, page)
		}
		form := url.Values{"csrf": {string(m[1])}, "phone": {local}, "box": {oldBox}, "pass": {"max_3m"}}
		resp, err = browser.PostForm(hub.URL+"/licensing/v1/buy", form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		order, ok := strings.CutPrefix(resp.Header.Get("Location"), "https://pay.naboopay.test/")
		if resp.StatusCode != http.StatusSeeOther || !ok {
			t.Fatalf("purchase = %d %q", resp.StatusCode, resp.Header.Get("Location"))
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
			t.Fatalf("webhook = %d", resp.StatusCode)
		}
	}

	// signIn is one phone of the household signing in to the number.
	signIn := func(device string) string {
		t.Helper()
		if st, body := call("POST", "/licensing/v1/otp", "", map[string]any{"phone": string(number)}); st != http.StatusAccepted {
			t.Fatalf("otp for %s = %d %v", device, st, body)
		}
		otp, _ := notifier.LastTo(number)
		st, body := call("POST", "/licensing/v1/otp/verify", "", map[string]any{
			"phone": string(number), "code": sixDigit.FindString(otp.Text), "device": device,
		})
		session, _ := body["sessionToken"].(string)
		if st != http.StatusOK || session == "" {
			t.Fatalf("verify for %s = %d %v", device, st, body)
		}
		return session
	}
	// refresh is what the app does on a box: bind it, then ask for the
	// token, and verify it offline. It returns the tier the token gives.
	refresh := func(session, box string) licensingdomain.Tier {
		t.Helper()
		if st, body := call("POST", "/licensing/v1/bind", session, map[string]any{"boxId": box}); st != http.StatusOK {
			t.Fatalf("bind %s = %d %v", box, st, body)
		}
		st, body := call("POST", "/licensing/v1/token", session, map[string]any{"boxId": box})
		if st != http.StatusOK {
			t.Fatalf("token %s = %d %v", box, st, body)
		}
		tok, _ := body["token"].(string)
		if tok == "" {
			return licensingdomain.Tier(fmt.Sprint(body["tier"]))
		}
		claims, _, err := licensingdomain.Verify(pub, tok, clock.Now())
		if err != nil || string(claims.BoxID) != box || string(claims.Tier) != body["tier"] {
			t.Fatalf("token for %s %v: %+v %v", box, body, claims, err)
		}
		return claims.Tier
	}
	transfer := func(session, from, to string) (int, map[string]any) {
		t.Helper()
		return call("POST", "/licensing/v1/transfer", session, map[string]any{"fromBoxId": from, "toBoxId": to})
	}
	errCode := func(body map[string]any) string {
		e, _ := body["error"].(map[string]any)
		return fmt.Sprint(e["code"])
	}

	// 1. Household: two phones on the same number, the same box, the same
	// Max token.
	phoneA := signIn("e2e-phone-a")
	clock.advance(licensingusecase.OTPResendAfter)
	phoneB := signIn("e2e-phone-b")
	if phoneA == phoneB {
		t.Fatal("the two phones share one session")
	}
	if got := refresh(phoneA, oldBox); got != licensingdomain.TierMax {
		t.Fatalf("phone A on the box: %s", got)
	}
	if got := refresh(phoneB, oldBox); got != licensingdomain.TierMax {
		t.Fatalf("phone B on the box: %s", got)
	}

	// 2. The box is replaced: phone A, on the new one, moves the pass.
	clock.advance(time.Hour)
	if got := refresh(phoneA, newBox); got != licensingdomain.TierFree {
		t.Fatalf("the new box before the transfer: %s", got)
	}
	if st, body := transfer(phoneA, oldBox, newBox); st != http.StatusOK || body["boxId"] != newBox || body["tier"] != "max" {
		t.Fatalf("first transfer = %d %v", st, body)
	}
	if got := refresh(phoneA, newBox); got != licensingdomain.TierMax {
		t.Fatalf("phone A on the new box: %s", got)
	}
	// The household's other phone follows on the new box; on the old one,
	// its token no longer renews.
	if got := refresh(phoneB, newBox); got != licensingdomain.TierMax {
		t.Fatalf("phone B on the new box: %s", got)
	}
	if got := refresh(phoneB, oldBox); got != licensingdomain.TierFree {
		t.Fatalf("the old box still renews: %s", got)
	}

	// Back and forth: the second transfer passes, the third within 30 days
	// is refused and points to support, and the pass stays where it is.
	clock.advance(day)
	if st, body := transfer(phoneB, newBox, oldBox); st != http.StatusOK {
		t.Fatalf("second transfer = %d %v", st, body)
	}
	clock.advance(day)
	st, body := transfer(phoneA, oldBox, newBox)
	if st != http.StatusConflict || errCode(body) != licensinghandler.CodeTransferLimit {
		t.Fatalf("third transfer = %d %v", st, body)
	}
	if msg := fmt.Sprint(body["error"].(map[string]any)["message"]); !strings.Contains(msg, "support") {
		t.Fatalf("the refusal does not point to support: %q", msg)
	}
	if got := refresh(phoneA, oldBox); got != licensingdomain.TierMax {
		t.Fatalf("the refused transfer moved the pass: %s on the old box", got)
	}

	// Rolling, not calendar: once the first transfer is 30 days old, one
	// slot is free again (the second is still inside the window).
	clock.advance(licensingdomain.TransferWindow - 2*day + time.Hour)
	if st, body := transfer(phoneA, oldBox, newBox); st != http.StatusOK {
		t.Fatalf("transfer after the window = %d %v", st, body)
	}
	if got := refresh(phoneB, newBox); got != licensingdomain.TierMax {
		t.Fatalf("phone B on the new box after the window: %s", got)
	}
	if got := refresh(phoneB, oldBox); got != licensingdomain.TierFree {
		t.Fatalf("the old box renews after the last transfer: %s", got)
	}
	if st, body := transfer(phoneA, newBox, oldBox); st != http.StatusConflict || errCode(body) != licensinghandler.CodeTransferLimit {
		t.Fatalf("a second transfer in the new window = %d %v", st, body)
	}
}
