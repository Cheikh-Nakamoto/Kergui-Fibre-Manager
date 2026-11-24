package whatsmeow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	kernelauth "github.com/agile-factory/kernel/kernel/auth"
	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"
	"github.com/agile-factory/kernel/platform/audit"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin/actionlog"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

// Session is what the admin routes drive: the running hub's own WhatsApp
// session.
type Session interface {
	Status() Status
	Link(ctx context.Context, number domain.Phone) (string, error)
	Unlink(ctx context.Context) error
}

// Admin serves the session's management to platform administrators only:
//
//	GET    /api/v1/admin/whatsapp       → Status
//	POST   /api/v1/admin/whatsapp/link  {"number": "+221…"} → {"code": "ABCD-EFGH"}
//	DELETE /api/v1/admin/whatsapp       → unlink (another number can be linked)
//
// They are mounted behind the hub's JWT middleware.
type Admin struct {
	Session Session
	// Number is the configured KERGUI_HUB_WHATSAPP_NUMBER, used when the
	// link request names none.
	Number domain.Phone
	// Audit receives the link and unlink gestures (the back-office's action
	// log); nil records nothing.
	Audit audit.EventLogger
}

// RegisterRoutes mounts the routes.
func (a *Admin) RegisterRoutes(mux *http.ServeMux) {
	const p = "/api/v1/admin/whatsapp"
	mux.HandleFunc("GET "+p, a.status)
	mux.HandleFunc("POST "+p+"/link", a.link)
	mux.HandleFunc("DELETE "+p, a.unlink)
}

func platformAdmin(r *http.Request) error {
	if kernelauth.UserIDFromContext(r.Context()) == "" {
		return kernelerrors.Unauthorized("authentication required")
	}
	if !kernelauth.IsPlatformAdmin(r.Context()) {
		return kernelerrors.Forbidden("platform administrators only")
	}
	return nil
}

func (a *Admin) status(w http.ResponseWriter, r *http.Request) {
	if err := platformAdmin(r); err != nil {
		response.Error(w, err)
		return
	}
	response.JSON(w, http.StatusOK, a.Session.Status())
}

func (a *Admin) link(w http.ResponseWriter, r *http.Request) {
	if err := platformAdmin(r); err != nil {
		response.Error(w, err)
		return
	}
	var req struct {
		Number string `json:"number"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			response.Error(w, kernelerrors.BadRequest("invalid JSON body"))
			return
		}
	}
	number := a.Number
	if req.Number != "" {
		p, err := domain.ParsePhone(req.Number)
		if err != nil {
			response.Error(w, kernelerrors.BadRequest("not a phone number"))
			return
		}
		number = p
	}
	if number == "" {
		response.Error(w, kernelerrors.BadRequest("no number: send {\"number\": \"+221…\"} or set KERGUI_HUB_WHATSAPP_NUMBER"))
		return
	}
	code, err := a.Session.Link(r.Context(), number)
	switch {
	case errors.Is(err, ErrAlreadyLinked):
		response.Error(w, kernelerrors.Conflict(err.Error()))
		return
	case err != nil:
		response.Error(w, err)
		return
	}
	actionlog.Record(r.Context(), a.Audit, actionlog.Gesture{
		Kind: actionlog.KindWhatsAppLinkRequested, TargetType: actionlog.TargetWhatsApp,
		Summary: "Code d’appairage demandé pour le numéro " + number.Masked(),
		Details: map[string]any{"number": number.Masked()},
	})
	response.JSON(w, http.StatusOK, map[string]string{
		"code":   code,
		"number": number.Masked(),
		"how":    "WhatsApp sur ce téléphone → Appareils connectés → Connecter un appareil → Connecter avec un numéro de téléphone, puis saisir le code.",
	})
}

func (a *Admin) unlink(w http.ResponseWriter, r *http.Request) {
	if err := platformAdmin(r); err != nil {
		response.Error(w, err)
		return
	}
	before := a.Session.Status()
	if err := a.Session.Unlink(r.Context()); err != nil {
		response.Error(w, err)
		return
	}
	summary := "Session WhatsApp du hub déliée"
	if p, err := domain.ParsePhone(before.Number); err == nil {
		summary += " (" + p.Masked() + ")"
	}
	actionlog.Record(r.Context(), a.Audit, actionlog.Gesture{
		Kind: actionlog.KindWhatsAppUnlinked, TargetType: actionlog.TargetWhatsApp, Summary: summary,
	})
	response.JSON(w, http.StatusOK, a.Session.Status())
}
