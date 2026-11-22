package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

// maxPushBody bounds a registration: a token and a platform.
const maxPushBody = 4 << 10

// pushTokenRequest is what the app registers: its Expo push token.
type pushTokenRequest struct {
	Token    string `json:"token"`
	Platform string `json:"platform"`
}

// registerPushToken stores the caller's app installation for this
// household (issue #49), so a new-device alert reaches it with the app
// closed. Any member may register their own phone: a viewer already sees the
// household's devices in the app, and the notification goes to that app,
// not to a third-party conversation (which is why the WhatsApp opt-in is
// for owners and admins only).
func (h *Handler) registerPushToken(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	if h.Targets == nil {
		response.Error(w, kernelerrors.Conflict("app notifications are not configured on this hub"))
		return
	}
	var req pushTokenRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPushBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		response.Error(w, kernelerrors.BadRequest("malformed request body"))
		return
	}
	t := domain.PushTarget{TenantID: caller.TenantID, UserID: caller.UserID, Platform: req.Platform, Token: req.Token}
	if err := t.Validate(); err != nil {
		response.Error(w, kernelerrors.BadRequest(err.Error()))
		return
	}
	if err := h.Targets.Register(r.Context(), t, h.Now().UTC()); err != nil {
		response.Error(w, kernelerrors.WrapInternal(err))
		return
	}
	response.JSON(w, http.StatusNoContent, nil)
}

// unregisterPushToken drops the caller's installation (sign-out, alerts
// turned off). Unknown tokens are not an error: the end state is the same.
func (h *Handler) unregisterPushToken(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	if h.Targets == nil {
		response.JSON(w, http.StatusNoContent, nil)
		return
	}
	token := r.PathValue("token")
	if err := (domain.PushTarget{Platform: domain.PlatformAndroid, Token: token}).Validate(); err != nil {
		response.Error(w, kernelerrors.BadRequest("not a push token"))
		return
	}
	if err := h.Targets.Unregister(r.Context(), caller.TenantID, caller.UserID, token); err != nil && !errors.Is(err, domain.ErrInvalidPushToken) {
		response.Error(w, kernelerrors.WrapInternal(err))
		return
	}
	response.JSON(w, http.StatusNoContent, nil)
}
