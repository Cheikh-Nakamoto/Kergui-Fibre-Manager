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
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	coredomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/adapters/console"
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
	// Today at 10:00 in Dakar: the reminder at the end goes out during the
	// day (issue #99), whenever the test runs.
	clock := &movingClock{now: time.Now().UTC().Truncate(24 * time.Hour).Add(10 * time.Hour)}
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

	// The 7-day trial, asked for on the web page (from a browser of its
	// own), starts when the app binds the box.
	if st, page := newActor(t, srv.URL, "127.0.0.2").webTrial(phone); st != http.StatusSeeOther {
		t.Fatalf("web trial = %d\n%s", st, page)
	}
	if code, out := user.do("POST", "/licensing/v1/bind", map[string]any{"boxId": box}); code != http.StatusOK || str(out, "trial") != "started" {
		t.Fatalf("bind = %d %v", code, out)
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
	// a link to the hub's purchase page for that box.
	clock.advance(97*24*time.Hour - licensingdomain.ReminderLead + time.Hour)
	if err := app.Licensing().Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	reminder, _ := notifier.LastTo(alertsdomain.Phone(phone))
	if !strings.Contains(reminder.Text, box) || !strings.Contains(reminder.Text, "http://hub.test/licensing/v1/buy?box="+box+"&pass=max_1m") {
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

// TestLicensing_PurchasePage buys a pass on the hub's public page for a
// number that has never signed in, as a relative would, and checks that it
// is there when that number does sign in. The form's POST has a budget of
// its own.
func TestLicensing_PurchasePage(t *testing.T) {
	gw := &paidGateway{paid: map[string]bool{}}
	srv, app := newTestHubApp(t, WithPaymentGateway(gw))
	notifier := consoleNotifier(t, app)
	phone := randPhone()
	box := "ZTEG" + randHex(4)
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	resp, err := noFollow.Get(srv.URL + "/licensing/v1/buy?box=" + box)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(page)
	if resp.StatusCode != http.StatusOK || m == nil || !strings.Contains(string(page), `value="`+box+`"`) ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("GET /buy = %d\n%s", resp.StatusCode, page)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == licensinghandler.BuyCookie {
			cookie = c
		}
	}
	post := func(form url.Values) *http.Response {
		req, _ := http.NewRequest("POST", srv.URL+"/licensing/v1/buy", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
		resp, err := noFollow.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	form := url.Values{"csrf": {html.UnescapeString(string(m[1]))}, "phone": {phone}, "box": {box}, "pass": {"basic_1m"}}
	resp = post(form)
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "https://pay.test/") {
		t.Fatalf("POST /buy = %d %q", resp.StatusCode, loc)
	}
	order := strings.TrimPrefix(loc, "https://pay.test/")
	gw.pay(order)
	body := []byte(`{"order_id":"` + order + `"}`)
	req, _ := http.NewRequest("POST", srv.URL+"/licensing/v1/webhooks/naboopay", bytes.NewReader(body))
	req.Header.Set(licensinghandler.SignatureHeader, licensinghandler.Sign(body, testNabooPaySecret))
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("webhook: %v %v", resp, err)
	}

	anon := client{t: t, base: srv.URL}
	if code, out := anon.do("POST", "/licensing/v1/otp", map[string]any{"phone": phone}); code != http.StatusAccepted {
		t.Fatalf("otp = %d %v", code, out)
	}
	msg, _ := notifier.LastTo(alertsdomain.Phone(phone))
	_, out := anon.do("POST", "/licensing/v1/otp/verify", map[string]any{
		"phone": phone, "code": otpCode.FindString(msg.Text), "device": "test",
	})
	user := client{t: t, base: srv.URL, token: str(out, "sessionToken")}
	code, out := user.do("POST", "/licensing/v1/token", map[string]any{"boxId": box})
	if code != http.StatusOK || str(out, "tier") != "basic" {
		t.Fatalf("token for the gifted pass = %d %v", code, out)
	}

	// Past the purchase page's burst, the form is turned away.
	limited := false
	for i := 0; i < buyBurst+2; i++ {
		if post(form).StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("the purchase form was never rate limited")
	}
}

// loopbackClient calls the hub from one loopback address of its own, so a
// test can be several clients as far as the per-IP rate limit can tell.
type loopbackClient struct {
	t    *testing.T
	base string
	http *http.Client
}

func fromIP(t *testing.T, base, ip string) loopbackClient {
	t.Helper()
	d := &net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(ip)}}
	tr := &http.Transport{DialContext: d.DialContext}
	t.Cleanup(tr.CloseIdleConnections)
	return loopbackClient{t: t, base: base, http: &http.Client{Transport: tr}}
}

func (c loopbackClient) post(path string, body any) (int, map[string]any, http.Header) {
	c.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", c.base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out, resp.Header
}

func errorCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

// TestLicensing_SignInRefusals runs the WhatsApp sign-in's refusals through
// the whole hub, with the console provider holding the codes: a number typed
// the local way, a wrong code, a second code too soon, an expired code, a
// code locked by wrong guesses, and the code requests of one address
// rate-limited across numbers.
func TestLicensing_SignInRefusals(t *testing.T) {
	clock := &movingClock{now: time.Now().UTC()}
	srv, app := newTestHubApp(t, WithClock(clock.Now))
	notifier := consoleNotifier(t, app)
	phone := randPhone()
	local := phone[len("+221"):] // "78…": how the household types it
	ipCounter := 1
	// Each step comes from an address of its own, so the per-IP budget it
	// does not test stays out of the way.
	fresh := func() loopbackClient {
		ipCounter++
		return fromIP(t, srv.URL, fmt.Sprintf("127.0.0.%d", ipCounter))
	}
	request := func(c loopbackClient) string {
		t.Helper()
		if st, out, _ := c.post("/licensing/v1/otp", map[string]any{"phone": local}); st != http.StatusAccepted || str(out, "phone") != phone {
			t.Fatalf("otp = %d %v", st, out)
		}
		// The alerts module's normalisation: the code goes to the E.164 number.
		msg, ok := notifier.LastTo(alertsdomain.Phone(phone))
		if !ok {
			t.Fatal("no code sent")
		}
		return otpCode.FindString(msg.Text)
	}
	verify := func(c loopbackClient, code string) (int, map[string]any) {
		t.Helper()
		st, out, _ := c.post("/licensing/v1/otp/verify", map[string]any{"phone": local, "code": code, "device": "test"})
		return st, out
	}
	miss := func(code string) string {
		if code == "000000" {
			return "111111"
		}
		return "000000"
	}

	c := fresh()
	code := request(c)
	if st, out := verify(c, miss(code)); st != http.StatusUnauthorized || errorCode(out) != licensinghandler.CodeOTPInvalid {
		t.Fatalf("wrong code = %d %v", st, out)
	}
	st, out, h := c.post("/licensing/v1/otp", map[string]any{"phone": phone})
	if st != http.StatusTooManyRequests || errorCode(out) != licensinghandler.CodeOTPRateLimited || h.Get("Retry-After") == "" {
		t.Fatalf("second code at once = %d %v retry=%q", st, out, h.Get("Retry-After"))
	}
	clock.advance(10 * time.Minute)
	if st, out := verify(c, code); st != http.StatusUnauthorized || errorCode(out) != licensinghandler.CodeOTPExpired {
		t.Fatalf("expired code = %d %v", st, out)
	}

	c = fresh()
	code = request(c)
	var last map[string]any
	for i := 0; i < 5; i++ {
		_, last = verify(c, miss(code))
	}
	if errorCode(last) != licensinghandler.CodeOTPLocked {
		t.Fatalf("fifth miss = %v", last)
	}
	if st, out := verify(c, code); st != http.StatusUnauthorized || errorCode(out) != licensinghandler.CodeOTPLocked {
		t.Fatalf("right code after lock-out = %d %v", st, out)
	}

	clock.advance(time.Minute)
	c = fresh()
	code = request(c)
	if st, out := verify(c, code); st != http.StatusOK || !strings.HasPrefix(str(out, "sessionToken"), "kgs_") || str(out, "phone") != phone {
		t.Fatalf("fresh code = %d %v", st, out)
	}

	// One address asking codes for many numbers: each number is within its
	// own limit, the address is not.
	c = fresh()
	limited := false
	for i := 0; i < 20 && !limited; i++ {
		st, _, _ := c.post("/licensing/v1/otp", map[string]any{"phone": randPhone()})
		switch st {
		case http.StatusAccepted:
		case http.StatusTooManyRequests:
			limited = true
		default:
			t.Fatalf("otp %d = %d", i, st)
		}
	}
	if !limited {
		t.Fatal("one address was never rate limited across numbers")
	}
	// Another address is not held back by it.
	if st, out, _ := fresh().post("/licensing/v1/otp", map[string]any{"phone": randPhone()}); st != http.StatusAccepted {
		t.Fatalf("other address = %d %v", st, out)
	}
}

// actor is one client of the hub from a loopback address of its own (a
// browser, a phone), so each has its own share of the per-IP budgets.
type actor struct {
	t     *testing.T
	base  string
	http  *http.Client
	token string
}

func newActor(t *testing.T, base, ip string) *actor {
	t.Helper()
	c := fromIP(t, base, ip).http
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &actor{t: t, base: base, http: c}
}

func (a *actor) do(method, path string, body any) (int, map[string]any) {
	a.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, a.base+path, r)
	req.Header.Set("Content-Type", "application/json")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// webTrial fills the purchase page's trial form with a number, as a person
// does in a browser: GET the page, then POST the form with its cookie and
// token. It returns the status and the page answered.
func (a *actor) webTrial(phone string) (int, string) {
	a.t.Helper()
	resp, err := a.http.Get(a.base + "/licensing/v1/buy")
	if err != nil {
		a.t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(page)
	if resp.StatusCode != http.StatusOK || m == nil || !strings.Contains(string(page), `name="offer" value="trial"`) {
		a.t.Fatalf("GET /buy = %d\n%s", resp.StatusCode, page)
	}
	form := url.Values{"csrf": {html.UnescapeString(string(m[1]))}, "offer": {"trial"}, "phone": {phone}}
	req, _ := http.NewRequest("POST", a.base+"/licensing/v1/buy", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range resp.Cookies() {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	resp, err = a.http.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// signIn is the app's WhatsApp sign-in, the console provider holding the code.
func (a *actor) signIn(n *console.Notifier, phone string) {
	a.t.Helper()
	if code, out := a.do("POST", "/licensing/v1/otp", map[string]any{"phone": phone}); code != http.StatusAccepted {
		a.t.Fatalf("otp = %d %v", code, out)
	}
	msg, ok := n.LastTo(alertsdomain.Phone(phone))
	if !ok {
		a.t.Fatal("no code sent")
	}
	code, out := a.do("POST", "/licensing/v1/otp/verify", map[string]any{
		"phone": phone, "code": otpCode.FindString(msg.Text), "device": "test",
	})
	if code != http.StatusOK {
		a.t.Fatalf("verify = %d %v", code, out)
	}
	a.token = str(out, "sessionToken")
}

// TestLicensing_WebTrial is the trial end to end (RFC-006, decision of
// 2026-11-21): a number asks for it on the web page with no sign-in and gets
// a WhatsApp confirmation; days later the app signs in with that number over
// WhatsApp and binds the box, which starts the 7 days; the token it then
// fetches is Max and verifies offline. A second trial is refused for the
// number on the web page, and at binding for the box.
func TestLicensing_WebTrial(t *testing.T) {
	clock := &movingClock{now: time.Now().UTC()}
	srv, app := newTestHubApp(t, WithClock(clock.Now))
	notifier := consoleNotifier(t, app)
	browser := newActor(t, srv.URL, "127.0.0.3")
	phone := randPhone()
	local := phone[len("+221"):] // typed the local way on the page
	box := "ZTEG" + randHex(4)

	st, page := browser.webTrial(local)
	if st != http.StatusSeeOther {
		t.Fatalf("web trial = %d\n%s", st, page)
	}
	confirm, ok := notifier.LastTo(alertsdomain.Phone(phone))
	if !ok || !strings.Contains(confirm.Text, "7 jours") {
		t.Fatalf("WhatsApp confirmation: %v %q", ok, confirm.Text)
	}
	// Once per number, on the page itself.
	if st, page := browser.webTrial(phone); st != http.StatusConflict || !strings.Contains(page, "déjà bénéficié") {
		t.Fatalf("second web trial = %d\n%s", st, page)
	}

	// The app is installed later: the 7 days start at the binding.
	clock.advance(3 * 24 * time.Hour)
	bound := clock.Now()
	phoneApp := newActor(t, srv.URL, "127.0.0.4")
	phoneApp.signIn(notifier, phone)
	code, out := phoneApp.do("POST", "/licensing/v1/bind", map[string]any{"boxId": strings.ToLower(box)})
	if sub, _ := out["subscription"].(map[string]any); code != http.StatusOK || str(out, "trial") != "started" || str(sub, "tier") != "max" {
		t.Fatalf("bind = %d %v", code, out)
	}

	_, key := phoneApp.do("GET", "/licensing/v1/public-key", nil)
	pub, _ := base64.StdEncoding.DecodeString(str(key, "publicKey"))
	code, out = phoneApp.do("POST", "/licensing/v1/token", map[string]any{"boxId": box})
	if code != http.StatusOK || str(out, "tier") != "max" || str(out, "keyId") != str(key, "keyId") {
		t.Fatalf("token = %d %v", code, out)
	}
	claims, _, err := licensingdomain.Verify(pub, str(out, "token"), clock.Now())
	if err != nil || string(claims.BoxID) != box || claims.Tier != licensingdomain.TierMax ||
		!claims.ExpiresAt.Equal(bound.Add(7*24*time.Hour).Truncate(time.Second)) {
		t.Fatalf("claims %+v %v (bound %v)", claims, err, bound)
	}

	// Another number asks for its trial and signs in on the same box: the
	// binding works, the trial is refused with a clear code, the account
	// stays free there.
	other := randPhone()
	if st, page := newActor(t, srv.URL, "127.0.0.5").webTrial(other); st != http.StatusSeeOther {
		t.Fatalf("other number's web trial = %d\n%s", st, page)
	}
	otherApp := newActor(t, srv.URL, "127.0.0.6")
	otherApp.signIn(notifier, other)
	code, out = otherApp.do("POST", "/licensing/v1/bind", map[string]any{"boxId": box})
	if code != http.StatusOK || str(out, "trial") != "refused" || str(out, "trialCode") != licensinghandler.CodeTrialBoxUsed {
		t.Fatalf("bind on a trialled box = %d %v", code, out)
	}
	if code, out := otherApp.do("POST", "/licensing/v1/token", map[string]any{"boxId": box}); code != http.StatusOK || str(out, "tier") != "free" || str(out, "token") != "" {
		t.Fatalf("token after a refused trial = %d %v", code, out)
	}

	// POST /trial (the customer area, #63) never starts a trial on a box,
	// and this number had its trial: refused.
	if code, _ := phoneApp.do("POST", "/licensing/v1/trial", map[string]any{"boxId": box}); code != http.StatusConflict {
		t.Fatalf("POST /trial for a number that had its trial = %d, want 409", code)
	}

	// Seven days after the binding, the hub gives Free again.
	clock.advance(7 * 24 * time.Hour)
	if code, out := phoneApp.do("POST", "/licensing/v1/token", map[string]any{"boxId": box}); code != http.StatusOK || str(out, "tier") != "free" {
		t.Fatalf("token after the trial = %d %v", code, out)
	}
}
