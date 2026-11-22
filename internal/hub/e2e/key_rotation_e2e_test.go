package e2e

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/adapters/console"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/app"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/config"
	licensinghandler "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/handler"
)

// This is the acceptance test of the signing key rotation (issue #39,
// docs/licensing-key-rotation.md): the whole hub on Postgres, restarted with
// each step of the runbook on the same database and a clock the test moves.
// The phone embeds the old key only, as an install from before the rotation
// does, and plays the app's part: it learns keys from the hub's signed list
// and asks for a token at every refresh. Nobody is signed out on the way.
//
//  1. old key active, new key published as next: the token is the old
//     key's; the phone learns the new key from the list the old one signed;
//  2. new key promoted, old one retiring: the same session gets a token of
//     the new key, and the old token still verifies (overlap);
//  3. old key revoked: its token is refused, the refresh hands a new-key
//     token, and a list signed by the old key is no longer learned.

// rotationHub is one run of the hub with a given key configuration.
type rotationHub struct {
	url      string
	notifier *console.Notifier
	stop     func()
}

func startRotationHub(t *testing.T, dsn string, naboo *fakeNabooPay, clock *fakeClock, lic config.Licensing) *rotationHub {
	t.Helper()
	lic.NabooPayAPIKey = "e2e-naboopay-key"
	lic.NabooPayBaseURL = naboo.srv.URL
	lic.NabooPayWebhookSecret = licensingWebhookSecret
	lic.SweepInterval = time.Hour
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
		Licensing:       lic,
	}, "e2e", log, app.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	if a.Licensing() == nil {
		t.Fatal("the licensing module is off")
	}
	runCtx, stopRun := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = a.Run(runCtx) }()
	srv := httptest.NewServer(a.Handler())
	notifier, ok := a.Notifier().(*console.Notifier)
	if !ok {
		t.Fatalf("the bench is wired with %T, want the console provider", a.Notifier())
	}
	h := &rotationHub{url: srv.URL, notifier: notifier}
	stopped := false
	h.stop = func() {
		if stopped {
			return
		}
		stopped = true
		srv.Close()
		stopRun()
		<-done
		a.Close()
	}
	t.Cleanup(h.stop)
	return h
}

func TestE2E_SigningKeyRotation(t *testing.T) {
	dsn := os.Getenv(testDatabaseEnv)
	if dsn == "" {
		t.Skipf("%s not set: skipping the end-to-end test", testDatabaseEnv)
	}
	oldPub, oldPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	newPub, newPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.StdEncoding.EncodeToString
	oldID, newID := entitlement.KeyID(oldPub), entitlement.KeyID(newPub)
	naboo := newFakeNabooPay(t)
	clock := &fakeClock{now: time.Now().UTC().Truncate(time.Second)}

	ips := 0
	client := func(jar http.CookieJar) *http.Client {
		ips++
		return from(fmt.Sprintf("198.18.%d.%d", ips/250, 1+ips%250), jar)
	}
	var hub *rotationHub
	call := func(method, path, token string, in any) (int, map[string]any) {
		t.Helper()
		var r io.Reader
		if in != nil {
			b, _ := json.Marshal(in)
			r = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, hub.url+path, r)
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

	// The phone: it embeds the old key only, and learns the others.
	trusted, err := entitlement.NewKeySet(oldPub)
	if err != nil {
		t.Fatal(err)
	}
	// learn reads GET /public-keys and learns its signed list, as the app
	// does at every refresh. It returns the statuses it read.
	learn := func() (map[string]string, error) {
		t.Helper()
		st, body := call("GET", "/licensing/v1/public-keys", "", nil)
		if st != http.StatusOK {
			t.Fatalf("public-keys = %d %v", st, body)
		}
		l, err := entitlement.VerifyKeyList(trusted, fmt.Sprint(body["signed"]))
		if err != nil {
			return nil, err
		}
		trusted = trusted.Learn(l)
		status := map[string]string{}
		for _, k := range l.Keys {
			status[k.KeyID] = string(k.Status)
		}
		return status, nil
	}
	local := "77" + randomDigits(t, 7)
	number := alertsdomain.Phone("+221" + local)
	box := "ZTEG" + randomDigits(t, 8)
	var session string
	// refresh is the app's refresh: bind the box, then a fresh token.
	refresh := func() (string, entitlement.Claims) {
		t.Helper()
		if st, body := call("POST", "/licensing/v1/bind", session, map[string]any{"boxId": box}); st != http.StatusOK {
			t.Fatalf("bind = %d %v", st, body)
		}
		st, body := call("POST", "/licensing/v1/token", session, map[string]any{"boxId": box})
		tok, _ := body["token"].(string)
		if st != http.StatusOK || tok == "" {
			t.Fatalf("token = %d %v", st, body)
		}
		c, _, err := entitlement.Verify(trusted, tok, clock.Now())
		if err != nil {
			t.Fatalf("the phone refuses the fresh token: %v", err)
		}
		if body["keyId"] != c.KeyID {
			t.Errorf("token keyId %v, claims %s", body["keyId"], c.KeyID)
		}
		return tok, c
	}

	// 1. Old key active, new key published as next. A Max pass is bought on
	// the page for the box; the phone signs in and gets an old-key token.
	hub = startRotationHub(t, dsn, naboo, clock, config.Licensing{
		SigningKey: hex.EncodeToString(oldPriv.Seed()), NextPublicKeys: enc(newPub),
	})
	jar, _ := cookiejar.New(nil)
	browser := client(jar)
	resp, err := browser.Get(hub.url + "/licensing/v1/buy?box=" + box)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := csrfInput.FindSubmatch(page)
	if resp.StatusCode != http.StatusOK || m == nil {
		t.Fatalf("buy page = %d\n%s", resp.StatusCode, page)
	}
	resp, err = browser.PostForm(hub.url+"/licensing/v1/buy", url.Values{"csrf": {string(m[1])}, "phone": {local}, "box": {box}, "pass": {"max_3m"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	order := strings.TrimPrefix(resp.Header.Get("Location"), "https://pay.naboopay.test/")
	if resp.StatusCode != http.StatusSeeOther || order == "" {
		t.Fatalf("purchase = %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	naboo.pay(order)
	hook := []byte(`{"order_id":"` + order + `","transaction_status":"paid"}`)
	req, _ := http.NewRequest(http.MethodPost, hub.url+"/licensing/v1/webhooks/naboopay", bytes.NewReader(hook))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(licensinghandler.SignatureHeader, licensinghandler.Sign(hook, licensingWebhookSecret))
	if resp, err = client(nil).Do(req); err != nil || resp.StatusCode/100 != 2 {
		t.Fatalf("webhook = %v %v", resp, err)
	}
	resp.Body.Close()
	if st, body := call("POST", "/licensing/v1/otp", "", map[string]any{"phone": string(number)}); st != http.StatusAccepted {
		t.Fatalf("otp = %d %v", st, body)
	}
	otp, _ := hub.notifier.LastTo(number)
	st, body := call("POST", "/licensing/v1/otp/verify", "", map[string]any{
		"phone": string(number), "code": sixDigit.FindString(otp.Text), "device": "e2e",
	})
	if session, _ = body["sessionToken"].(string); st != http.StatusOK || session == "" {
		t.Fatalf("verify = %d %v", st, body)
	}
	status, err := learn()
	if err != nil || status[oldID] != "active" || status[newID] != "next" {
		t.Fatalf("step 1 list %v, %v", status, err)
	}
	if !strings.Contains(strings.Join(trusted.IDs(), ","), newID) {
		t.Fatalf("the phone did not learn the next key: %v", trusted.IDs())
	}
	oldTok, c := refresh()
	if c.KeyID != oldID || c.Tier != entitlement.TierMax {
		t.Fatalf("step 1 token %+v, want Max signed by the old key", c)
	}
	hub.stop()

	// 2. A day later the new key is promoted, the old one retires. The
	// session from before the rotation still works, and the refresh
	// switches the token to the new key; the old token still verifies.
	clock.advance(day)
	hub = startRotationHub(t, dsn, naboo, clock, config.Licensing{
		SigningKey: hex.EncodeToString(newPriv.Seed()), RetiringPublicKeys: enc(oldPub),
	})
	if status, err = learn(); err != nil || status[newID] != "active" || status[oldID] != "retiring" {
		t.Fatalf("step 2 list %v, %v", status, err)
	}
	if _, _, err := entitlement.Verify(trusted, oldTok, clock.Now()); err != nil {
		t.Fatalf("the old key's token during the overlap: %v", err)
	}
	_, c = refresh()
	if c.KeyID != newID || c.Tier != entitlement.TierMax || string(c.BoxID) != box {
		t.Fatalf("step 2 token %+v, want Max signed by the new key", c)
	}
	hub.stop()

	// 3. After the overlap (longest token life + grace), the old key is
	// revoked. Its token is refused; the refresh needs no new sign-in.
	clock.advance(entitlement.TokenMaxLifetime + entitlement.OfflineGrace)
	hub = startRotationHub(t, dsn, naboo, clock, config.Licensing{
		SigningKey: hex.EncodeToString(newPriv.Seed()), RevokedKeyIDs: oldID,
	})
	if status, err = learn(); err != nil || len(status) != 1 || status[newID] != "active" {
		t.Fatalf("step 3 list %v, %v", status, err)
	}
	if _, _, err := entitlement.Verify(trusted, oldTok, clock.Now()); !errors.Is(err, entitlement.ErrRevokedKey) {
		t.Fatalf("the revoked key's token: %v, want ErrRevokedKey", err)
	}
	if _, c = refresh(); c.KeyID != newID || c.Tier != entitlement.TierMax {
		t.Fatalf("step 3 token %+v", c)
	}
	// A list the old key signs (a stolen key) teaches the phone nothing.
	forged, err := entitlement.SignKeyList(oldPriv, entitlement.KeyList{
		Keys: []entitlement.PublishedKey{{KeyID: oldID, PublicKey: oldPub, Status: entitlement.KeyActive}}, IssuedAt: clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entitlement.VerifyKeyList(trusted, forged); !errors.Is(err, entitlement.ErrRevokedKey) {
		t.Fatalf("a list signed by the revoked key: %v", err)
	}
}
