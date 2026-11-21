package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

// playBudget keeps the webhook under Pub/Sub's default 10 s ack deadline:
// past it the push is redelivered, which is harmless but wasteful.
const playBudget = 8 * time.Second

type playPurchaseRequest struct {
	BoxID         string `json:"boxId"`
	ProductID     string `json:"productId"`
	PurchaseToken string `json:"purchaseToken"`
}

// playPurchase is the play flavor reporting a purchase (after the billing
// flow, and on launch to restore it). It answers the subscription, in the
// shape of GET /subscriptions.
func (h *Handler) playPurchase(w http.ResponseWriter, r *http.Request) {
	var req playPurchaseRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	sub, err := h.Svc.PlayPurchase(r.Context(), usecase.PlayPurchaseInput{
		Account: accountFrom(r.Context()).ID, BoxID: req.BoxID, ProductID: req.ProductID, PurchaseToken: req.PurchaseToken,
	})
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusOK, h.view(sub))
}

// pubsubPush is a Pub/Sub push request; Data is a DeveloperNotification.
type pubsubPush struct {
	Message struct {
		Data      string `json:"data"`
		MessageID string `json:"messageId"`
	} `json:"message"`
}

// developerNotification is Google Play's RTDN payload.
type developerNotification struct {
	PackageName              string `json:"packageName"`
	SubscriptionNotification *struct {
		NotificationType int    `json:"notificationType"`
		PurchaseToken    string `json:"purchaseToken"`
	} `json:"subscriptionNotification"`
	VoidedPurchaseNotification *struct {
		PurchaseToken string `json:"purchaseToken"`
		ProductType   int    `json:"productType"` // 1 = subscription
	} `json:"voidedPurchaseNotification"`
	TestNotification *struct{} `json:"testNotification"`
}

// playWebhook receives Google Play's Real-time Developer Notifications
// through a Pub/Sub push subscription. The push is authenticated by its
// OIDC token; the notification is then only a hint naming a purchase token,
// whose state is asked from Google. Answers: 401 for a push that does not
// authenticate, 503 when Google or the store cannot be reached (Pub/Sub
// redelivers), 200 otherwise, including for messages the hub ignores, so a
// message it cannot use is not redelivered for days.
func (h *Handler) playWebhook(w http.ResponseWriter, r *http.Request) {
	if h.PlayPush == nil || h.Svc.Play == nil {
		http.NotFound(w, r)
		return
	}
	if err := h.PlayPush.Verify(r.Context(), bearer(r)); err != nil {
		if errors.Is(err, usecase.ErrPushUnauthenticated) {
			h.warn("licensing: play webhook with a bad push token, ignored", "error", err)
			response.Error(w, kernelerrors.Unauthorized("invalid push token"))
			return
		}
		h.warn("licensing: play webhook: cannot check the push token, Pub/Sub will retry", "error", err)
		response.Error(w, kernelerrors.ServiceUnavailable("cannot authenticate the push now"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		response.Error(w, kernelerrors.BadRequest("body too large"))
		return
	}
	n, ok := parseRTDN(body)
	if !ok {
		h.warn("licensing: play webhook: unreadable message, acknowledged")
		response.JSON(w, http.StatusOK, map[string]any{"received": true, "outcome": usecase.PlayIgnored})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), playBudget)
	defer cancel()
	res, err := h.Svc.PlayNotify(ctx, n)
	if err != nil {
		h.warn("licensing: play webhook: Google Play or the store failed, Pub/Sub will retry", "type", n.Type, "error", err)
		response.Error(w, kernelerrors.New(CodePlayDown, "try again later", http.StatusServiceUnavailable, err))
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"received": true, "outcome": res.Outcome})
}

// parseRTDN reads the push envelope. false means nothing usable.
func parseRTDN(body []byte) (usecase.PlayNotification, bool) {
	var push pubsubPush
	if err := json.Unmarshal(body, &push); err != nil {
		return usecase.PlayNotification{}, false
	}
	data, err := base64.StdEncoding.DecodeString(push.Message.Data)
	if err != nil {
		if data, err = base64.URLEncoding.DecodeString(push.Message.Data); err != nil {
			return usecase.PlayNotification{}, false
		}
	}
	var dn developerNotification
	if err := json.Unmarshal(data, &dn); err != nil {
		return usecase.PlayNotification{}, false
	}
	n := usecase.PlayNotification{PackageName: dn.PackageName, Test: dn.TestNotification != nil}
	switch {
	case dn.SubscriptionNotification != nil:
		n.PurchaseToken, n.Type = dn.SubscriptionNotification.PurchaseToken, dn.SubscriptionNotification.NotificationType
	case dn.VoidedPurchaseNotification != nil && dn.VoidedPurchaseNotification.ProductType == 1:
		// A refunded subscription: Google's state says what is left of it.
		n.PurchaseToken, n.Type = dn.VoidedPurchaseNotification.PurchaseToken, usecase.PlayRevoked
	}
	return n, true
}
