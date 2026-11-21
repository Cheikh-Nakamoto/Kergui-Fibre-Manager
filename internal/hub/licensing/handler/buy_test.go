package handler_test

import (
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/handler"
)

// noRedirect is a client that shows the 303 instead of following it.
var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

var (
	csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)
	nonceAttr = regexp.MustCompile(`<style nonce="([^"]+)">`)
)

type page struct {
	status int
	body   string
	header http.Header
	cookie *http.Cookie
	csrf   string
}

func (e *env) getBuy(query string) page {
	e.t.Helper()
	resp, err := noRedirect.Get(e.srv.URL + "/licensing/v1/buy" + query)
	if err != nil {
		e.t.Fatal(err)
	}
	return readPage(e.t, resp)
}

func readPage(t *testing.T, resp *http.Response) page {
	t.Helper()
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	p := page{status: resp.StatusCode, body: string(b), header: resp.Header}
	for _, c := range resp.Cookies() {
		if c.Name == handler.BuyCookie {
			p.cookie = c
		}
	}
	if m := csrfField.FindStringSubmatch(p.body); m != nil {
		p.csrf = html.UnescapeString(m[1])
	}
	return p
}

// postBuy submits the form with the cookie and token of a page.
func (e *env) postBuy(from page, form url.Values, mod func(*http.Request)) page {
	e.t.Helper()
	form = withCSRF(form, form.Get("csrf"))
	if form.Get("csrf") == "" && from.csrf != "" {
		form.Set("csrf", from.csrf)
	}
	req, _ := http.NewRequest("POST", e.srv.URL+"/licensing/v1/buy", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if from.cookie != nil {
		req.AddCookie(&http.Cookie{Name: from.cookie.Name, Value: from.cookie.Value})
	}
	if mod != nil {
		mod(req)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	return readPage(e.t, resp)
}

// requestTrial submits the page's trial form for a number.
func (e *env) requestTrial(number string) page {
	e.t.Helper()
	return e.postBuy(e.getBuy(""), url.Values{"offer": {"trial"}, "phone": {number}}, nil)
}

func buyForm(phone, box, pass string) url.Values {
	return url.Values{"phone": {phone}, "box": {box}, "pass": {pass}}
}

func TestBuyPage_Renders(t *testing.T) {
	e := newEnv(t)
	p := e.getBuy("")
	if p.status != http.StatusOK || !strings.HasPrefix(p.header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET /buy = %d %s", p.status, p.header.Get("Content-Type"))
	}
	for _, want := range []string{
		`lang="fr"`, `name="phone"`, `name="box"`, "Pro", "étiquette",
		`value="basic_1m"`, `value="basic_3m"`, `value="basic_12m"`,
		`value="max_1m"`, `value="max_3m"`, `value="max_12m"`,
		"650 F CFA", "5 700 F CFA", "20 000 F CFA", "12 mois", "proche",
	} {
		if !strings.Contains(p.body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(p.body, "<script") {
		t.Error("the page carries a script")
	}

	csp := p.header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "frame-ancestors 'none'", "base-uri 'none'", "form-action 'self' https:"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	m := nonceAttr.FindStringSubmatch(p.body)
	if m == nil || !strings.Contains(csp, "style-src 'nonce-"+m[1]+"'") {
		t.Errorf("style nonce %v not in CSP %q", m, csp)
	}
	if again := e.getBuy(""); nonceAttr.FindStringSubmatch(again.body)[1] == m[1] {
		t.Error("the style nonce is reused across requests")
	}
	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff", "Cache-Control": "no-store",
		"Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY",
	} {
		if got := p.header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	if p.cookie == nil || !p.cookie.HttpOnly || p.cookie.SameSite != http.SameSiteStrictMode || p.cookie.Path != "/licensing/v1/buy" {
		t.Fatalf("form cookie %+v", p.cookie)
	}
	if p.csrf == "" || strings.Contains(p.csrf, p.cookie.Value) {
		t.Fatalf("csrf token %q (cookie %q)", p.csrf, p.cookie.Value)
	}
}

func TestBuyPage_PrefillsTheBox(t *testing.T) {
	e := newEnv(t)
	if p := e.getBuy("?box=" + strings.ToLower(box)); !strings.Contains(p.body, `value="`+box+`"`) {
		t.Fatalf("box not prefilled:\n%s", p.body)
	}
	// A box that is not a serial number is not echoed at all.
	p := e.getBuy("?box=" + url.QueryEscape(`"><script>alert(1)</script>`))
	if p.status != http.StatusOK || strings.Contains(p.body, "alert(1)") || strings.Contains(p.body, "<script") {
		t.Fatalf("hostile box reflected: %d\n%s", p.status, p.body)
	}
}

func TestBuy_ValidationErrorsReRenderTheForm(t *testing.T) {
	e := newEnv(t)
	hostile := `"><img src=x onerror=alert(1)>`
	for _, c := range []struct {
		name, phone, box, pass, want string
		status                       int
	}{
		{"bad phone", hostile, box, "max_1m", "numéro de téléphone", http.StatusBadRequest},
		{"no phone", "", box, "max_1m", "numéro de téléphone", http.StatusBadRequest},
		{"bad box", phone, "ZTEG-0001", "max_1m", "numéro de série", http.StatusBadRequest},
		{"unknown pass", phone, box, "gold", "pass", http.StatusBadRequest},
		{"no pass", phone, box, "", "pass", http.StatusBadRequest},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := e.postBuy(e.getBuy(""), buyForm(c.phone, c.box, c.pass), nil)
			if got.status != c.status || !strings.Contains(got.body, `role="alert"`) || !strings.Contains(got.body, c.want) {
				t.Fatalf("%d, want %d with %q:\n%s", got.status, c.status, c.want, got.body)
			}
			if strings.Contains(got.body, "<img") {
				t.Fatal("input reflected unescaped")
			}
			if got.csrf == "" {
				t.Fatal("the re-rendered form has no token")
			}
		})
	}
	// What was typed comes back, escaped, so it can be corrected.
	got := e.postBuy(e.getBuy(""), buyForm(hostile, box, "max_3m"), nil)
	if !strings.Contains(got.body, html.EscapeString(hostile)) && !strings.Contains(got.body, "&#34;&gt;&lt;img") {
		t.Fatalf("typed phone not kept:\n%s", got.body)
	}
	if !regexp.MustCompile(`value="max_3m"[^>]*checked`).MatchString(got.body) {
		t.Fatalf("chosen pass not kept:\n%s", got.body)
	}
}

func TestBuy_MaxActiveRefusesBasic(t *testing.T) {
	e := newEnv(t)
	if got := e.requestTrial(phone); got.status != http.StatusSeeOther {
		t.Fatalf("trial = %d", got.status)
	}
	tok := e.signIn()
	if st, body, _ := e.do("POST", "/licensing/v1/bind", tok, map[string]any{"boxId": box}); st != http.StatusOK || body["trial"] != "started" {
		t.Fatalf("bind = %d %v", st, body)
	}
	got := e.postBuy(e.getBuy(""), buyForm(phone, box, "basic_1m"), nil)
	if got.status != http.StatusConflict || !strings.Contains(got.body, "Un pass Max est en cours") {
		t.Fatalf("basic over max = %d\n%s", got.status, got.body)
	}
}

func TestBuy_PaymentsOff(t *testing.T) {
	e := newEnvWith(t, func(h *handler.Handler) { h.Svc.Gateway = nil })
	got := e.postBuy(e.getBuy(""), buyForm(phone, box, "max_1m"), nil)
	if got.status != http.StatusServiceUnavailable || !strings.Contains(got.body, "indisponible") {
		t.Fatalf("payments off = %d\n%s", got.status, got.body)
	}
}

func TestBuy_CSRF(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC).Unix())
	e := newEnvWith(t, func(h *handler.Handler) {
		h.Svc.Now = func() time.Time { return time.Unix(clock.Load(), 0) }
	})
	good := buyForm(phone, box, "max_1m")
	first, other := e.getBuy(""), e.getBuy("")
	expired := e.getBuy("")

	for _, c := range []struct {
		name string
		page page
		mod  func(*http.Request)
		form url.Values
	}{
		{"no cookie", page{csrf: first.csrf}, nil, good},
		{"no token", page{cookie: first.cookie}, nil, good},
		{"forged token", first, nil, withCSRF(good, "1700000000.AAAA")},
		{"another form's token", page{cookie: first.cookie, csrf: other.csrf}, nil, good},
		{"cross-site", first, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, good},
		{"foreign origin", first, func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }, good},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := e.postBuy(c.page, c.form, c.mod)
			if got.status != http.StatusForbidden || !strings.Contains(got.body, "Rechargez") {
				t.Fatalf("%d\n%s", got.status, got.body)
			}
		})
	}

	clock.Add(int64(3 * time.Hour / time.Second))
	if got := e.postBuy(expired, buyForm(phone, box, "max_1m"), nil); got.status != http.StatusForbidden {
		t.Fatalf("expired token = %d", got.status)
	}
	e.gw.mu.Lock()
	defer e.gw.mu.Unlock()
	if e.gw.opened != 0 {
		t.Fatal("a refused form reached the gateway")
	}
}

func withCSRF(v url.Values, tok string) url.Values {
	out := url.Values{}
	for k, vs := range v {
		out[k] = vs
	}
	out.Set("csrf", tok)
	return out
}

func TestBuy_BodyLimitAndContentType(t *testing.T) {
	e := newEnv(t)
	p := e.getBuy("")
	big := buyForm(phone, box, "max_1m")
	big.Set("pad", strings.Repeat("x", 16<<10))
	if got := e.postBuy(p, big, nil); got.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("large form = %d", got.status)
	}
	got := e.postBuy(p, buyForm(phone, box, "max_1m"), func(r *http.Request) { r.Header.Set("Content-Type", "application/json") })
	if got.status != http.StatusUnsupportedMediaType {
		t.Fatalf("json body = %d", got.status)
	}
}

// The pass lands on the account of the number typed, whoever pays: buying
// for a relative is the point of the page. A local Senegalese number is
// the same account as its E.164 form.
func TestBuy_RedirectsToNabooPayAndCreditsTheNumber(t *testing.T) {
	e := newEnv(t)
	got := e.postBuy(e.getBuy("?box="+box), buyForm("77 123 45 67", strings.ToLower(box), "max_3m"), nil)
	if got.status != http.StatusSeeOther || got.header.Get("Location") != "https://pay.test/ord-1" {
		t.Fatalf("buy = %d %q\n%s", got.status, got.header.Get("Location"), got.body)
	}

	e.gw.mu.Lock()
	e.gw.paid["ord-1"] = true
	e.gw.mu.Unlock()
	payload := `{"order_id":"ord-1"}`
	req, _ := http.NewRequest("POST", e.srv.URL+"/licensing/v1/webhooks/naboopay", strings.NewReader(payload))
	req.Header.Set(handler.SignatureHeader, handler.Sign([]byte(payload), hookKey))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	tok := e.signIn() // the relative signs in with their own number later
	st, body, _ := e.do("GET", "/licensing/v1/subscriptions", tok, nil)
	list, _ := body["subscriptions"].([]any)
	if st != 200 || len(list) != 1 {
		t.Fatalf("subscriptions = %d %v", st, body)
	}
	if s := list[0].(map[string]any); s["boxId"] != box || s["tier"] != "max" {
		t.Fatalf("subscription %v", s)
	}
}

func TestBuyPage_OffersTheTrialWithANumberOnly(t *testing.T) {
	e := newEnv(t)
	p := e.getBuy("")
	for _, want := range []string{"7 jours d'essai Max", `name="offer" value="trial"`, `id="trial-phone"`} {
		if !strings.Contains(p.body, want) && !strings.Contains(p.body, html.EscapeString(want)) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(p.body, `role="status"`) {
		t.Error("a confirmation on a fresh page")
	}
}

func TestBuy_TrialRequestConfirmsAndIsOncePerNumber(t *testing.T) {
	e := newEnv(t)
	got := e.requestTrial("77 123 45 67") // typed the local way
	if got.status != http.StatusSeeOther || got.header.Get("Location") != "/licensing/v1/buy?trial=ok#essai" {
		t.Fatalf("trial = %d %q\n%s", got.status, got.header.Get("Location"), got.body)
	}
	e.sender.mu.Lock()
	msg := e.sender.last
	e.sender.mu.Unlock()
	if !strings.Contains(msg, "7 jours") || !strings.Contains(msg, "Max") {
		t.Fatalf("WhatsApp confirmation %q", msg)
	}
	if p := e.getBuy("?trial=ok"); !strings.Contains(p.body, `role="status"`) || !strings.Contains(p.body, "démarrent") {
		t.Fatalf("confirmation page:\n%s", p.body)
	}

	again := e.requestTrial(phone)
	if again.status != http.StatusConflict || !strings.Contains(again.body, "déjà bénéficié") || !strings.Contains(again.body, `value="&#43;221771234567"`) {
		t.Fatalf("second trial = %d\n%s", again.status, again.body)
	}
	if bad := e.requestTrial(`"><img src=x>`); bad.status != http.StatusBadRequest || strings.Contains(bad.body, "<img") ||
		!strings.Contains(bad.body, "numéro de téléphone") {
		t.Fatalf("bad number = %d\n%s", bad.status, bad.body)
	}
	e.gw.mu.Lock()
	defer e.gw.mu.Unlock()
	if e.gw.opened != 0 {
		t.Fatal("the trial opened a checkout")
	}
}

func TestBuy_TrialNeedsTheFormToken(t *testing.T) {
	e := newEnv(t)
	p := e.getBuy("")
	got := e.postBuy(page{cookie: p.cookie}, url.Values{"offer": {"trial"}, "phone": {phone}}, nil)
	if got.status != http.StatusForbidden || !strings.Contains(got.body, "Rechargez") {
		t.Fatalf("no token = %d\n%s", got.status, got.body)
	}
	if again := e.requestTrial(phone); again.status != http.StatusSeeOther {
		t.Fatalf("the refused form recorded a trial: %d", again.status)
	}
}

// The page sells a pass with a number, a tier and a duration alone: no box
// serial. The webhook (and its replays) credit it once to the number's
// account, where it waits; the app's first binding applies it, and the
// token it then gets carries the tier.
func TestBuy_WithoutABoxThePassWaitsForTheAppsBinding(t *testing.T) {
	e := newEnv(t)
	if p := e.getBuy(""); strings.Contains(p.body, `id="box" name="box" value="" required`) {
		t.Fatal("the box serial is still required on the page")
	}
	got := e.postBuy(e.getBuy(""), buyForm("77 123 45 67", "", "basic_12m"), nil)
	if got.status != http.StatusSeeOther || got.header.Get("Location") != "https://pay.test/ord-1" {
		t.Fatalf("buy = %d %q\n%s", got.status, got.header.Get("Location"), got.body)
	}
	e.gw.mu.Lock()
	e.gw.paid["ord-1"] = true
	e.gw.mu.Unlock()
	payload := `{"order_id":"ord-1"}`
	for i := 0; i < 3; i++ { // NabooPay retries; the order is credited once
		req, _ := http.NewRequest("POST", e.srv.URL+"/licensing/v1/webhooks/naboopay", strings.NewReader(payload))
		req.Header.Set(handler.SignatureHeader, handler.Sign([]byte(payload), hookKey))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			t.Fatalf("webhook %d = %d", i, resp.StatusCode)
		}
	}
	e.sender.mu.Lock()
	confirm := e.sender.last
	e.sender.mu.Unlock()
	if !strings.Contains(confirm, "Basic") || !strings.Contains(confirm, "12 mois") {
		t.Fatalf("WhatsApp confirmation %q", confirm)
	}

	e.now = e.now.Add(48 * time.Hour) // the app signs in two days later
	tok := e.signIn()
	st, body, _ := e.do("POST", "/licensing/v1/bind", tok, map[string]any{"boxId": box})
	if st != http.StatusOK || body["passes"] != float64(1) {
		t.Fatalf("bind = %d %v", st, body)
	}
	st, body, _ = e.do("POST", "/licensing/v1/token", tok, map[string]any{"boxId": box})
	if st != http.StatusOK || body["tier"] != "basic" || body["token"] == "" {
		t.Fatalf("token = %d %v", st, body)
	}
	st, body, _ = e.do("GET", "/licensing/v1/subscriptions", tok, nil)
	list, _ := body["subscriptions"].([]any)
	if st != 200 || len(list) != 1 {
		t.Fatalf("subscriptions = %d %v", st, body)
	}
	want := e.now.Add(365 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if s := list[0].(map[string]any); s["tier"] != "basic" || s["expiresAt"] != want {
		t.Fatalf("subscription %v, want expiry %s", s, want)
	}
}
