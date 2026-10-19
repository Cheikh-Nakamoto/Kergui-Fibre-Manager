// Package handler exposes the alerts module over HTTP: the opt-in route the
// owner's app calls with a kernel JWT, and the provider webhook the
// messaging provider calls.
package handler

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"
	"github.com/agile-factory/kernel/platform/audit"
	auditdomain "github.com/agile-factory/kernel/platform/audit/domain"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
)

// Audit actions this module records: who linked a number, who stopped, and
// what a reply asked for.
const (
	ActionWhatsAppInvited = auditdomain.AuditAction("WHATSAPP_INVITED")
	ActionWhatsAppLinked  = auditdomain.AuditAction("WHATSAPP_LINKED")
	ActionWhatsAppStopped = auditdomain.AuditAction("WHATSAPP_STOPPED")
)

// Replier acts on a household's reply to an alert. It is a port because
// "1 = block" belongs to the command path, not to the messaging one: this
// handler reads the message, authenticates the number, and hands over.
type Replier interface {
	Reply(ctx context.Context, msg domain.InboundMessage, intent domain.Intent) (string, error)
}

// Handler serves the alerts endpoints.
type Handler struct {
	Guard    *tenantaccess.Guard
	OptIn    usecase.OptIn
	Notifier domain.Notifier
	Repo     domain.Repository
	// Replier handles "1 = block"; nil until that path is wired, in which
	// case a numeric reply is answered politely and nothing happens.
	Replier Replier
	Audit   audit.EventLogger
	Now     func() time.Time
	// WebhookSecret is what makes the provider webhook trustworthy for a
	// provider that does not sign its calls (Evolution Go offers a URL and
	// nothing more). It is compared in constant time, and the route is
	// expected to be reachable only from the provider's own network.
	WebhookSecret string
	Log           Logger
}

// Logger is the slice of a logger this module uses.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// RegisterRoutes mounts the household routes (kernel JWT required).
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	const p = "/api/v1/tenants/{tenantId}/"
	mux.HandleFunc("POST "+p+"whatsapp/optin", h.invite)
	mux.HandleFunc("GET "+p+"whatsapp", h.status)
}

// RegisterWebhookRoutes mounts the provider's inbound route. It carries no
// JWT: the secret in the path is what authenticates the caller.
func (h *Handler) RegisterWebhookRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /whatsapp/v1/inbound/{secret}", h.inbound)
}

// --- household routes -----------------------------------------------------

type inviteResponse struct {
	WaLink    string    `json:"waLink"`
	QRPayload string    `json:"qrPayload"`
	Number    string    `json:"number"`
	ExpiresAt time.Time `json:"expiresAt"`
	Provider  provider  `json:"provider"`
}

// provider carries the notice the messaging provider's licence requires to
// be visible to administrators (Evolution Go's usage notification). The
// app's settings screen shows it.
type provider struct {
	Name   string `json:"name"`
	Notice string `json:"notice,omitempty"`
}

// invite issues the wa.me link that pre-fills "KERGUI <code>". Only an
// owner or an admin may ask: an alert says what is on the household's
// network, and a viewer does not get to route that to their own phone.
func (h *Handler) invite(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	if !caller.IsAdmin() {
		response.Error(w, kernelerrors.Forbidden("only the household's owner or an admin may link a WhatsApp number"))
		return
	}
	inv, err := h.OptIn.Invite(r.Context(), caller.TenantID, caller.UserID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	h.Audit.Execute(r.Context(), audit.LogEventParams{
		TenantID: caller.TenantID, ActorID: caller.UserID, Action: ActionWhatsAppInvited,
		TargetType: "whatsapp_optin", TargetID: caller.UserID,
		Metadata: map[string]any{"provider": h.Notifier.Name()},
	})
	response.JSON(w, http.StatusCreated, inviteResponse{
		WaLink: inv.Link, QRPayload: inv.QRPayload, Number: string(inv.Number),
		ExpiresAt: inv.ExpiresAt,
		Provider:  provider{Name: inv.Provider, Notice: inv.Notice},
	})
}

type statusResponse struct {
	Provider provider      `json:"provider"`
	OptIns   []optInStatus `json:"optIns"`
}

// optInStatus is what the app shows about a linked number. The number is
// masked: the app knows whose it is, and a full number has no business
// coming back out of the hub (core rule C5 in spirit — what went in for one
// purpose does not come back for another).
type optInStatus struct {
	UserID  string    `json:"userId"`
	Phone   string    `json:"phone"`
	Active  bool      `json:"active"`
	LinkedA time.Time `json:"linkedAt"`
}

// status tells the app who in the household receives WhatsApp alerts, and
// which provider carries them.
func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	optIns, err := h.Repo.ActiveOptIns(r.Context(), caller.TenantID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	out := statusResponse{
		Provider: provider{Name: h.Notifier.Name(), Notice: h.Notifier.Attribution()},
		OptIns:   make([]optInStatus, 0, len(optIns)),
	}
	for _, o := range optIns {
		out.OptIns = append(out.OptIns, optInStatus{
			UserID: o.UserID, Phone: o.Phone.Masked(), Active: o.Active(), LinkedA: o.CreatedAt,
		})
	}
	response.JSON(w, http.StatusOK, out)
}

// --- the provider's webhook ----------------------------------------------

// inbound reads one message from the provider.
//
// It always answers 200 once the caller is authenticated, whatever the
// message turns out to be: a provider that is told "error" retries, and a
// retried "1" must not block a second device. What the hub could not do is
// said to the household in the conversation, not in an HTTP status.
func (h *Handler) inbound(w http.ResponseWriter, r *http.Request) {
	if !h.authentic(r.PathValue("secret")) {
		// Deliberately 404: an unauthenticated caller learns nothing about
		// whether this hub has a webhook at all.
		http.NotFound(w, r)
		return
	}
	msg, err := h.Notifier.ParseInbound(r)
	switch {
	case errors.Is(err, domain.ErrNotAuthentic):
		http.NotFound(w, r)
		return
	case errors.Is(err, domain.ErrIgnored):
		w.WriteHeader(http.StatusOK)
		return
	case err != nil:
		if h.Log != nil {
			h.Log.Warn("alerts: unreadable inbound message", "provider", h.Notifier.Name(), "error", err)
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	h.handle(r.Context(), msg)
	w.WriteHeader(http.StatusOK)
}

// authentic compares the path secret in constant time.
func (h *Handler) authentic(got string) bool {
	if h.WebhookSecret == "" {
		return false // no secret configured, no inbound route
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.WebhookSecret)) == 1
}

// handle acts on one message, and answers in the conversation.
func (h *Handler) handle(ctx context.Context, msg domain.InboundMessage) {
	intent := domain.ReadIntent(msg.Text)
	switch intent.Keyword {
	case domain.KeywordOptIn:
		h.redeem(ctx, msg, intent)
	case domain.KeywordStop:
		h.stop(ctx, msg)
	case domain.KeywordChoice:
		h.reply(ctx, msg, intent)
	default:
		h.say(ctx, msg.From, "Je n'ai pas compris. Répondez par le numéro de l'appareil à bloquer, "+
			"ou STOP pour ne plus recevoir ces alertes.")
	}
}

func (h *Handler) redeem(ctx context.Context, msg domain.InboundMessage, intent domain.Intent) {
	o, err := h.OptIn.Redeem(ctx, msg, intent.Code)
	if err != nil {
		if h.Log != nil {
			h.Log.Warn("alerts: opt-in refused", "from", msg.From.Masked(), "error", err)
		}
		h.say(ctx, msg.From, "Ce code n'est plus valable. Ouvrez l'application Kergui et demandez un nouveau lien.")
		return
	}
	h.Audit.Execute(ctx, audit.LogEventParams{
		TenantID: o.TenantID, ActorID: o.UserID, Action: ActionWhatsAppLinked,
		TargetType: "whatsapp_optin", TargetID: o.UserID,
		// The number is masked here too: an audit trail is read by people.
		Metadata: map[string]any{"provider": h.Notifier.Name(), "phone": o.Phone.Masked()},
	})
	h.say(ctx, msg.From, usecase.ComposeOptInConfirmation(h.Notifier.Attribution()))
}

func (h *Handler) stop(ctx context.Context, msg domain.InboundMessage) {
	// Stopping is answered for any number, registered or not: someone who
	// asks not to be messaged is never asked to prove anything first.
	if err := h.OptIn.Stop(ctx, msg.From); err != nil && h.Log != nil {
		h.Log.Warn("alerts: cannot stop the alerts", "from", msg.From.Masked(), "error", err)
	}
	if optIns, err := h.Repo.OptInsByPhone(ctx, msg.From); err == nil {
		for _, o := range optIns {
			h.Audit.Execute(ctx, audit.LogEventParams{
				TenantID: o.TenantID, ActorID: o.UserID, Action: ActionWhatsAppStopped,
				TargetType: "whatsapp_optin", TargetID: o.UserID,
				Metadata: map[string]any{"phone": o.Phone.Masked()},
			})
		}
	}
	h.say(ctx, msg.From, "C'est noté : vous ne recevrez plus d'alertes ici. "+
		"Vous pouvez les réactiver depuis l'application Kergui.")
}

func (h *Handler) reply(ctx context.Context, msg domain.InboundMessage, intent domain.Intent) {
	if h.Replier == nil {
		h.say(ctx, msg.From, "Le blocage par message n'est pas encore activé sur ce hub. "+
			"Ouvrez l'application Kergui pour bloquer cet appareil.")
		return
	}
	answer, err := h.Replier.Reply(ctx, msg, intent)
	if err != nil {
		if h.Log != nil {
			h.Log.Warn("alerts: a reply could not be acted on",
				"from", msg.From.Masked(), "choices", usecase.FormatChoices(intent.Choices), "error", err)
		}
		if answer == "" {
			answer = "Je n'ai pas pu bloquer cet appareil. Ouvrez l'application Kergui pour réessayer."
		}
	}
	h.say(ctx, msg.From, answer)
}

// say answers in the conversation. A message that cannot be sent is logged,
// not retried: the household's next alert will come anyway.
func (h *Handler) say(ctx context.Context, to domain.Phone, text string) {
	if _, err := h.Notifier.Send(ctx, to, domain.OutboundMessage{Text: text}); err != nil && h.Log != nil {
		h.Log.Warn("alerts: cannot answer", "to", to.Masked(), "error", err)
	}
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotActivated):
		return kernelerrors.Conflict("WhatsApp is not configured on this hub")
	case errors.Is(err, domain.ErrNoOptIn):
		return kernelerrors.NotFound("no pending opt-in")
	case errors.Is(err, domain.ErrInvalidPhone):
		return kernelerrors.BadRequest("not a usable phone number")
	default:
		return kernelerrors.WrapInternal(err)
	}
}
