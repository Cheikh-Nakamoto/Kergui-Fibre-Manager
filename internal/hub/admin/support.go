package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin/actionlog"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	licdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	licusecase "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

// The « Support » page (issue #78, parent #67): a customer found by their
// number, everything about them on one page, and the three gestures support
// makes for them, each with a reason and in the action log.
//
//	GET  /api/v1/admin/support/lookup?phone=77…|+221…|00221…
//	GET  /api/v1/admin/support/accounts/{accountId}
//	POST /api/v1/admin/support/accounts/{accountId}/extend          {boxId, days, reason}
//	POST /api/v1/admin/support/accounts/{accountId}/transfer        {fromBoxId, toBoxId, reason}
//	POST /api/v1/admin/support/accounts/{accountId}/revoke-sessions {reason}
//	GET  /api/v1/admin/households/{tenantId}/history?limit=&before=
//
// The rules are the licensing module's (Support); nothing here sends the
// customer a sign-in code, so support cannot take an account over.

// Support is what the page needs of the licensing module: its use cases,
// not new rules. *licusecase.Service satisfies it.
type Support interface {
	FindAccount(ctx context.Context, rawPhone string) (licusecase.Account, error)
	AccountByID(ctx context.Context, id licdomain.AccountID) (licusecase.Account, error)
	Subscriptions(ctx context.Context, a licdomain.AccountID) ([]licdomain.Subscription, error)
	WaitingPasses(ctx context.Context, a licdomain.AccountID) ([]licusecase.Payment, error)
	TrialStatus(ctx context.Context, a licdomain.AccountID, phone licdomain.Phone) (licusecase.TrialStatus, error)
	Payments(ctx context.Context, a licdomain.AccountID) ([]licusecase.Payment, error)
	ExtendPass(ctx context.Context, a licdomain.AccountID, rawBox string, days int) (licdomain.Subscription, error)
	AdminTransfer(ctx context.Context, a licdomain.AccountID, rawFrom, rawTo string) (licdomain.Subscription, error)
	RevokeSessions(ctx context.Context, a licdomain.AccountID) (int, error)
	// The household page's pass gestures (issue #98).
	RenewPass(ctx context.Context, in licusecase.RenewInput) (licdomain.Subscription, error)
	GrantLifetime(ctx context.Context, a licdomain.AccountID, rawBox string, t licdomain.Tier) (licdomain.Subscription, error)
	RevokeLifetime(ctx context.Context, a licdomain.AccountID, rawBox string) (licdomain.Subscription, error)
}

var _ Support = (*licusecase.Service)(nil)

// --- lookup ----------------------------------------------------------------

func (m *Module) lookup(w http.ResponseWriter, r *http.Request) {
	if !m.supportOn(w) {
		return
	}
	acc, err := m.cfg.Support.FindAccount(r.Context(), r.URL.Query().Get("phone"))
	if errors.Is(err, alertsdomain.ErrInvalidPhone) {
		response.Error(w, kernelerrors.BadRequest("not a phone number"))
		return
	}
	if err != nil {
		supportError(w, err, "no account for this number")
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{"accountId": string(acc.ID), "phone": string(acc.Phone)})
}

// --- the customer's page -----------------------------------------------------

type supportSubscription struct {
	BoxID     string    `json:"boxId"`
	Tier      string    `json:"tier"`
	ExpiresAt time.Time `json:"expiresAt"`
	Active    bool      `json:"active"`
	// Channel is "play" for a Google Play subscription, empty for prepaid.
	Channel string `json:"channel,omitempty"`
	// Lifetime is a pass with no end (issue #98): ExpiresAt is 9999-12-31.
	Lifetime bool `json:"lifetime,omitempty"`
}

func subscriptionOf(s licdomain.Subscription, now time.Time) supportSubscription {
	return supportSubscription{
		BoxID: string(s.BoxID), Tier: string(s.Tier), ExpiresAt: s.ExpiresAt,
		Active: s.Active(now), Channel: string(s.Channel), Lifetime: s.Lifetime,
	}
}

type waitingPass struct {
	PaymentID  string     `json:"paymentId"`
	PassCode   string     `json:"passCode"`
	Provider   string     `json:"provider"`
	AmountXOF  int64      `json:"amountXof"`
	CreditedAt *time.Time `json:"creditedAt,omitempty"`
}

type trialView struct {
	State       string     `json:"state"`
	BoxID       string     `json:"boxId,omitempty"`
	RequestedAt *time.Time `json:"requestedAt,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	EndsAt      *time.Time `json:"endsAt,omitempty"`
}

type sessionDevice struct {
	Device    string    `json:"device"`
	CreatedAt time.Time `json:"createdAt"`
}

type customerView struct {
	Account struct {
		ID        string    `json:"id"`
		Phone     string    `json:"phone"`
		CreatedAt time.Time `json:"createdAt"`
	} `json:"account"`
	// Household is the one the number owns; nil until it signs in to the
	// app (a number that only bought on the web has none).
	Household     *householdRow         `json:"household"`
	Members       []memberRow           `json:"members"`
	Subscriptions []supportSubscription `json:"subscriptions"`
	WaitingPasses []waitingPass         `json:"waitingPasses"`
	Trial         trialView             `json:"trial"`
	Payments      []paymentRow          `json:"payments"`
	Sessions      struct {
		Active  int             `json:"active"`
		Devices []sessionDevice `json:"devices"`
	} `json:"sessions"`
	Transfers struct {
		// Used is the customer's own transfers in the rolling window; Max
		// what they may do alone. Support's transfers count for neither.
		Used       int `json:"used"`
		Max        int `json:"max"`
		WindowDays int `json:"windowDays"`
	} `json:"transfers"`
	MaxExtendDays int `json:"maxExtendDays"`
}

func (m *Module) customer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	acc, ok := m.account(w, r)
	if !ok {
		return
	}
	now := m.cfg.Now()
	var out customerView
	out.Account.ID, out.Account.Phone, out.Account.CreatedAt = string(acc.ID), string(acc.Phone), acc.CreatedAt
	out.Members = []memberRow{}
	out.MaxExtendDays = licdomain.MaxExtendDays

	tenant := m.tenantOf(ctx, string(acc.ID))
	if tenant != "" {
		h, err := scanHousehold(m.cfg.Pool.QueryRow(ctx, householdSelect+` WHERE t.id = $2`, now.Add(-m.cfg.Online), tenant))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			response.Error(w, kernelerrors.WrapDBError(err))
			return
		}
		if err == nil {
			out.Household = &h
			if out.Members, err = m.members(ctx, tenant); err != nil {
				response.Error(w, kernelerrors.WrapDBError(err))
				return
			}
		}
	}

	subs, err := m.cfg.Support.Subscriptions(ctx, acc.ID)
	if err != nil {
		supportError(w, err, "")
		return
	}
	out.Subscriptions = make([]supportSubscription, 0, len(subs))
	for _, s := range subs {
		out.Subscriptions = append(out.Subscriptions, subscriptionOf(s, now))
	}

	waiting, err := m.cfg.Support.WaitingPasses(ctx, acc.ID)
	if err != nil {
		supportError(w, err, "")
		return
	}
	out.WaitingPasses = make([]waitingPass, 0, len(waiting))
	for _, p := range waiting {
		out.WaitingPasses = append(out.WaitingPasses, waitingPass{
			PaymentID: p.ID, PassCode: p.PassCode, Provider: p.Provider, AmountXOF: p.AmountXOF, CreditedAt: p.CreditedAt,
		})
	}

	tr, err := m.cfg.Support.TrialStatus(ctx, acc.ID, acc.Phone)
	if err != nil {
		supportError(w, err, "")
		return
	}
	out.Trial = trialView{State: string(tr.State), BoxID: string(tr.BoxID),
		RequestedAt: timePtr(tr.RequestedAt), StartedAt: timePtr(tr.StartedAt), EndsAt: timePtr(tr.EndsAt)}

	pays, err := m.cfg.Support.Payments(ctx, acc.ID)
	if err != nil {
		supportError(w, err, "")
		return
	}
	out.Payments = make([]paymentRow, 0, len(pays))
	for _, p := range pays {
		out.Payments = append(out.Payments, paymentRow{
			ID: p.ID, Provider: p.Provider, Reference: p.Reference, AccountID: string(p.Account), Phone: string(acc.Phone),
			BoxID: string(p.BoxID), PassCode: p.PassCode, AmountXOF: p.AmountXOF, Status: string(p.Status),
			CreatedAt: p.CreatedAt, Credited: p.CreditedAt,
		})
	}

	// The signed-in phones: what « Révoquer les sessions » would cut off.
	out.Sessions.Devices = []sessionDevice{}
	rows, err := m.cfg.Pool.Query(ctx, `SELECT device, created_at FROM hub_licensing_sessions
		WHERE account_id = $1 AND revoked_at IS NULL AND expires_at > $2 ORDER BY created_at DESC`, string(acc.ID), now)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	for rows.Next() {
		var d sessionDevice
		if err := rows.Scan(&d.Device, &d.CreatedAt); err != nil {
			rows.Close()
			response.Error(w, kernelerrors.WrapDBError(err))
			return
		}
		out.Sessions.Devices = append(out.Sessions.Devices, d)
	}
	rows.Close()
	out.Sessions.Active = len(out.Sessions.Devices)

	out.Transfers.Max, out.Transfers.WindowDays = licdomain.MaxTransfers, int(licdomain.TransferWindow/(24*time.Hour))
	if err := m.cfg.Pool.QueryRow(ctx, `SELECT count(*) FROM hub_licensing_transfers WHERE account_id = $1 AND at > $2`,
		string(acc.ID), now.Add(-licdomain.TransferWindow)).Scan(&out.Transfers.Used); err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	response.JSON(w, http.StatusOK, out)
}

// --- the gestures --------------------------------------------------------------

type extendRequest struct {
	BoxID  string `json:"boxId"`
	Days   int    `json:"days"`
	Reason string `json:"reason"`
}

// extend adds days to a box's pass: compensation for an outage.
func (m *Module) extend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	acc, ok := m.account(w, r)
	if !ok {
		return
	}
	var req extendRequest
	if !decode(w, r, &req) {
		return
	}
	reason, ok := reasonOf(w, req.Reason)
	if !ok {
		return
	}
	if req.Days < 1 || req.Days > licdomain.MaxExtendDays {
		response.Error(w, kernelerrors.BadRequest(fmt.Sprintf("days: 1 to %d", licdomain.MaxExtendDays)))
		return
	}
	box, err := licdomain.ParseBoxID(req.BoxID)
	if err != nil {
		response.Error(w, kernelerrors.BadRequest("boxId: not a box serial number"))
		return
	}
	var previous time.Time
	if subs, err := m.cfg.Support.Subscriptions(ctx, acc.ID); err == nil {
		for _, s := range subs {
			if s.BoxID == box {
				previous = s.ExpiresAt
			}
		}
	}
	sub, err := m.cfg.Support.ExtendPass(ctx, acc.ID, string(box), req.Days)
	if err != nil {
		supportError(w, err, "no such account")
		return
	}
	summary := fmt.Sprintf("Pass %s de la box %s prolongé de %d jour(s), jusqu’au %s",
		tierName(sub.Tier), box, req.Days, day(sub.ExpiresAt))
	if !previous.IsZero() {
		summary += fmt.Sprintf(" (au lieu du %s)", day(previous))
	}
	actionlog.Record(ctx, m.cfg.Audit, actionlog.Gesture{
		Kind: actionlog.KindPassExtended, TenantID: m.tenantOf(ctx, string(acc.ID)),
		TargetType: actionlog.TargetAccount, TargetID: string(acc.ID),
		Reason: reason, Summary: summary,
		Details: map[string]any{"box": string(box), "days": req.Days, "phone": string(acc.Phone),
			"from": previous.UTC().Format(time.RFC3339), "to": sub.ExpiresAt.UTC().Format(time.RFC3339)},
	})
	response.JSON(w, http.StatusOK, map[string]any{
		"boxId": sub.BoxID, "tier": sub.Tier, "expiresAt": sub.ExpiresAt, "previousExpiresAt": timePtr(previous),
	})
}

type transferRequest struct {
	FromBoxID string `json:"fromBoxId"`
	ToBoxID   string `json:"toBoxId"`
	Reason    string `json:"reason"`
}

// transfer moves a pass to another box past the customer's own limit: a
// replaced box.
func (m *Module) transfer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	acc, ok := m.account(w, r)
	if !ok {
		return
	}
	var req transferRequest
	if !decode(w, r, &req) {
		return
	}
	reason, ok := reasonOf(w, req.Reason)
	if !ok {
		return
	}
	from, err := licdomain.ParseBoxID(req.FromBoxID)
	if err != nil {
		response.Error(w, kernelerrors.BadRequest("fromBoxId: not a box serial number"))
		return
	}
	to, err := licdomain.ParseBoxID(req.ToBoxID)
	if err != nil {
		response.Error(w, kernelerrors.BadRequest("toBoxId: not a box serial number"))
		return
	}
	sub, err := m.cfg.Support.AdminTransfer(ctx, acc.ID, string(from), string(to))
	if err != nil {
		supportError(w, err, "no such account")
		return
	}
	actionlog.Record(ctx, m.cfg.Audit, actionlog.Gesture{
		Kind: actionlog.KindPassTransferred, TenantID: m.tenantOf(ctx, string(acc.ID)),
		TargetType: actionlog.TargetAccount, TargetID: string(acc.ID),
		Reason: reason,
		Summary: fmt.Sprintf("Pass %s déplacé de la box %s vers la box %s, jusqu’au %s",
			tierName(sub.Tier), from, to, day(sub.ExpiresAt)),
		Details: map[string]any{"from": string(from), "to": string(to), "phone": string(acc.Phone)},
	})
	response.JSON(w, http.StatusOK, map[string]any{"boxId": sub.BoxID, "tier": sub.Tier, "expiresAt": sub.ExpiresAt})
}

type revokeRequest struct {
	Reason string `json:"reason"`
}

// revokeSessions signs every phone of the account out of the app and the
// hub: a lost or stolen phone.
func (m *Module) revokeSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	acc, ok := m.account(w, r)
	if !ok {
		return
	}
	var req revokeRequest
	if !decode(w, r, &req) {
		return
	}
	reason, ok := reasonOf(w, req.Reason)
	if !ok {
		return
	}
	n, err := m.cfg.Support.RevokeSessions(ctx, acc.ID)
	if err != nil {
		supportError(w, err, "no such account")
		return
	}
	actionlog.Record(ctx, m.cfg.Audit, actionlog.Gesture{
		Kind: actionlog.KindSessionsRevoked, TenantID: m.tenantOf(ctx, string(acc.ID)),
		TargetType: actionlog.TargetAccount, TargetID: string(acc.ID),
		Reason:  reason,
		Summary: fmt.Sprintf("Sessions de %s révoquées (%d téléphone(s))", acc.Phone, n),
		Details: map[string]any{"sessions": n, "phone": string(acc.Phone)},
	})
	response.JSON(w, http.StatusOK, map[string]any{"sessionsRevoked": n})
}

// --- the household's history ---------------------------------------------------

type historyEvent struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Summary string    `json:"summary"`
	Detail  string    `json:"detail,omitempty"`
	Failed  bool      `json:"failed,omitempty"`
}

// historySources is every hub-side trace of a household, one row shape:
// alerts and their deliveries, its relays (paired, revoked, last
// heartbeat) and its lease, the approvals synced, the payments of the
// number that owns it, and the admin gestures about it. $1 is the
// household, $2 the owning account (” for none), $3 the admin kinds.
const historySources = `
	SELECT a.created_at AS at, 'alert' AS kind, 'alert:' || a.id::text AS key,
	  format('Alerte : %s appareil(s) inconnu(s)',
	    jsonb_array_length(CASE WHEN jsonb_typeof(a.devices) = 'array' THEN a.devices ELSE '[]'::jsonb END)) AS summary,
	  '' AS detail, false AS failed
	FROM hub_alerts a WHERE a.tenant_id = $1
	UNION ALL
	SELECT d.sent_at, 'delivery', 'delivery:' || d.alert_id::text || ':' || d.user_id::text || ':' || d.channel,
	  format('Envoi %s (%s)', d.channel, d.provider),
	  CASE WHEN d.error <> '' THEN d.error ELSE COALESCE(la.phone, '') END, d.error <> ''
	FROM hub_alert_deliveries d
	LEFT JOIN hub_identity_households ih ON ih.user_id = d.user_id
	LEFT JOIN hub_licensing_accounts la ON la.id = ih.account_id
	WHERE d.tenant_id = $1
	UNION ALL
	SELECT g.created_at, 'relay', 'relay:paired:' || g.id::text,
	  format('Relais « %s » (%s) appairé', g.name, g.kind), '', false
	FROM hub_agents g WHERE g.tenant_id = $1
	UNION ALL
	SELECT g.revoked_at, 'relay', 'relay:revoked:' || g.id::text,
	  format('Relais « %s » révoqué', g.name), '', true
	FROM hub_agents g WHERE g.tenant_id = $1 AND g.revoked_at IS NOT NULL
	UNION ALL
	SELECT p.seen_at, 'heartbeat', 'heartbeat:' || p.agent_id::text,
	  format('Dernier signal du relais « %s »', COALESCE(g.name, p.agent_id::text)),
	  trim(both ' ·' from concat_ws(' · ', NULLIF(p.version, ''), CASE WHEN p.eligible THEN 'éligible' ELSE 'non éligible' END, NULLIF(p.reason, ''))),
	  NOT p.eligible
	FROM hub_relay_presence p LEFT JOIN hub_agents g ON g.id = p.agent_id WHERE p.tenant_id = $1
	UNION ALL
	SELECT l.updated_at, 'lease', 'lease:' || l.tenant_id::text || ':' || l.epoch::text,
	  format('Bail : relais « %s », époque %s', COALESCE(g.name, 'aucun'), l.epoch),
	  'expire le ' || to_char(l.expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS') || ' UTC', l.agent_id IS NULL
	FROM hub_relay_leases l LEFT JOIN hub_agents g ON g.id = l.agent_id WHERE l.tenant_id = $1
	UNION ALL
	SELECT ap.approved_at, 'approval', 'approval:' || ap.mac,
	  format('Appareil %s approuvé', ap.mac), ap.approved_by, false
	FROM hub_inventory_approvals ap WHERE ap.tenant_id = $1
	UNION ALL
	SELECT py.created_at, 'payment', 'payment:' || py.id::text,
	  format('Paiement %s : %s, %s F', py.provider, py.pass_code, py.amount_xof),
	  py.status || CASE WHEN py.box_id IS NOT NULL AND py.box_id <> '' THEN ' · box ' || py.box_id ELSE '' END,
	  py.status IN ('failed', 'cancelled')
	FROM hub_licensing_payments py WHERE $2 <> '' AND py.account_id::text = $2
	UNION ALL
	SELECT al.occurred_at, 'admin', 'admin:' || al.id::text,
	  COALESCE(NULLIF(al.metadata->>'summary', ''), al.action::text),
	  trim(both ' ·' from concat_ws(' · ', u.email, NULLIF(al.metadata->>'reason', ''))), false
	FROM audit_logs al LEFT JOIN users u ON u.id = al.actor_id
	WHERE al.action = ANY($3::audit_action[])
	  AND (al.tenant_id = $1 OR ($2 <> '' AND al.target_id::text = $2))`

func (m *Module) history(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("tenantId")
	if _, err := uuid.Parse(id); err != nil {
		response.Error(w, kernelerrors.BadRequest("not a household id"))
		return
	}
	var exists bool
	if err := m.cfg.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tenants WHERE id = $1)`, id).Scan(&exists); err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	if !exists {
		response.Error(w, kernelerrors.NotFound("no such household"))
		return
	}
	var account string
	_ = m.cfg.Pool.QueryRow(ctx, `SELECT account_id::text FROM hub_identity_households WHERE tenant_id = $1`, id).Scan(&account)
	kinds := make([]string, len(actionlog.Kinds))
	for i, k := range actionlog.Kinds {
		kinds[i] = string(k)
	}
	limit := intParam(r, "limit", 50, 200)
	args := []any{id, account, kinds}
	where := ""
	if s := r.URL.Query().Get("before"); s != "" {
		at, key, err := decodeKeyCursor(s)
		if err != nil {
			response.Error(w, kernelerrors.BadRequest("before: not a cursor"))
			return
		}
		args = append(args, at, key)
		where = " WHERE (h.at, h.key) < ($4, $5)"
	}
	args = append(args, limit+1)
	rows, err := m.cfg.Pool.Query(ctx, `SELECT h.at, h.kind, h.key, h.summary, h.detail, h.failed FROM (`+historySources+`) h`+
		where+fmt.Sprintf(" ORDER BY h.at DESC, h.key DESC LIMIT $%d", len(args)), args...)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	defer rows.Close()
	events := []historyEvent{}
	for rows.Next() {
		var e historyEvent
		if err := rows.Scan(&e.At, &e.Kind, &e.ID, &e.Summary, &e.Detail, &e.Failed); err != nil {
			response.Error(w, kernelerrors.WrapDBError(err))
			return
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	out := map[string]any{"events": events}
	if len(events) > limit {
		events = events[:limit]
		last := events[limit-1]
		out["events"], out["next"] = events, encodeCursor(last.At, last.ID)
	}
	response.JSON(w, http.StatusOK, out)
}

// --- helpers --------------------------------------------------------------------

func (m *Module) supportOn(w http.ResponseWriter) bool {
	if m.cfg.Support == nil {
		response.Error(w, kernelerrors.ServiceUnavailable("licensing is not enabled on this hub"))
		return false
	}
	return true
}

// account reads the path's account, answering 400 or 404 itself.
func (m *Module) account(w http.ResponseWriter, r *http.Request) (licusecase.Account, bool) {
	if !m.supportOn(w) {
		return licusecase.Account{}, false
	}
	id := r.PathValue("accountId")
	if _, err := uuid.Parse(id); err != nil {
		response.Error(w, kernelerrors.BadRequest("not an account id"))
		return licusecase.Account{}, false
	}
	acc, err := m.cfg.Support.AccountByID(r.Context(), licdomain.AccountID(id))
	if err != nil {
		supportError(w, err, "no such account")
		return licusecase.Account{}, false
	}
	return acc, true
}

// tenantOf is the household an account owns, or "".
func (m *Module) tenantOf(ctx context.Context, accountID string) string {
	var tenant string
	_ = m.cfg.Pool.QueryRow(ctx, `SELECT tenant_id::text FROM hub_identity_households WHERE account_id = $1`, accountID).Scan(&tenant)
	return tenant
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(v); err != nil {
		response.Error(w, kernelerrors.BadRequest("invalid JSON body"))
		return false
	}
	return true
}

// reasonOf is the gesture's reason, required as for a pass grant.
func reasonOf(w http.ResponseWriter, raw string) (string, bool) {
	reason := strings.TrimSpace(raw)
	if len(reason) < 3 || len(reason) > 500 {
		response.Error(w, kernelerrors.BadRequest("a reason (3 to 500 characters) is required"))
		return "", false
	}
	return reason, true
}

// supportError maps the licensing module's refusals: its rules say no in
// words support can read out (400), a pass already running on the target
// box is a conflict (409).
func supportError(w http.ResponseWriter, err error, notFound string) {
	switch {
	case errors.Is(err, licusecase.ErrNotFound):
		if notFound == "" {
			notFound = "not found"
		}
		response.Error(w, kernelerrors.NotFound(notFound))
	case errors.Is(err, licusecase.ErrTargetActive), errors.Is(err, licdomain.ErrLifetimePass):
		// A lifetime pass (issue #98) has no end to extend: revoke it first.
		response.Error(w, kernelerrors.Conflict(err.Error()))
	case errors.Is(err, licdomain.ErrNoActivePass), errors.Is(err, licdomain.ErrSameBox),
		errors.Is(err, licdomain.ErrExtendDays), errors.Is(err, licdomain.ErrInvalidBoxID),
		errors.Is(err, alertsdomain.ErrInvalidPhone):
		response.Error(w, kernelerrors.BadRequest(err.Error()))
	default:
		response.Error(w, kernelerrors.InternalServerError(err))
	}
}

func tierName(t licdomain.Tier) string {
	switch t {
	case licdomain.TierBasic:
		return "Basic"
	case licdomain.TierMax:
		return "Max"
	}
	return string(t)
}

// day is a date as the log shows it; Dakar is UTC all year.
func day(t time.Time) string { return t.UTC().Format("2006-01-02") }

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
