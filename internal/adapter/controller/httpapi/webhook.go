package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

const maxWebhookBody = 64 << 10

// paymentWebhook receives NabooPay's "payment_status" notification
// (docs.naboopay.com/api-reference/webhooks): X-Signature is the hex
// HMAC-SHA256 of the raw body, keyed with the webhook secret.
//
// The notification is only a hint: its status is never trusted. kergui asks
// NabooPay's API about the order (Billing.RefreshByOrder), so a replayed or
// forged call cannot mark anything paid. The 30 s polling stays the safety net.
func (s *Server) paymentWebhook(w http.ResponseWriter, r *http.Request) {
	if s.cfg.WebhookSecret == "" || s.svc.Billing == nil {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	if !validSignature(body, r.Header.Get("X-Signature"), s.cfg.WebhookSecret) {
		s.svc.Logger.Warnf("payment webhook: bad signature from %s, ignored", r.RemoteAddr)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	var ev struct {
		OrderID string `json:"order_id"`
	}
	if err := json.Unmarshal(body, &ev); err != nil || ev.OrderID == "" {
		http.Error(w, "order_id missing", http.StatusBadRequest)
		return
	}
	// NabooPay gives up after 5 s: acknowledge first, check afterwards.
	w.WriteHeader(http.StatusOK)
	go s.refreshOrder(ev.OrderID)
}

func (s *Server) refreshOrder(orderID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, ok, err := s.svc.Billing.RefreshByOrder(ctx, orderID)
	switch {
	case err != nil:
		s.svc.Logger.Warnf("payment webhook: order %s: status check failed: %v (polling will retry)", orderID, err)
	case !ok:
		s.svc.Logger.Infof("payment webhook: order %s: no pending payment (already settled or unknown)", orderID)
	default:
		s.svc.Logger.Infof("payment webhook: order %s: payment %s is %s", orderID, p.ID, p.Status)
		if p.Status == domain.PaymentPaid {
			s.enforceSoon()
			s.changed()
		}
	}
}

func validSignature(body []byte, sig, secret string) bool {
	got, err := hex.DecodeString(sig)
	if err != nil || len(got) == 0 {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
