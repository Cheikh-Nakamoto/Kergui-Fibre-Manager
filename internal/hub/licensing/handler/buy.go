package handler

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"errors"
	"html/template"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	coredomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	alerts "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

// The purchase page (RFC-006, Two channels: "the hub purchase page") sells
// a NabooPay pass without the app: a phone number, a box serial number, a
// pass, then NabooPay's hosted checkout. There is no sign-in on purpose:
// the pass is credited to the account of the number typed, created if it
// does not exist, so anyone can pay for a relative's box. Nothing is
// revealed in return — the page never says whether the number or the box
// already has an account or a pass — and the pass only does something once
// that number signs in over WhatsApp, which the payer cannot do for it.
//
// It is server-rendered and works without JavaScript. Only the naboopay
// flavor of the app may link to it, never the play one (Play's payment
// policy).

// BuyCookie carries the form's CSRF nonce.
const BuyCookie = "kergui_buy"

const (
	buyPath = Prefix + "buy"
	// maxBuyForm is far above the four short fields of the form.
	maxBuyForm = 4 << 10
	// buyTokenTTL is how long a rendered form can be submitted.
	buyTokenTTL = 2 * time.Hour
	// buyTokenSkew tolerates a token issued by a hub whose clock is ahead.
	buyTokenSkew = 5 * time.Minute
	nonceBytes   = 16
)

//go:embed buy.html
var buyHTML string

var buyTemplate = template.Must(template.New("buy").Parse(buyHTML))

type buyOption struct {
	Code, Term, Price string
	Checked           bool
}

type buyGroup struct {
	Tier   string
	Passes []buyOption
}

type buyPage struct {
	Nonce, CSRF      string
	Phone, Box, Pass string
	Error            string
	Groups           []buyGroup
}

var (
	tierLabels = map[domain.Tier]string{domain.TierBasic: "Basic", domain.TierMax: "Max"}
	termLabels = map[domain.Term]string{
		domain.Term1Month: "1 mois", domain.Term3Months: "3 mois", domain.Term12Months: "12 mois",
	}
)

func (p *buyPage) groups() {
	byTier := map[domain.Tier]int{}
	for _, pass := range domain.Catalogue() {
		i, ok := byTier[pass.Tier]
		if !ok {
			i = len(p.Groups)
			byTier[pass.Tier] = i
			p.Groups = append(p.Groups, buyGroup{Tier: tierLabels[pass.Tier]})
		}
		p.Groups[i].Passes = append(p.Groups[i].Passes, buyOption{
			Code: pass.Code, Term: termLabels[pass.Term], Price: formatXOF(pass.PriceXOF), Checked: pass.Code == p.Pass,
		})
	}
}

// formatXOF writes a price the French way: "20 000 F CFA".
func formatXOF(n int64) string {
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String() + " F CFA"
}

// --- routes ---------------------------------------------------------------------

func (h *Handler) registerBuy(mux *http.ServeMux) {
	mux.HandleFunc("GET "+buyPath, h.buyForm)
	// Go's cross-origin check (Sec-Fetch-Site, else Origin against Host)
	// turns a browser's cross-site POST away before the token is even
	// read; the signed double-submit token covers the browsers and
	// clients that send neither header.
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.renderBuy(w, r, http.StatusForbidden, buyPage{Error: msgExpired})
	}))
	mux.Handle("POST "+buyPath, cop.Handler(http.HandlerFunc(h.buySubmit)))
}

func (h *Handler) buyForm(w http.ResponseWriter, r *http.Request) {
	p := buyPage{}
	// The app's deep link names the box; only a valid serial is echoed.
	if b, err := domain.ParseBoxID(r.URL.Query().Get("box")); err == nil {
		p.Box = string(b)
	}
	h.renderBuy(w, r, http.StatusOK, p)
}

// French messages for what the payer can fix.
const (
	msgPhone       = "Ce numéro de téléphone n'est pas valide. Exemple : 77 123 45 67 ou +221 77 123 45 67."
	msgBox         = "Ce numéro de série n'est pas valide : il ne contient que des lettres et des chiffres (par exemple ZTEG1A2B3C4D)."
	msgPass        = "Choisissez un pass dans la liste."
	msgMaxActive   = "Un pass Max est en cours sur cette box : le pass Basic n'y est pas proposé. Choisissez un pass Max pour la prolonger."
	msgPaymentsOff = "Le paiement en ligne est momentanément indisponible. Réessayez plus tard."
	msgPaymentDown = "Le service de paiement ne répond pas. Réessayez dans quelques minutes."
	msgExpired     = "Le formulaire a expiré ou n'a pas pu être vérifié. Rechargez la page et réessayez."
	msgTooLarge    = "Le formulaire envoyé est trop volumineux."
	msgUnsupported = "Le formulaire n'a pas été envoyé correctement. Rechargez la page et réessayez."
	msgInternal    = "Une erreur est survenue. Réessayez plus tard."
)

func (h *Handler) buySubmit(w http.ResponseWriter, r *http.Request) {
	if ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || ct != "application/x-www-form-urlencoded" {
		h.renderBuy(w, r, http.StatusUnsupportedMediaType, buyPage{Error: msgUnsupported})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBuyForm)
	if err := r.ParseForm(); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			h.renderBuy(w, r, http.StatusRequestEntityTooLarge, buyPage{Error: msgTooLarge})
			return
		}
		h.renderBuy(w, r, http.StatusBadRequest, buyPage{Error: msgUnsupported})
		return
	}
	// Only the body counts: a query string cannot fill the form.
	f := r.PostForm
	p := buyPage{
		Phone: clip(f.Get("phone"), 64), Box: clip(f.Get("box"), 64), Pass: clip(f.Get("pass"), 32),
	}
	if !h.validCSRF(r, f.Get("csrf")) {
		h.renderBuy(w, r, http.StatusForbidden, buyPage{Error: msgExpired, Phone: p.Phone, Box: p.Box, Pass: p.Pass})
		return
	}
	out, err := h.Svc.Checkout(r.Context(), usecase.CheckoutInput{Phone: localPhone(p.Phone), BoxID: p.Box, PassCode: p.Pass})
	if err != nil {
		status, msg := buyError(err)
		if status == http.StatusInternalServerError {
			h.warn("licensing: purchase page checkout failed", "error", err)
		}
		p.Error = msg
		h.renderBuy(w, r, status, p)
		return
	}
	// NabooPay's page is the only place this redirects to; a URL that is
	// not https is not followed.
	if u, err := url.Parse(out.CheckoutURL); err != nil || u.Scheme != "https" || u.Host == "" {
		h.warn("licensing: purchase page got an unusable checkout url", "order", out.OrderID)
		p.Error = msgPaymentDown
		h.renderBuy(w, r, http.StatusBadGateway, p)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, out.CheckoutURL, http.StatusSeeOther)
}

func buyError(err error) (int, string) {
	switch {
	case errors.Is(err, alerts.ErrInvalidPhone):
		return http.StatusBadRequest, msgPhone
	case errors.Is(err, domain.ErrInvalidBoxID):
		return http.StatusBadRequest, msgBox
	case errors.Is(err, domain.ErrUnknownPass):
		return http.StatusBadRequest, msgPass
	case errors.Is(err, domain.ErrMaxActive):
		return http.StatusConflict, msgMaxActive
	case errors.Is(err, usecase.ErrPaymentsOff):
		return http.StatusServiceUnavailable, msgPaymentsOff
	case errors.Is(err, coredomain.ErrPaymentUnavailable):
		return http.StatusBadGateway, msgPaymentDown
	default:
		return http.StatusInternalServerError, msgInternal
	}
}

// localPhone reads a Senegalese number typed the local way. ParsePhone
// takes E.164 (with "+" or "00"): nine digits with neither would become
// "+77…", someone else's number, so the page — sold in Senegal — reads
// them as +221. Anything else goes to ParsePhone untouched.
func localPhone(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "00") {
		return s
	}
	var digits strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r == ' ' || r == '-' || r == '.':
		default:
			return s
		}
	}
	if d := digits.String(); len(d) == 9 {
		return "+221" + d
	}
	return s
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = strings.ToValidUTF8(s[:n], "")
	}
	return s
}

// --- rendering ------------------------------------------------------------------

func (h *Handler) renderBuy(w http.ResponseWriter, r *http.Request, status int, p buyPage) {
	nonce := h.formNonce(r)
	http.SetCookie(w, &http.Cookie{
		Name: BuyCookie, Value: nonce, Path: buyPath, MaxAge: int(buyTokenTTL / time.Second),
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: isHTTPS(r),
	})
	p.CSRF = h.csrfToken(nonce, h.now())
	p.Nonce = randomToken()
	p.groups()

	var buf bytes.Buffer
	if err := buyTemplate.Execute(&buf, p); err != nil {
		h.warn("licensing: purchase page did not render", "error", err)
		http.Error(w, msgInternal, http.StatusInternalServerError)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("X-Frame-Options", "DENY")
	hd.Set("Referrer-Policy", "no-referrer")
	// The form posts here, then follows a 303 to NabooPay: browsers apply
	// form-action to that redirect, and NabooPay's host is whatever its
	// API answers, so any https origin is allowed after 'self'.
	hd.Set("Content-Security-Policy", "default-src 'none'; style-src 'nonce-"+p.Nonce+"'; "+
		"form-action 'self' https:; frame-ancestors 'none'; base-uri 'none'")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (h *Handler) now() time.Time {
	if h.Svc != nil && h.Svc.Now != nil {
		return h.Svc.Now()
	}
	return time.Now()
}

// --- CSRF -----------------------------------------------------------------------
//
// A signed double-submit token: the cookie holds a random nonce (HttpOnly,
// SameSite=Strict), the form holds "<unix time>.<HMAC(key, nonce, time)>".
// A cross-site page can neither read the cookie nor mint a token for it,
// and the HMAC means a cookie planted by a sibling subdomain is useless
// without its token. The key needs no storage: it is derived from the
// signing key, so every hub instance and every restart agree on it.

func randomToken() string {
	b := make([]byte, nonceBytes)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// formNonce is the caller's nonce if it has a well-formed one (two tabs
// stay valid), or a fresh one.
func (h *Handler) formNonce(r *http.Request) string {
	if c, err := r.Cookie(BuyCookie); err == nil {
		if b, err := base64.RawURLEncoding.DecodeString(c.Value); err == nil && len(b) == nonceBytes {
			return c.Value
		}
	}
	return randomToken()
}

func (h *Handler) formKey() []byte {
	h.formKeyOnce.Do(func() {
		switch {
		case len(h.FormKey) > 0:
			h.formKeyBytes = h.FormKey
		case h.Svc != nil && len(h.Svc.SigningKey) > 0:
			mac := hmac.New(sha256.New, h.Svc.SigningKey.Seed())
			mac.Write([]byte("kergui/licensing/buy-form/v1"))
			h.formKeyBytes = mac.Sum(nil)
		default:
			h.formKeyBytes = make([]byte, 32)
			_, _ = rand.Read(h.formKeyBytes)
		}
	})
	return h.formKeyBytes
}

func (h *Handler) csrfMAC(nonce, ts string) []byte {
	mac := hmac.New(sha256.New, h.formKey())
	mac.Write([]byte("buy\x00" + nonce + "\x00" + ts))
	return mac.Sum(nil)
}

func (h *Handler) csrfToken(nonce string, now time.Time) string {
	ts := strconv.FormatInt(now.Unix(), 10)
	return ts + "." + base64.RawURLEncoding.EncodeToString(h.csrfMAC(nonce, ts))
}

func (h *Handler) validCSRF(r *http.Request, tok string) bool {
	c, err := r.Cookie(BuyCookie)
	if err != nil || c.Value == "" {
		return false
	}
	ts, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return false
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	age := h.now().Sub(time.Unix(sec, 0))
	if age > buyTokenTTL || age < -buyTokenSkew {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	return hmac.Equal(got, h.csrfMAC(c.Value, ts))
}
