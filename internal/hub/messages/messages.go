// Package messages is the back-office's « Messages » (issue #80, parent
// #67): an announcement the Kergui team writes once — a title, a text — and
// sends to every household, to one level, or to one household, with the
// history of what was said to whom.
//
// Push is the channel for everyone: the announcement goes through the alerts
// module's push sender to the app installations the households registered,
// its data marked {kind: "announcement"} so the app files it in its
// notifications. A message and its deliveries are channel-neutral rows.
//
// WhatsApp (issue #81) goes through whatsmeow, which is not WhatsApp's
// official API: bulk sending gets the Kergui number banned. So a WhatsApp
// message goes to one household or to a small hand-picked group (20
// numbers by default), only to numbers that opted in and did not send
// STOP, and through a queue in Postgres that sends one message every 40 s
// (whatsapp.go). Announcements to everyone stay on push.
//
// Routes, platform administrators only (the kernel JWT's ROLE_ADMIN):
//
//	POST /api/v1/admin/messages/preview  {audience, value, channel?, numbers?}               → who it reaches
//	POST /api/v1/admin/messages          {title, body, audience, value, channel?, numbers?} → sent or queued
//	GET  /api/v1/admin/messages?limit=   the history, newest first, with its counters
//
// audience is "all", "tier" (value "free", "basic" or "max") or
// "household" (value a household's id, or its owner's number in any form
// the sign-in accepts: 77…, +221…, 00221…). channel is "push" (default) or
// "whatsapp"; WhatsApp takes "household" or "numbers" (numbers: the
// hand-picked list, in any of those forms).
//
// A household's level is the highest tier among its boxes' passes still
// running: the subscriptions of the account that owns the household
// (hub_identity_households.account_id) whose expires_at is in the future,
// Max above Basic; Gratuit when none runs (or the household has no
// account, signed up by e-mail). The 7-day trial is a Max pass, so it
// counts as Max while it runs.
//
// Every send lands in the action log (ANNOUNCEMENT_SENT).
package messages

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	kernelauth "github.com/agile-factory/kernel/kernel/auth"
	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"
	"github.com/agile-factory/kernel/platform/audit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin/actionlog"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

// Migrations holds the module's tables.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// Limits of an announcement, in characters.
const (
	MaxTitle = 80
	MaxBody  = 500
)

// Audiences.
const (
	AudienceAll       = "all"
	AudienceTier      = "tier"
	AudienceHousehold = "household"
	// AudienceNumbers is a hand-picked group of numbers, WhatsApp only.
	AudienceNumbers = "numbers"
)

// Channels.
const (
	ChannelPush     = "push"
	ChannelWhatsApp = "whatsapp"
)

// PushKind is the push data's kind: what the app files as a « message ».
const PushKind = "announcement"

// Config wires the module.
type Config struct {
	Pool *pgxpool.Pool
	// Pusher is the alerts module's push sender (console or Expo). Nil
	// turns sending off (503); the preview and the history still answer.
	Pusher alertsdomain.Pusher
	// Targets forgets a token the provider says is gone, as the alerts do.
	// Nil leaves it.
	Targets alertsdomain.PushTargets
	// Notifier is the WhatsApp provider (whatsmeow, or the console). Nil
	// turns the WhatsApp channel off (503); its queue then waits.
	Notifier alertsdomain.Notifier
	// SendInterval spaces two WhatsApp messages of the queue (40 s by
	// default); GroupMax caps a hand-picked group (20 by default);
	// RetryDelay is the wait before a failed send is tried again, times
	// the attempts so far (2 min by default).
	SendInterval time.Duration
	GroupMax     int
	RetryDelay   time.Duration
	Audit        audit.EventLogger
	Logger       *slog.Logger
	Now          func() time.Time
}

// Module serves the routes and runs the WhatsApp queue.
type Module struct {
	cfg Config
	// wake tells the queue's worker a message was just queued.
	wake chan struct{}
}

// New wires the module.
func New(cfg Config) *Module {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.SendInterval <= 0 {
		cfg.SendInterval = DefaultSendInterval
	}
	if cfg.GroupMax <= 0 {
		cfg.GroupMax = DefaultGroupMax
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = DefaultRetryDelay
	}
	return &Module{cfg: cfg, wake: make(chan struct{}, 1)}
}

// RegisterRoutes mounts the routes, behind the JWT middleware.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	const p = "/api/v1/admin/messages"
	mux.HandleFunc("POST "+p+"/preview", guard(m.preview))
	mux.HandleFunc("POST "+p, guard(m.send))
	mux.HandleFunc("GET "+p, guard(m.history))
}

// guard lets platform administrators through, and nobody else, as the
// admin module does.
func guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if kernelauth.UserIDFromContext(r.Context()) == "" {
			response.Error(w, kernelerrors.Unauthorized("authentication required"))
			return
		}
		if !kernelauth.IsPlatformAdmin(r.Context()) {
			response.Error(w, kernelerrors.Forbidden("platform administrators only"))
			return
		}
		next(w, r)
	}
}

// --- the audience -------------------------------------------------------------

// tierRank orders the levels; a household's is the highest running.
var tierRank = map[string]int{"free": 0, "basic": 1, "max": 2}

type householdRef struct {
	TenantID string `json:"tenantId"`
	Name     string `json:"name"`
	Phone    string `json:"phone,omitempty"`
}

// audience is who a message reaches.
type audience struct {
	Kind  string
	Value string // the tier, or the household's id
	// Household is the one household of a "household" audience.
	Household *householdRef
	// Households counts the households in the audience, WithApp those with
	// at least one phone to push to.
	Households int
	WithApp    int
	// Targets are the app installations, one per token.
	Targets []alertsdomain.PushTarget
}

var (
	errBadAudience = errors.New("bad audience")
	errNoHousehold = errors.New("no such household")
)

// resolve reads an audience and lists the phones it reaches.
func (m *Module) resolve(ctx context.Context, kind, value string) (audience, error) {
	a := audience{Kind: strings.TrimSpace(kind)}
	value = strings.TrimSpace(value)
	var cond string
	var arg any
	switch a.Kind {
	case AudienceAll:
		cond = "true"
	case AudienceTier:
		rank, ok := tierRank[strings.ToLower(value)]
		if !ok {
			return a, fmt.Errorf("%w: value: a level, free, basic or max", errBadAudience)
		}
		a.Value = strings.ToLower(value)
		cond, arg = "hh.rank = $2", rank
	case AudienceHousehold:
		h, err := m.findHousehold(ctx, value)
		if err != nil {
			return a, err
		}
		a.Value, a.Household = h.TenantID, &h
		cond, arg = "hh.tenant_id = $2::uuid", h.TenantID
	default:
		return a, fmt.Errorf("%w: audience: all, tier or household", errBadAudience)
	}
	args := []any{m.cfg.Now().UTC()}
	if arg != nil {
		args = append(args, arg)
	}
	rows, err := m.cfg.Pool.Query(ctx, `
		WITH hh AS (
		  SELECT t.id AS tenant_id,
		    COALESCE((SELECT max(CASE s.tier WHEN 'max' THEN 2 WHEN 'basic' THEN 1 ELSE 0 END)
		              FROM hub_licensing_subscriptions s
		              WHERE s.account_id = h.account_id AND s.expires_at > $1), 0) AS rank
		  FROM tenants t LEFT JOIN hub_identity_households h ON h.tenant_id = t.id
		  WHERE t.is_active)
		SELECT hh.tenant_id::text, p.user_id::text, p.platform, p.token
		FROM hh LEFT JOIN hub_push_tokens p ON p.tenant_id = hh.tenant_id
		WHERE `+cond+`
		ORDER BY hh.tenant_id, p.created_at`, args...)
	if err != nil {
		return a, err
	}
	defer rows.Close()
	seenHousehold := map[string]bool{}
	withApp := map[string]bool{}
	seenToken := map[string]bool{}
	for rows.Next() {
		var tenant string
		var user, platform, token *string
		if err := rows.Scan(&tenant, &user, &platform, &token); err != nil {
			return a, err
		}
		if !seenHousehold[tenant] {
			seenHousehold[tenant] = true
			a.Households++
		}
		if token == nil {
			continue
		}
		withApp[tenant] = true
		// A phone in two households hears an announcement once.
		if seenToken[*token] {
			continue
		}
		seenToken[*token] = true
		a.Targets = append(a.Targets, alertsdomain.PushTarget{TenantID: tenant, UserID: deref(user), Platform: deref(platform), Token: *token})
	}
	a.WithApp = len(withApp)
	return a, rows.Err()
}

// findHousehold reads a household's id or its owner's number.
func (m *Module) findHousehold(ctx context.Context, value string) (householdRef, error) {
	var h householdRef
	if value == "" {
		return h, fmt.Errorf("%w: value: the household's number or id", errBadAudience)
	}
	const sel = `
		SELECT t.id::text, t.name, COALESCE(a.phone, '')
		FROM tenants t
		LEFT JOIN hub_identity_households hh ON hh.tenant_id = t.id
		LEFT JOIN hub_licensing_accounts a ON a.id = hh.account_id`
	var row pgx.Row
	if _, err := uuid.Parse(value); err == nil {
		row = m.cfg.Pool.QueryRow(ctx, sel+` WHERE t.id = $1`, value)
	} else {
		phone, err := alertsdomain.ParsePhone(value)
		if err != nil {
			return h, fmt.Errorf("%w: value: the household's number or id", errBadAudience)
		}
		row = m.cfg.Pool.QueryRow(ctx, sel+` WHERE a.phone = $1 ORDER BY t.created_at LIMIT 1`, string(phone))
	}
	if err := row.Scan(&h.TenantID, &h.Name, &h.Phone); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return h, errNoHousehold
		}
		return h, err
	}
	return h, nil
}

func audienceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errBadAudience):
		response.Error(w, kernelerrors.BadRequest(strings.TrimPrefix(err.Error(), errBadAudience.Error()+": ")))
	case errors.Is(err, errTooMany):
		response.Error(w, kernelerrors.BadRequest(strings.TrimPrefix(err.Error(), errTooMany.Error()+": ")))
	case errors.Is(err, errNoHousehold):
		response.Error(w, kernelerrors.NotFound("no household for this number or id"))
	default:
		response.Error(w, kernelerrors.WrapDBError(err))
	}
}

// --- preview --------------------------------------------------------------------

type audienceRequest struct {
	Audience string   `json:"audience"`
	Value    string   `json:"value"`
	Channel  string   `json:"channel"`
	Numbers  []string `json:"numbers"`
}

type previewResponse struct {
	Channel           string        `json:"channel"`
	Audience          string        `json:"audience"`
	Value             string        `json:"value,omitempty"`
	Household         *householdRef `json:"household,omitempty"`
	Households        int           `json:"households"`
	HouseholdsWithApp int           `json:"householdsWithApp"`
	Phones            int           `json:"phones"`
	// WhatsApp only: the numbers it goes to, those refused and why, and
	// how long the queue takes to send it.
	*whatsAppPreview
}

func (a audience) preview() previewResponse {
	return previewResponse{
		Channel: ChannelPush, Audience: a.Kind, Value: a.Value, Household: a.Household,
		Households: a.Households, HouseholdsWithApp: a.WithApp, Phones: len(a.Targets),
	}
}

func (m *Module) preview(w http.ResponseWriter, r *http.Request) {
	var req audienceRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		response.Error(w, kernelerrors.BadRequest("invalid JSON body"))
		return
	}
	switch strings.TrimSpace(req.Channel) {
	case "", ChannelPush:
	case ChannelWhatsApp:
		a, err := m.resolveWhatsApp(r.Context(), req.Audience, req.Value, req.Numbers)
		if err != nil {
			audienceError(w, err)
			return
		}
		p, err := m.previewWhatsApp(r.Context(), a)
		if err != nil {
			response.Error(w, kernelerrors.WrapDBError(err))
			return
		}
		response.JSON(w, http.StatusOK, p)
		return
	default:
		response.Error(w, kernelerrors.BadRequest("channel: push or whatsapp"))
		return
	}
	a, err := m.resolve(r.Context(), req.Audience, req.Value)
	if err != nil {
		audienceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, a.preview())
}

// --- send -------------------------------------------------------------------------

type sendRequest struct {
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Audience string   `json:"audience"`
	Value    string   `json:"value"`
	Channel  string   `json:"channel"`
	Numbers  []string `json:"numbers"`
}

type sendResponse struct {
	MessageID string `json:"messageId"`
	previewResponse
	Sent   int `json:"sent"`
	Failed int `json:"failed"`
	// Queued is what the WhatsApp queue still has to send.
	Queued int `json:"queued"`
}

func (m *Module) send(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req sendRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		response.Error(w, kernelerrors.BadRequest("invalid JSON body"))
		return
	}
	title, body := strings.TrimSpace(req.Title), strings.TrimSpace(req.Body)
	switch {
	case title == "" || utf8.RuneCountInString(title) > MaxTitle:
		response.Error(w, kernelerrors.BadRequest(fmt.Sprintf("title: 1 to %d characters", MaxTitle)))
		return
	case body == "" || utf8.RuneCountInString(body) > MaxBody:
		response.Error(w, kernelerrors.BadRequest(fmt.Sprintf("body: 1 to %d characters", MaxBody)))
		return
	}
	channel := strings.TrimSpace(req.Channel)
	if channel == "" {
		channel = ChannelPush
	}
	switch channel {
	case ChannelPush:
	case ChannelWhatsApp:
		m.sendWhatsApp(w, r, title, body, req)
		return
	default:
		response.Error(w, kernelerrors.BadRequest("channel: push or whatsapp"))
		return
	}
	a, err := m.resolve(ctx, req.Audience, req.Value)
	if err != nil {
		audienceError(w, err)
		return
	}
	if m.cfg.Pusher == nil {
		response.Error(w, kernelerrors.ServiceUnavailable("app notifications are not configured on this hub"))
		return
	}
	if len(a.Targets) == 0 {
		response.Error(w, kernelerrors.Conflict("no phone to reach in this audience"))
		return
	}

	id := uuid.NewString()
	var tenant *string
	if a.Household != nil {
		tenant = &a.Household.TenantID
	}
	author := kernelauth.UserIDFromContext(ctx)
	if _, err := m.cfg.Pool.Exec(ctx, `
		INSERT INTO hub_messages (id, title, body, channel, audience, audience_value, tenant_id, author_id, households, phones, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		id, title, body, channel, a.Kind, a.Value, tenant, author, a.Households, len(a.Targets), m.cfg.Now().UTC()); err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}

	// The sends outlive the admin's request: a page closed halfway does not
	// leave half the households untold.
	sendCtx := context.WithoutCancel(ctx)
	out := sendResponse{MessageID: id, previewResponse: a.preview()}
	for _, t := range a.Targets {
		if m.deliver(sendCtx, id, title, body, t) {
			out.Sent++
		} else {
			out.Failed++
		}
	}

	actionlog.Record(ctx, m.cfg.Audit, actionlog.Gesture{
		Kind: actionlog.KindAnnouncementSent, TenantID: deref(tenant),
		TargetType: actionlog.TargetMessage, TargetID: id,
		Summary: fmt.Sprintf("Annonce « %s » envoyée (%s) : %d foyer(s), %d téléphone(s), %d échec(s)",
			title, a.label(), a.Households, len(a.Targets), out.Failed),
		Details: map[string]any{"title": title, "channel": channel, "audience": a.Kind, "value": a.Value,
			"households": a.Households, "phones": len(a.Targets), "sent": out.Sent, "failed": out.Failed},
	})
	response.JSON(w, http.StatusCreated, out)
}

// deliver pushes one announcement to one phone and records it. A token the
// provider says is gone is forgotten, as the alerts do.
func (m *Module) deliver(ctx context.Context, id, title, body string, t alertsdomain.PushTarget) bool {
	msg := alertsdomain.PushMessage{
		Title: title, Body: body,
		Data: map[string]string{"kind": PushKind, "messageId": id, "title": title, "body": body, "tenantId": t.TenantID},
	}
	err := m.cfg.Pusher.Push(ctx, t, msg)
	status, reason := "sent", ""
	if err != nil {
		status, reason = "failed", err.Error()
		if errors.Is(err, alertsdomain.ErrPushTokenGone) && m.cfg.Targets != nil {
			if ferr := m.cfg.Targets.Forget(ctx, t.Token); ferr != nil && m.cfg.Logger != nil {
				m.cfg.Logger.Warn("messages: cannot forget a gone push token", "error", ferr)
			}
		} else if m.cfg.Logger != nil {
			m.cfg.Logger.Warn("messages: push failed", "provider", m.cfg.Pusher.Name(),
				"user_id", t.UserID, "platform", t.Platform, "error", err)
		}
	}
	var user *string
	if t.UserID != "" {
		user = &t.UserID
	}
	if _, derr := m.cfg.Pool.Exec(ctx, `
		INSERT INTO hub_message_deliveries (message_id, tenant_id, user_id, channel, provider, device, status, error, sent_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, t.TenantID, user, ChannelPush, m.cfg.Pusher.Name(), device(t), status, reason, m.cfg.Now().UTC()); derr != nil && m.cfg.Logger != nil {
		m.cfg.Logger.Warn("messages: cannot record a delivery", "message_id", id, "error", derr)
	}
	return err == nil
}

// device tells two phones apart without keeping the token.
func device(t alertsdomain.PushTarget) string {
	tok := strings.TrimSuffix(t.Token, "]")
	if len(tok) > 6 {
		tok = tok[len(tok)-6:]
	}
	return t.Platform + " …" + tok
}

func (a audience) label() string {
	switch a.Kind {
	case AudienceTier:
		return "niveau " + map[string]string{"free": "Gratuit", "basic": "Basic", "max": "Max"}[a.Value]
	case AudienceHousehold:
		if a.Household != nil && a.Household.Phone != "" {
			return "foyer " + a.Household.Phone
		}
		return "foyer " + a.Value
	}
	return "tous les foyers"
}

// --- history ----------------------------------------------------------------------

type author struct {
	ID    string `json:"id,omitempty"`
	Email string `json:"email,omitempty"`
}

type historyEntry struct {
	ID         string        `json:"id"`
	Title      string        `json:"title"`
	Body       string        `json:"body"`
	Channel    string        `json:"channel"`
	Audience   string        `json:"audience"`
	Value      string        `json:"value,omitempty"`
	Household  *householdRef `json:"household,omitempty"`
	Author     author        `json:"author"`
	CreatedAt  time.Time     `json:"createdAt"`
	Households int           `json:"households"`
	Phones     int           `json:"phones"`
	Sent       int           `json:"sent"`
	Failed     int           `json:"failed"`
	// Queued is what the WhatsApp queue has yet to send.
	Queued int `json:"queued"`
}

func (m *Module) history(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit := 50
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			limit = min(n, 200)
		}
	}
	rows, err := m.cfg.Pool.Query(ctx, `
		SELECT m.id::text, m.title, m.body, m.channel, m.audience, m.audience_value,
		       COALESCE(m.tenant_id::text, ''), COALESCE(t.name, ''), COALESCE(a.phone, ''),
		       COALESCE(m.author_id::text, ''), COALESCE(u.email, ''), m.created_at, m.households, m.phones,
		       (SELECT count(*) FROM hub_message_deliveries d WHERE d.message_id = m.id AND d.status = 'sent'),
		       (SELECT count(*) FROM hub_message_deliveries d WHERE d.message_id = m.id AND d.status = 'failed'),
		       (SELECT count(*) FROM hub_whatsapp_queue q WHERE q.message_id = m.id AND q.status = 'queued')
		FROM hub_messages m
		LEFT JOIN users u ON u.id = m.author_id
		LEFT JOIN tenants t ON t.id = m.tenant_id
		LEFT JOIN hub_identity_households hh ON hh.tenant_id = m.tenant_id
		LEFT JOIN hub_licensing_accounts a ON a.id = hh.account_id
		ORDER BY m.created_at DESC, m.id DESC LIMIT $1`, limit)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	defer rows.Close()
	out := []historyEntry{}
	for rows.Next() {
		var e historyEntry
		var h householdRef
		if err := rows.Scan(&e.ID, &e.Title, &e.Body, &e.Channel, &e.Audience, &e.Value,
			&h.TenantID, &h.Name, &h.Phone, &e.Author.ID, &e.Author.Email, &e.CreatedAt,
			&e.Households, &e.Phones, &e.Sent, &e.Failed, &e.Queued); err != nil {
			response.Error(w, kernelerrors.WrapDBError(err))
			return
		}
		if h.TenantID != "" {
			e.Household = &h
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	var pending int
	if err := m.cfg.Pool.QueryRow(ctx, `SELECT count(*) FROM hub_whatsapp_queue WHERE status = 'queued'`).Scan(&pending); err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{
		"messages": out, "push": m.cfg.Pusher != nil,
		"limits": map[string]int{"title": MaxTitle, "body": MaxBody},
		"whatsapp": map[string]any{
			"enabled": m.cfg.Notifier != nil, "intervalSeconds": m.cfg.SendInterval.Seconds(),
			"groupMax": m.cfg.GroupMax, "pending": pending,
		},
	})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
