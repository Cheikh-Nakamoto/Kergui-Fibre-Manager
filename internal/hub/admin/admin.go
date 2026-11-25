// Package admin is the hub's back-office API (issue #62): what the Kergui
// team sees and does from the web back-office, for platform administrators
// only (the kernel JWT's platform role ROLE_ADMIN).
//
// It reads across the other modules' tables, read-only, because it is a
// reporting view, not a domain: no other module depends on it. Its writes
// (granting, extending or moving a pass, revoking sessions) go through the
// licensing module's own use cases, so the subscription rules and the
// payments ledger stay in one place, and each lands in the action log.
package admin

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	kernelauth "github.com/agile-factory/kernel/kernel/auth"
	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"
	"github.com/agile-factory/kernel/platform/audit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin/actionlog"
	licdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	licusecase "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

// Migrations adds the module's audit actions (the action log's kinds).
//
//go:embed migrations/*.sql
var Migrations embed.FS

// ActionPassGranted is a pass granted from the back-office.
const ActionPassGranted = actionlog.KindPassGranted

// Granter credits a pass: the licensing module's CreditPass. Nil when the
// hub runs without licensing.
type Granter func(ctx context.Context, in licusecase.CreditInput) (licdomain.Subscription, bool, error)

// WhatsAppStatus reports the WhatsApp session, nil without whatsmeow.
type WhatsAppStatus func() any

// Config wires the module.
type Config struct {
	Pool    *pgxpool.Pool
	Version string
	Grant   Granter
	// Support is the licensing module's service, for the « Support » page
	// (issue #78). Nil when the hub runs without licensing.
	Support Support
	// Recheck asks NabooPay about one order and credits it if paid: the
	// licensing module's SettleOrder (issue #79). Nil when the hub has no
	// NabooPay.
	Recheck  Rechecker
	WhatsApp WhatsAppStatus
	Audit    audit.EventLogger
	Now      func() time.Time
	// Online is how recently a relay must have been seen to count as online.
	Online time.Duration
}

// Module serves the routes.
type Module struct{ cfg Config }

// New wires the module.
func New(cfg Config) *Module {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Online == 0 {
		cfg.Online = 2 * time.Minute
	}
	return &Module{cfg: cfg}
}

// RegisterRoutes mounts the back-office API, behind the JWT middleware.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	const p = "/api/v1/admin/"
	mux.HandleFunc("GET "+p+"me", m.guard(m.me))
	mux.HandleFunc("GET "+p+"health", m.guard(m.health))
	mux.HandleFunc("GET "+p+"households", m.guard(m.households))
	mux.HandleFunc("GET "+p+"households/{tenantId}", m.guard(m.household))
	mux.HandleFunc("GET "+p+"passes", m.guard(m.passes))
	mux.HandleFunc("POST "+p+"passes/grant", m.guard(m.grant))
	// Expiring passes and renewals (issue #97).
	mux.HandleFunc("GET "+p+"passes/expiring", m.guard(m.expiring))
	mux.HandleFunc("GET "+p+"passes/renewals", m.guard(m.renewals))
	mux.HandleFunc("GET "+p+"audit", m.guard(m.actionLog))
	// Support (issue #78).
	mux.HandleFunc("GET "+p+"support/lookup", m.guard(m.lookup))
	mux.HandleFunc("GET "+p+"support/accounts/{accountId}", m.guard(m.customer))
	mux.HandleFunc("POST "+p+"support/accounts/{accountId}/extend", m.guard(m.extend))
	mux.HandleFunc("POST "+p+"support/accounts/{accountId}/transfer", m.guard(m.transfer))
	mux.HandleFunc("POST "+p+"support/accounts/{accountId}/revoke-sessions", m.guard(m.revokeSessions))
	mux.HandleFunc("GET "+p+"households/{tenantId}/history", m.guard(m.history))
	// Payments (issue #79).
	mux.HandleFunc("GET "+p+"payments/revenue", m.guard(m.revenue))
	mux.HandleFunc("GET "+p+"payments/stuck", m.guard(m.stuck))
	mux.HandleFunc("POST "+p+"payments/{paymentId}/recheck", m.guard(m.recheck))
	mux.HandleFunc("GET "+p+"payments/export.csv", m.guard(m.exportCSV))
}

// guard lets platform administrators through, and nobody else: not a
// household's owner, whatever its role in its household.
func (m *Module) guard(next http.HandlerFunc) http.HandlerFunc {
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

func (m *Module) me(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var email string
	_ = m.cfg.Pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, kernelauth.UserIDFromContext(ctx)).Scan(&email)
	response.JSON(w, http.StatusOK, map[string]string{"userId": kernelauth.UserIDFromContext(ctx), "email": email})
}

// --- health ---------------------------------------------------------------

type healthResponse struct {
	Version  string    `json:"version"`
	Database string    `json:"database"`
	Now      time.Time `json:"now"`
	Counts   struct {
		Households      int `json:"households"`
		Relays          int `json:"relays"`
		RelaysOnline    int `json:"relaysOnline"`
		Alerts24h       int `json:"alerts24h"`
		Deliveries24h   int `json:"deliveries24h"`
		Failed24h       int `json:"failed24h"`
		PendingPayments int `json:"pendingPayments"`
		ActivePasses    int `json:"activePasses"`
	} `json:"counts"`
	RecentFailures []failure `json:"recentFailures"`
	WhatsApp       any       `json:"whatsapp,omitempty"`
}

type failure struct {
	At       time.Time `json:"at"`
	TenantID string    `json:"tenantId"`
	Channel  string    `json:"channel"`
	Provider string    `json:"provider"`
	Error    string    `json:"error"`
}

func (m *Module) health(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := m.cfg.Now().UTC()
	out := healthResponse{Version: m.cfg.Version, Database: "ok", Now: now, RecentFailures: []failure{}}
	if err := m.cfg.Pool.Ping(ctx); err != nil {
		out.Database = "down"
		response.JSON(w, http.StatusOK, out)
		return
	}
	day := now.Add(-24 * time.Hour)
	c := &out.Counts
	err := m.cfg.Pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM tenants),
		  (SELECT count(*) FROM hub_agents WHERE revoked_at IS NULL),
		  (SELECT count(*) FROM hub_relay_presence p JOIN hub_agents a ON a.id = p.agent_id
		     WHERE a.revoked_at IS NULL AND p.seen_at > $1),
		  (SELECT count(*) FROM hub_alerts WHERE created_at > $2),
		  (SELECT count(*) FROM hub_alert_deliveries WHERE sent_at > $2),
		  (SELECT count(*) FROM hub_alert_deliveries WHERE sent_at > $2 AND error <> ''),
		  (SELECT count(*) FROM hub_licensing_payments WHERE status = 'pending'),
		  (SELECT count(*) FROM hub_licensing_subscriptions WHERE tier <> 'free' AND expires_at > $3)`,
		now.Add(-m.cfg.Online), day, now,
	).Scan(&c.Households, &c.Relays, &c.RelaysOnline, &c.Alerts24h, &c.Deliveries24h, &c.Failed24h, &c.PendingPayments, &c.ActivePasses)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	rows, err := m.cfg.Pool.Query(ctx, `
		SELECT sent_at, tenant_id, channel, provider, error FROM hub_alert_deliveries
		WHERE error <> '' ORDER BY sent_at DESC LIMIT 20`)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	for rows.Next() {
		var f failure
		if err := rows.Scan(&f.At, &f.TenantID, &f.Channel, &f.Provider, &f.Error); err != nil {
			rows.Close()
			response.Error(w, kernelerrors.WrapDBError(err))
			return
		}
		out.RecentFailures = append(out.RecentFailures, f)
	}
	rows.Close()
	if m.cfg.WhatsApp != nil {
		out.WhatsApp = m.cfg.WhatsApp()
	}
	response.JSON(w, http.StatusOK, out)
}

// --- households -----------------------------------------------------------

type householdRow struct {
	TenantID     string     `json:"tenantId"`
	Name         string     `json:"name"`
	Phone        string     `json:"phone,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	Members      int        `json:"members"`
	Relays       int        `json:"relays"`
	RelaysOnline int        `json:"relaysOnline"`
	Devices      int        `json:"devices"`
	LastSnapshot *time.Time `json:"lastSnapshot,omitempty"`
	Tier         string     `json:"tier"`
	PassUntil    *time.Time `json:"passUntil,omitempty"`
}

// householdSelect is one row per household; $1 is "online since".
const householdSelect = `
	SELECT t.id, t.name, COALESCE(a.phone, ''), t.created_at,
	  (SELECT count(*) FROM memberships ms WHERE ms.tenant_id = t.id AND ms.status = 'ACTIVE'),
	  (SELECT count(*) FROM hub_agents g WHERE g.tenant_id = t.id AND g.revoked_at IS NULL),
	  (SELECT count(*) FROM hub_relay_presence p JOIN hub_agents g ON g.id = p.agent_id
	     WHERE p.tenant_id = t.id AND g.revoked_at IS NULL AND p.seen_at > $1),
	  (SELECT count(*) FROM hub_inventory_devices d WHERE d.tenant_id = t.id),
	  (SELECT max(s.as_of) FROM hub_relay_snapshots s WHERE s.tenant_id = t.id),
	  COALESCE(sub.tier, 'free'), sub.expires_at
	FROM tenants t
	LEFT JOIN hub_identity_households h ON h.tenant_id = t.id
	LEFT JOIN hub_licensing_accounts a ON a.id = h.account_id
	LEFT JOIN LATERAL (
	  SELECT tier, expires_at FROM hub_licensing_subscriptions s
	  WHERE s.account_id = h.account_id AND s.tier <> 'free' AND s.expires_at > now()
	  ORDER BY expires_at DESC LIMIT 1) sub ON true`

func scanHousehold(row pgx.Row) (householdRow, error) {
	var h householdRow
	err := row.Scan(&h.TenantID, &h.Name, &h.Phone, &h.CreatedAt, &h.Members, &h.Relays,
		&h.RelaysOnline, &h.Devices, &h.LastSnapshot, &h.Tier, &h.PassUntil)
	return h, err
}

// households lists the households, newest first; q searches the owner's
// phone number (digits), the household's name or its id.
func (m *Module) households(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := intParam(r, "limit", 50, 200)
	args := []any{m.cfg.Now().Add(-m.cfg.Online)}
	where := ""
	if q != "" {
		args = append(args, "%"+strings.ToLower(q)+"%", "%"+digits(q)+"%")
		where = ` WHERE lower(t.name) LIKE $2 OR t.id::text LIKE $2` +
			` OR ($3 <> '%%' AND regexp_replace(COALESCE(a.phone, ''), '\D', '', 'g') LIKE $3)`
	}
	args = append(args, limit)
	rows, err := m.cfg.Pool.Query(ctx, householdSelect+where+
		fmt.Sprintf(" ORDER BY t.created_at DESC LIMIT $%d", len(args)), args...)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	defer rows.Close()
	out := []householdRow{}
	for rows.Next() {
		h, err := scanHousehold(rows)
		if err != nil {
			response.Error(w, kernelerrors.WrapDBError(err))
			return
		}
		out = append(out, h)
	}
	response.JSON(w, http.StatusOK, map[string]any{"households": out})
}

type memberRow struct {
	UserID   string     `json:"userId"`
	Email    string     `json:"email"`
	Role     string     `json:"role"`
	Status   string     `json:"status"`
	WhatsApp string     `json:"whatsapp,omitempty"`
	JoinedAt *time.Time `json:"joinedAt,omitempty"`
}

type relayRow struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Kind     string     `json:"kind"`
	Version  string     `json:"version,omitempty"`
	SeenAt   *time.Time `json:"seenAt,omitempty"`
	Online   bool       `json:"online"`
	Revoked  bool       `json:"revoked"`
	Eligible bool       `json:"eligible"`
	Reason   string     `json:"reason,omitempty"`
}

type alertRow struct {
	At      time.Time       `json:"at"`
	Devices json.RawMessage `json:"devices"`
}

func (m *Module) household(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("tenantId")
	if _, err := uuid.Parse(id); err != nil {
		response.Error(w, kernelerrors.BadRequest("not a household id"))
		return
	}
	online := m.cfg.Now().Add(-m.cfg.Online)
	h, err := scanHousehold(m.cfg.Pool.QueryRow(ctx, householdSelect+` WHERE t.id = $2`, online, id))
	if errors.Is(err, pgx.ErrNoRows) {
		response.Error(w, kernelerrors.NotFound("no such household"))
		return
	}
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}

	members, err := m.members(ctx, id)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}

	relays := []relayRow{}
	rows, err := m.cfg.Pool.Query(ctx, `
		SELECT g.id, g.name, g.kind, COALESCE(p.version, ''), p.seen_at, g.revoked_at IS NOT NULL,
		       COALESCE(p.eligible, false), COALESCE(p.reason, '')
		FROM hub_agents g LEFT JOIN hub_relay_presence p ON p.agent_id = g.id
		WHERE g.tenant_id = $1 ORDER BY g.created_at`, id)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	for rows.Next() {
		var rr relayRow
		if err := rows.Scan(&rr.ID, &rr.Name, &rr.Kind, &rr.Version, &rr.SeenAt, &rr.Revoked, &rr.Eligible, &rr.Reason); err != nil {
			rows.Close()
			response.Error(w, kernelerrors.WrapDBError(err))
			return
		}
		rr.Online = !rr.Revoked && rr.SeenAt != nil && rr.SeenAt.After(online)
		relays = append(relays, rr)
	}
	rows.Close()

	alerts := []alertRow{}
	rows, err = m.cfg.Pool.Query(ctx, `SELECT created_at, devices FROM hub_alerts WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT 10`, id)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	for rows.Next() {
		var a alertRow
		if err := rows.Scan(&a.At, &a.Devices); err != nil {
			rows.Close()
			response.Error(w, kernelerrors.WrapDBError(err))
			return
		}
		alerts = append(alerts, a)
	}
	rows.Close()

	response.JSON(w, http.StatusOK, map[string]any{
		"household": h, "members": members, "relays": relays, "alerts": alerts,
	})
}

// members lists a household's members, its owner first.
func (m *Module) members(ctx context.Context, tenantID string) ([]memberRow, error) {
	rows, err := m.cfg.Pool.Query(ctx, `
		SELECT u.id, u.email, ms.role::text, ms.status::text, COALESCE(o.phone, ''), ms.joined_at
		FROM memberships ms JOIN users u ON u.id = ms.user_id
		LEFT JOIN hub_whatsapp_optins o ON o.tenant_id = ms.tenant_id AND o.user_id = ms.user_id AND o.stopped_at IS NULL
		WHERE ms.tenant_id = $1 ORDER BY ms.created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []memberRow{}
	for rows.Next() {
		var mr memberRow
		if err := rows.Scan(&mr.UserID, &mr.Email, &mr.Role, &mr.Status, &mr.WhatsApp, &mr.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, mr)
	}
	return out, rows.Err()
}

// --- passes ---------------------------------------------------------------

type accountRow struct {
	AccountID     string         `json:"accountId"`
	Phone         string         `json:"phone"`
	TenantID      string         `json:"tenantId,omitempty"`
	CreatedAt     time.Time      `json:"createdAt"`
	Subscriptions []subscription `json:"subscriptions"`
	Trial         *trial         `json:"trial,omitempty"`
}

type subscription struct {
	BoxID     string    `json:"boxId"`
	Tier      string    `json:"tier"`
	ExpiresAt time.Time `json:"expiresAt"`
	Active    bool      `json:"active"`
}

// trial is the account's 7-day Max trial. One asked for on the web waits
// for the app to bind it to a box: no box and no start yet.
type trial struct {
	BoxID       string     `json:"boxId,omitempty"`
	RequestedAt time.Time  `json:"requestedAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
}

type paymentRow struct {
	ID        string     `json:"id"`
	Provider  string     `json:"provider"`
	Reference string     `json:"reference"`
	AccountID string     `json:"accountId"`
	Phone     string     `json:"phone"`
	BoxID     string     `json:"boxId,omitempty"`
	PassCode  string     `json:"passCode"`
	AmountXOF int64      `json:"amountXof"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"createdAt"`
	Credited  *time.Time `json:"creditedAt,omitempty"`
}

// passes lists the accounts (phone numbers) with their passes and trial,
// and the latest payments; q searches the phone number.
func (m *Module) passes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := m.cfg.Now()
	q := digits(r.URL.Query().Get("q"))
	limit := intParam(r, "limit", 50, 200)
	where, args := "", []any{limit}
	if q != "" {
		where, args = ` WHERE regexp_replace(a.phone, '\D', '', 'g') LIKE $2`, append(args, "%"+q+"%")
	}
	rows, err := m.cfg.Pool.Query(ctx, `
		SELECT a.id, a.phone, COALESCE(h.tenant_id::text, ''), a.created_at
		FROM hub_licensing_accounts a LEFT JOIN hub_identity_households h ON h.account_id = a.id`+where+`
		ORDER BY a.created_at DESC LIMIT $1`, args...)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	accounts := []accountRow{}
	index := map[string]int{}
	ids := []string{}
	for rows.Next() {
		var a accountRow
		if err := rows.Scan(&a.AccountID, &a.Phone, &a.TenantID, &a.CreatedAt); err != nil {
			rows.Close()
			response.Error(w, kernelerrors.WrapDBError(err))
			return
		}
		a.Subscriptions = []subscription{}
		index[a.AccountID] = len(accounts)
		ids = append(ids, a.AccountID)
		accounts = append(accounts, a)
	}
	rows.Close()

	if len(ids) > 0 {
		rows, err = m.cfg.Pool.Query(ctx, `SELECT account_id, box_id, tier, expires_at FROM hub_licensing_subscriptions
			WHERE account_id = ANY($1::uuid[]) ORDER BY expires_at DESC`, ids)
		if err != nil {
			response.Error(w, kernelerrors.WrapDBError(err))
			return
		}
		for rows.Next() {
			var acc string
			var s subscription
			if err := rows.Scan(&acc, &s.BoxID, &s.Tier, &s.ExpiresAt); err != nil {
				rows.Close()
				response.Error(w, kernelerrors.WrapDBError(err))
				return
			}
			s.Active = s.Tier != "free" && s.ExpiresAt.After(now)
			accounts[index[acc]].Subscriptions = append(accounts[index[acc]].Subscriptions, s)
		}
		rows.Close()
		rows, err = m.cfg.Pool.Query(ctx, `SELECT account_id, COALESCE(box_id, ''), requested_at, started_at FROM hub_licensing_trials WHERE account_id = ANY($1::uuid[])`, ids)
		if err != nil {
			response.Error(w, kernelerrors.WrapDBError(err))
			return
		}
		for rows.Next() {
			var acc string
			var t trial
			if err := rows.Scan(&acc, &t.BoxID, &t.RequestedAt, &t.StartedAt); err != nil {
				rows.Close()
				response.Error(w, kernelerrors.WrapDBError(err))
				return
			}
			accounts[index[acc]].Trial = &t
		}
		rows.Close()
	}

	pwhere, pargs := "", []any{}
	if q != "" {
		pwhere, pargs = ` WHERE regexp_replace(a.phone, '\D', '', 'g') LIKE $1`, []any{"%" + q + "%"}
	}
	rows, err = m.cfg.Pool.Query(ctx, `
		SELECT p.id, p.provider, p.provider_ref, p.account_id, a.phone, COALESCE(p.box_id, ''), p.pass_code,
		       p.amount_xof, p.status, p.created_at, p.credited_at
		FROM hub_licensing_payments p JOIN hub_licensing_accounts a ON a.id = p.account_id`+pwhere+`
		ORDER BY p.created_at DESC LIMIT 50`, pargs...)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	defer rows.Close()
	payments := []paymentRow{}
	for rows.Next() {
		var p paymentRow
		if err := rows.Scan(&p.ID, &p.Provider, &p.Reference, &p.AccountID, &p.Phone, &p.BoxID, &p.PassCode,
			&p.AmountXOF, &p.Status, &p.CreatedAt, &p.Credited); err != nil {
			response.Error(w, kernelerrors.WrapDBError(err))
			return
		}
		payments = append(payments, p)
	}
	response.JSON(w, http.StatusOK, map[string]any{
		"accounts": accounts, "payments": payments, "catalogue": catalogue(),
	})
}

func catalogue() []map[string]any {
	out := []map[string]any{}
	for _, code := range []string{"basic_1m", "basic_3m", "basic_12m", "max_1m", "max_3m", "max_12m"} {
		if p, err := licdomain.PassByCode(code); err == nil {
			out = append(out, map[string]any{"code": p.Code, "tier": p.Tier, "days": p.Days, "priceXof": p.PriceXOF})
		}
	}
	return out
}

type grantRequest struct {
	AccountID string `json:"accountId"`
	BoxID     string `json:"boxId"`
	PassCode  string `json:"passCode"`
	Reason    string `json:"reason"`
}

// grant gives an account a pass by hand: a support gesture, recorded at 0 F
// as a "support" payment and audited with who did it and why.
func (m *Module) grant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if m.cfg.Grant == nil {
		response.Error(w, kernelerrors.New("SERVICE_UNAVAILABLE", "licensing is not enabled on this hub", http.StatusServiceUnavailable, nil))
		return
	}
	var req grantRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		response.Error(w, kernelerrors.BadRequest("invalid JSON body"))
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if _, err := uuid.Parse(req.AccountID); err != nil {
		response.Error(w, kernelerrors.BadRequest("accountId is required"))
		return
	}
	if _, err := licdomain.PassByCode(req.PassCode); err != nil {
		response.Error(w, kernelerrors.BadRequest("unknown pass"))
		return
	}
	if len(req.Reason) < 3 || len(req.Reason) > 500 {
		response.Error(w, kernelerrors.BadRequest("a reason (3 to 500 characters) is required"))
		return
	}
	ref := uuid.NewString()
	sub, _, err := m.cfg.Grant(ctx, licusecase.CreditInput{
		Account: licdomain.AccountID(req.AccountID), BoxID: licdomain.BoxID(strings.TrimSpace(req.BoxID)),
		PassCode: req.PassCode, Provider: licusecase.ProviderSupport, Reference: ref,
	})
	if err != nil {
		if errors.Is(err, licusecase.ErrNotFound) {
			response.Error(w, kernelerrors.NotFound("no such account"))
			return
		}
		response.Error(w, kernelerrors.BadRequest(err.Error()))
		return
	}
	var tenant string
	_ = m.cfg.Pool.QueryRow(ctx, `SELECT tenant_id::text FROM hub_identity_households WHERE account_id = $1`, req.AccountID).Scan(&tenant)
	var phone string
	_ = m.cfg.Pool.QueryRow(ctx, `SELECT phone FROM hub_licensing_accounts WHERE id = $1`, req.AccountID).Scan(&phone)
	summary := fmt.Sprintf("Pass %s offert à %s", req.PassCode, phone)
	if sub.BoxID != "" {
		summary += fmt.Sprintf(" (box %s)", sub.BoxID)
	}
	summary += fmt.Sprintf(", jusqu’au %s", sub.ExpiresAt.UTC().Format("2006-01-02"))
	actionlog.Record(ctx, m.cfg.Audit, actionlog.Gesture{
		Kind: actionlog.KindPassGranted, TenantID: tenant,
		TargetType: actionlog.TargetAccount, TargetID: req.AccountID,
		Reason: req.Reason, Summary: summary,
		Details: map[string]any{"pass": req.PassCode, "box": string(sub.BoxID), "reference": ref},
	})
	response.JSON(w, http.StatusOK, map[string]any{
		"reference": ref, "tier": sub.Tier, "expiresAt": sub.ExpiresAt, "boxId": sub.BoxID,
	})
}

// --- helpers --------------------------------------------------------------

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func intParam(r *http.Request, name string, def, max int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
