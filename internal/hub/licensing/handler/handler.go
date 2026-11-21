// Package handler exposes the licensing module over HTTP under
// /licensing/v1. The routes carry no kernel JWT: a phone signs in with a
// WhatsApp code and then sends its kgs_ session token, which Middleware
// resolves; NabooPay's webhook is authenticated by its HMAC signature.
package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"

	coredomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	alerts "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/signingkey"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

const (
	// Prefix is where the module's routes live.
	Prefix = "/licensing/v1/"
	// SignatureHeader is NabooPay's webhook signature: the hex HMAC-SHA256
	// of the raw body, keyed with the webhook secret.
	SignatureHeader = "X-Signature"
	maxBody         = 64 << 10
	// webhookBudget keeps the webhook under NabooPay's 5 s: past it, the
	// sweep settles the order instead.
	webhookBudget = 4 * time.Second
)

// Logger is the handler's logging port.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// Handler serves the licensing endpoints.
type Handler struct {
	Svc *usecase.Service
	// WebhookSecret authenticates NabooPay's calls; empty turns the
	// webhook route off (the sweep still settles orders).
	WebhookSecret string
	// PlayPush authenticates Pub/Sub's push of Google Play's notifications;
	// nil (or no Play on the service) turns the Play webhook route off.
	PlayPush usecase.PushVerifier
	// FormKey keys the purchase page's CSRF tokens. Empty derives one from
	// the service's signing key, so every hub instance agrees on it.
	FormKey []byte
	Log     Logger

	formKeyOnce  sync.Once
	formKeyBytes []byte
}

// RegisterRoutes mounts every licensing route.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	const p = Prefix
	mux.HandleFunc("POST "+p+"otp", h.requestOTP)
	mux.HandleFunc("POST "+p+"otp/verify", h.verifyOTP)
	mux.HandleFunc("GET "+p+"passes", h.passes)
	mux.HandleFunc("GET "+p+"public-key", h.publicKey)
	mux.HandleFunc("POST "+p+"webhooks/naboopay", h.nabooPayWebhook)
	mux.HandleFunc("POST "+p+"webhooks/play", h.playWebhook)
	mux.HandleFunc("GET "+p+"checkout/done", h.checkoutDone)
	h.registerBuy(mux)

	mux.Handle("DELETE "+p+"session", h.Middleware(http.HandlerFunc(h.signOut)))
	mux.Handle("GET "+p+"subscriptions", h.Middleware(http.HandlerFunc(h.subscriptions)))
	mux.Handle("POST "+p+"bind", h.Middleware(http.HandlerFunc(h.bindBox)))
	mux.Handle("POST "+p+"checkout", h.Middleware(http.HandlerFunc(h.checkout)))
	mux.Handle("POST "+p+"token", h.Middleware(http.HandlerFunc(h.token)))
	mux.Handle("POST "+p+"transfer", h.Middleware(http.HandlerFunc(h.transfer)))
	mux.Handle("POST "+p+"play/purchase", h.Middleware(http.HandlerFunc(h.playPurchase)))
}

// --- authentication -----------------------------------------------------------

type accountKey struct{}

// Middleware resolves the kgs_ session token. Every failure answers the
// same 401, so a caller probing tokens learns nothing.
func (h *Handler) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acc, err := h.Svc.Authenticate(r.Context(), bearer(r))
		if err != nil {
			response.Error(w, mapErr(err))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), accountKey{}, acc)))
	})
}

func accountFrom(ctx context.Context) usecase.Account {
	a, _ := ctx.Value(accountKey{}).(usecase.Account)
	return a
}

func bearer(r *http.Request) string {
	if after, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

// --- sign-in ------------------------------------------------------------------

type otpRequest struct {
	Phone string `json:"phone"`
}

type otpResponse struct {
	Phone     string    `json:"phone"`
	ExpiresAt time.Time `json:"expiresAt"`
	ResendAt  time.Time `json:"resendAt"`
}

func (h *Handler) requestOTP(w http.ResponseWriter, r *http.Request) {
	var req otpRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	out, err := h.Svc.RequestOTP(r.Context(), req.Phone)
	if err != nil {
		var rl *usecase.RateLimitError
		if errors.As(err, &rl) {
			w.Header().Set("Retry-After", strconv.Itoa(int(rl.RetryAfter.Round(time.Second)/time.Second)))
		}
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusAccepted, otpResponse{Phone: string(out.Phone), ExpiresAt: out.ExpiresAt, ResendAt: out.ResendAt})
}

type verifyRequest struct {
	Phone  string `json:"phone"`
	Code   string `json:"code"`
	Device string `json:"device"`
}

type verifyResponse struct {
	SessionToken string    `json:"sessionToken"`
	AccountID    string    `json:"accountId"`
	Phone        string    `json:"phone"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

func (h *Handler) verifyOTP(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	in, err := h.Svc.VerifyOTP(r.Context(), req.Phone, req.Code, req.Device)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusOK, verifyResponse{
		SessionToken: in.Token, AccountID: string(in.Account.ID), Phone: string(in.Account.Phone), ExpiresAt: in.ExpiresAt,
	})
}

func (h *Handler) signOut(w http.ResponseWriter, r *http.Request) {
	if err := h.Svc.SignOut(r.Context(), bearer(r)); err != nil {
		response.Error(w, mapErr(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- catalogue and key --------------------------------------------------------

type passView struct {
	Code     string `json:"code"`
	Tier     string `json:"tier"`
	Term     string `json:"term"`
	Days     int    `json:"days"`
	PriceXOF int64  `json:"priceXof"`
}

func viewPass(p domain.Pass) passView {
	return passView{Code: p.Code, Tier: string(p.Tier), Term: string(p.Term), Days: p.Days, PriceXOF: p.PriceXOF}
}

func (h *Handler) passes(w http.ResponseWriter, _ *http.Request) {
	list := domain.Catalogue()
	out := make([]passView, 0, len(list))
	for _, p := range list {
		out = append(out, viewPass(p))
	}
	response.JSON(w, http.StatusOK, map[string]any{"passes": out, "trialDays": domain.TrialPass.Days})
}

type publicKeyResponse struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"keyId"`
	// PublicKey is the raw 32-byte key, standard base64.
	PublicKey string `json:"publicKey"`
}

func (h *Handler) publicKey(w http.ResponseWriter, _ *http.Request) {
	pub := h.Svc.PublicKey()
	if pub == nil {
		response.Error(w, kernelerrors.ServiceUnavailable("no signing key is configured"))
		return
	}
	response.JSON(w, http.StatusOK, publicKeyResponse{
		Algorithm: "Ed25519", KeyID: signingkey.KeyID(pub), PublicKey: base64.StdEncoding.EncodeToString(pub),
	})
}

// --- subscriptions --------------------------------------------------------------

type subscriptionView struct {
	BoxID         string    `json:"boxId"`
	Tier          string    `json:"tier"`
	EffectiveTier string    `json:"effectiveTier"`
	Active        bool      `json:"active"`
	ExpiresAt     time.Time `json:"expiresAt"`
	// Channel is "prepaid" (NabooPay, the trial) or "play": a Play
	// subscription is managed in the Play Store, not renewed in the app.
	Channel string `json:"channel"`
}

func (h *Handler) view(s domain.Subscription) subscriptionView {
	now := time.Now()
	if h.Svc.Now != nil {
		now = h.Svc.Now()
	}
	return subscriptionView{
		BoxID: string(s.BoxID), Tier: string(s.Tier), EffectiveTier: string(s.EffectiveTier(now)),
		Active: s.Active(now), ExpiresAt: s.ExpiresAt, Channel: string(channelOf(s)),
	}
}

func channelOf(s domain.Subscription) domain.Channel {
	if s.Channel == "" {
		return domain.ChannelPrepaid
	}
	return s.Channel
}

func (h *Handler) subscriptions(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.Subscriptions(r.Context(), accountFrom(r.Context()).ID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	out := make([]subscriptionView, 0, len(list))
	for _, s := range list {
		out = append(out, h.view(s))
	}
	response.JSON(w, http.StatusOK, map[string]any{"subscriptions": out})
}

type boxRequest struct {
	BoxID string `json:"boxId"`
}

// bindResponse is the app's first sign-in on a box. Trial says what
// happened to a trial asked for on the web page: "none" (nothing waiting),
// "started", or "refused" with TrialCode TRIAL_BOX_USED (the box already
// had one; the account stays as it was on the box).
type bindResponse struct {
	BoxID        string           `json:"boxId"`
	Subscription subscriptionView `json:"subscription"`
	Trial        string           `json:"trial"`
	TrialCode    string           `json:"trialCode,omitempty"`
	// Passes is how many passes bought on the web page without a box this
	// binding applied.
	Passes int `json:"passes,omitempty"`
}

func (h *Handler) bindBox(w http.ResponseWriter, r *http.Request) {
	var req boxRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	out, err := h.Svc.BindBox(r.Context(), accountFrom(r.Context()).ID, req.BoxID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	res := bindResponse{BoxID: string(out.Subscription.BoxID), Subscription: h.view(out.Subscription), Trial: string(out.Trial), Passes: out.Passes}
	if out.Trial == usecase.TrialBoxUsed {
		res.Trial, res.TrialCode = "refused", CodeTrialBoxUsed
	}
	response.JSON(w, http.StatusOK, res)
}

type checkoutRequest struct {
	BoxID string `json:"boxId"`
	Pass  string `json:"pass"`
}

type checkoutResponse struct {
	OrderID     string   `json:"orderId"`
	CheckoutURL string   `json:"checkoutUrl"`
	AmountXOF   int64    `json:"amountXof"`
	Pass        passView `json:"pass"`
}

func (h *Handler) checkout(w http.ResponseWriter, r *http.Request) {
	var req checkoutRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	out, err := h.Svc.Checkout(r.Context(), usecase.CheckoutInput{
		Phone: string(accountFrom(r.Context()).Phone), BoxID: req.BoxID, PassCode: req.Pass,
	})
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusCreated, checkoutResponse{
		OrderID: out.OrderID, CheckoutURL: out.CheckoutURL, AmountXOF: out.AmountXOF, Pass: viewPass(out.Pass),
	})
}

type tokenResponse struct {
	BoxID     string     `json:"boxId"`
	Tier      string     `json:"tier"`
	Token     string     `json:"token,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	IssuedAt  *time.Time `json:"issuedAt,omitempty"`
	KeyID     string     `json:"keyId,omitempty"`
}

func (h *Handler) token(w http.ResponseWriter, r *http.Request) {
	var req boxRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	ent, err := h.Svc.IssueToken(r.Context(), accountFrom(r.Context()).ID, req.BoxID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	box, _ := domain.ParseBoxID(req.BoxID)
	out := tokenResponse{BoxID: string(box), Tier: string(ent.Tier)}
	if ent.Token != "" {
		exp, iat := ent.Claims.ExpiresAt.UTC().Truncate(time.Second), ent.Claims.IssuedAt.UTC().Truncate(time.Second)
		out.Token, out.ExpiresAt, out.IssuedAt = ent.Token, &exp, &iat
		out.KeyID = signingkey.KeyID(h.Svc.PublicKey())
	}
	response.JSON(w, http.StatusOK, out)
}

type transferRequest struct {
	FromBoxID string `json:"fromBoxId"`
	ToBoxID   string `json:"toBoxId"`
}

func (h *Handler) transfer(w http.ResponseWriter, r *http.Request) {
	var req transferRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	sub, err := h.Svc.Transfer(r.Context(), accountFrom(r.Context()).ID, req.FromBoxID, req.ToBoxID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusOK, h.view(sub))
}

// --- NabooPay -------------------------------------------------------------------

// nabooPayWebhook receives NabooPay's payment notification. As in the kergui
// core (httpapi.paymentWebhook), the signature proves the call came from
// NabooPay, and its status is still never trusted: SettleOrder asks
// NabooPay's API about the order, then credits the pass once.
func (h *Handler) nabooPayWebhook(w http.ResponseWriter, r *http.Request) {
	if h.WebhookSecret == "" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		response.Error(w, kernelerrors.BadRequest("body too large"))
		return
	}
	if !ValidSignature(body, r.Header.Get(SignatureHeader), h.WebhookSecret) {
		h.warn("licensing: naboopay webhook with a bad signature, ignored")
		response.Error(w, kernelerrors.Unauthorized("invalid signature"))
		return
	}
	var ev struct {
		OrderID string `json:"order_id"`
	}
	if err := json.Unmarshal(body, &ev); err != nil || ev.OrderID == "" {
		response.Error(w, kernelerrors.BadRequest("order_id missing"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webhookBudget)
	defer cancel()
	credited, err := h.Svc.SettleOrder(ctx, ev.OrderID)
	if err != nil {
		// Acknowledged anyway: the sweep asks NabooPay again.
		h.warn("licensing: naboopay webhook: settling failed, the sweep will retry", "order", ev.OrderID, "error", err)
	}
	response.JSON(w, http.StatusOK, map[string]any{"received": true, "credited": credited})
}

// ValidSignature checks NabooPay's X-Signature in constant time. It is what
// the kergui core checks too (httpapi.validSignature).
func ValidSignature(body []byte, sig, secret string) bool {
	got, err := hex.DecodeString(strings.TrimSpace(sig))
	if err != nil || len(got) == 0 {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// Sign is the X-Signature for a body, for tests and a curl in the pilot.
func Sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// checkoutDone is where NabooPay sends the payer back. The pass is credited
// by the webhook, not by this page, which anyone can open.
func (h *Handler) checkoutDone(w http.ResponseWriter, r *http.Request) {
	msg := "Paiement reçu. Votre pass sera actif dans quelques instants : revenez dans l'application Kergui."
	if r.URL.Query().Get("status") != "success" {
		msg = "Le paiement n'a pas abouti. Vous pouvez réessayer depuis l'application Kergui."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, `<!doctype html><html lang="fr"><head><meta charset="utf-8">`+
		`<meta name="viewport" content="width=device-width, initial-scale=1"><title>Kergui</title></head>`+
		`<body style="font-family:sans-serif;max-width:32rem;margin:3rem auto;padding:0 1rem"><p>`+
		html.EscapeString(msg)+`</p></body></html>`)
}

// --- plumbing -------------------------------------------------------------------

func (h *Handler) warn(msg string, args ...any) {
	if h.Log != nil {
		h.Log.Warn(msg, args...)
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return kernelerrors.BadRequest("malformed request body")
	}
	return nil
}

// Error codes the app reads. The kernel's generic codes say what kind of
// failure it is; these say which rule refused, so the app can word it.
const (
	CodeInvalidPhone   = "INVALID_PHONE"
	CodeInvalidBox     = "INVALID_BOX"
	CodeUnknownPass    = "UNKNOWN_PASS"
	CodeOTPRateLimited = "OTP_RATE_LIMITED"
	CodeOTPInvalid     = "OTP_INVALID"
	CodeOTPExpired     = "OTP_EXPIRED"
	CodeOTPLocked      = "OTP_LOCKED"
	CodeUnauthorized   = "SESSION_INVALID"
	CodeTrialPhoneUsed = "TRIAL_PHONE_USED"
	CodeTrialBoxUsed   = "TRIAL_BOX_USED"
	CodeMaxActive      = "MAX_ACTIVE"
	CodeNoActivePass   = "NO_ACTIVE_PASS"
	CodeSameBox        = "SAME_BOX"
	CodeTargetActive   = "TARGET_BOX_ACTIVE"
	CodeTransferLimit  = "TRANSFER_LIMIT"
	CodePaymentsOff    = "PAYMENTS_OFF"
	CodePaymentDown    = "PAYMENT_UNAVAILABLE"
	CodeWhatsAppDown   = "WHATSAPP_UNAVAILABLE"

	CodePlayOff        = "PLAY_OFF"
	CodeUnknownProduct = "UNKNOWN_PRODUCT"
	CodePlayInvalid    = "PLAY_PURCHASE_INVALID"
	CodePlayPending    = "PLAY_PURCHASE_PENDING"
	CodePlayInactive   = "PLAY_PURCHASE_INACTIVE"
	CodePlayTokenBound = "PLAY_TOKEN_BOUND"
	CodePlayDown       = "PLAY_UNAVAILABLE"
)

func appErr(code string, status int, err error) error {
	return kernelerrors.New(code, err.Error(), status, err)
}

// mapErr turns a domain or use-case error into the kernel's AppError.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, alerts.ErrInvalidPhone):
		return appErr(CodeInvalidPhone, http.StatusBadRequest, err)
	case errors.Is(err, domain.ErrInvalidBoxID):
		return appErr(CodeInvalidBox, http.StatusBadRequest, err)
	case errors.Is(err, domain.ErrUnknownPass):
		return appErr(CodeUnknownPass, http.StatusBadRequest, err)
	case errors.Is(err, usecase.ErrOTPRateLimited):
		return appErr(CodeOTPRateLimited, http.StatusTooManyRequests, err)
	case errors.Is(err, usecase.ErrOTPInvalid):
		return appErr(CodeOTPInvalid, http.StatusUnauthorized, err)
	case errors.Is(err, usecase.ErrOTPExpired):
		return appErr(CodeOTPExpired, http.StatusUnauthorized, err)
	case errors.Is(err, usecase.ErrOTPLocked):
		return appErr(CodeOTPLocked, http.StatusUnauthorized, err)
	case errors.Is(err, usecase.ErrUnauthenticated):
		return appErr(CodeUnauthorized, http.StatusUnauthorized, err)
	case errors.Is(err, domain.ErrTrialPhoneUsed):
		return appErr(CodeTrialPhoneUsed, http.StatusConflict, err)
	case errors.Is(err, domain.ErrTrialBoxUsed):
		return appErr(CodeTrialBoxUsed, http.StatusConflict, err)
	case errors.Is(err, domain.ErrMaxActive):
		return appErr(CodeMaxActive, http.StatusConflict, err)
	case errors.Is(err, domain.ErrNoActivePass):
		return appErr(CodeNoActivePass, http.StatusConflict, err)
	case errors.Is(err, domain.ErrSameBox):
		return appErr(CodeSameBox, http.StatusConflict, err)
	case errors.Is(err, usecase.ErrTargetActive):
		return appErr(CodeTargetActive, http.StatusConflict, err)
	case errors.Is(err, domain.ErrTransferLimit):
		return appErr(CodeTransferLimit, http.StatusConflict, err)
	case errors.Is(err, usecase.ErrPaymentsOff):
		return appErr(CodePaymentsOff, http.StatusServiceUnavailable, err)
	case errors.Is(err, coredomain.ErrPaymentUnavailable):
		return kernelerrors.New(CodePaymentDown, "the payment provider is unavailable, try again later", http.StatusBadGateway, err)
	case errors.Is(err, alerts.ErrNotActivated):
		return kernelerrors.New(CodeWhatsAppDown, "WhatsApp is unavailable, try again later", http.StatusServiceUnavailable, err)
	case errors.Is(err, usecase.ErrPlayOff):
		return appErr(CodePlayOff, http.StatusNotFound, err)
	case errors.Is(err, domain.ErrUnknownProduct):
		return appErr(CodeUnknownProduct, http.StatusBadRequest, err)
	case errors.Is(err, usecase.ErrPlayPurchaseInvalid):
		return appErr(CodePlayInvalid, http.StatusBadRequest, usecase.ErrPlayPurchaseInvalid)
	case errors.Is(err, usecase.ErrPlayPurchasePending):
		return appErr(CodePlayPending, http.StatusConflict, err)
	case errors.Is(err, usecase.ErrPlayPurchaseInactive):
		return appErr(CodePlayInactive, http.StatusConflict, err)
	case errors.Is(err, usecase.ErrPlayTokenBound):
		return appErr(CodePlayTokenBound, http.StatusConflict, err)
	case errors.Is(err, usecase.ErrPlayUnavailable):
		return kernelerrors.New(CodePlayDown, "Google Play is unavailable, try again later", http.StatusBadGateway, err)
	case errors.Is(err, usecase.ErrNotFound):
		return kernelerrors.NotFound("not found")
	default:
		return kernelerrors.WrapInternal(err)
	}
}
