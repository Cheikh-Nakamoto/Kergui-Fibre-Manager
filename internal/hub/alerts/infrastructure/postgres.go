// Package infrastructure implements the alerts module's repository on
// Postgres. Queries go through txctx.DB so they join the caller's
// transaction, as the kernel's own repositories do.
package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agile-factory/kernel/kernel/txctx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

// Repo is the alerts module's storage.
type Repo struct{ pool *pgxpool.Pool }

var _ domain.Repository = (*Repo)(nil)

// New wires the repository.
func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) db(ctx context.Context) txctx.DBTX { return txctx.DB(ctx, r.pool) }

// --- opt-in ---------------------------------------------------------------

// PutPending records the code the app is showing, replacing whatever that
// member had before.
func (r *Repo) PutPending(ctx context.Context, p domain.PendingOptIn) error {
	_, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_whatsapp_pending (tenant_id, user_id, code_hash, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (tenant_id, user_id) DO UPDATE SET
		   code_hash = EXCLUDED.code_hash, expires_at = EXCLUDED.expires_at,
		   created_at = EXCLUDED.created_at`,
		p.TenantID, p.UserID, p.CodeHash, p.ExpiresAt, p.CreatedAt)
	if err != nil {
		return fmt.Errorf("alerts: pending opt-in: %w", err)
	}
	return nil
}

// ClaimPending spends a code: the DELETE is the claim, so two messages
// carrying the same code cannot both succeed.
func (r *Repo) ClaimPending(ctx context.Context, codeHash []byte, now time.Time) (domain.PendingOptIn, error) {
	var p domain.PendingOptIn
	err := r.db(ctx).QueryRow(ctx,
		`DELETE FROM hub_whatsapp_pending
		 WHERE code_hash = $1 AND expires_at > $2
		 RETURNING tenant_id, user_id, expires_at, created_at`,
		codeHash, now).Scan(&p.TenantID, &p.UserID, &p.ExpiresAt, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PendingOptIn{}, domain.ErrNoOptIn
	}
	if err != nil {
		return domain.PendingOptIn{}, fmt.Errorf("alerts: claim opt-in: %w", err)
	}
	p.CodeHash = codeHash
	return p, nil
}

// SaveOptIn records a verified number. A member who re-enrols with another
// number replaces their own row and is no longer stopped.
func (r *Repo) SaveOptIn(ctx context.Context, o domain.OptIn) error {
	_, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_whatsapp_optins (tenant_id, user_id, phone, locale, created_at, stopped_at)
		 VALUES ($1, $2, $3, $4, $5, NULL)
		 ON CONFLICT (tenant_id, user_id) DO UPDATE SET
		   phone = EXCLUDED.phone, locale = EXCLUDED.locale,
		   created_at = EXCLUDED.created_at, stopped_at = NULL`,
		o.TenantID, o.UserID, string(o.Phone), o.Locale, o.CreatedAt)
	if err != nil {
		return fmt.Errorf("alerts: save opt-in: %w", err)
	}
	return nil
}

const optInColumns = `tenant_id, user_id, phone, locale, created_at, stopped_at`

// ActiveOptIns lists a household's numbers that still want alerts.
func (r *Repo) ActiveOptIns(ctx context.Context, tenantID string) ([]domain.OptIn, error) {
	return r.optIns(ctx,
		`SELECT `+optInColumns+` FROM hub_whatsapp_optins
		 WHERE tenant_id = $1 AND stopped_at IS NULL ORDER BY created_at`, tenantID)
}

// OptInsByPhone lists the households a number serves.
func (r *Repo) OptInsByPhone(ctx context.Context, phone domain.Phone) ([]domain.OptIn, error) {
	return r.optIns(ctx,
		`SELECT `+optInColumns+` FROM hub_whatsapp_optins
		 WHERE phone = $1 AND stopped_at IS NULL ORDER BY created_at`, string(phone))
}

func (r *Repo) optIns(ctx context.Context, query string, args ...any) ([]domain.OptIn, error) {
	rows, err := r.db(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("alerts: opt-ins: %w", err)
	}
	defer rows.Close()
	var out []domain.OptIn
	for rows.Next() {
		var (
			o     domain.OptIn
			phone string
		)
		if err := rows.Scan(&o.TenantID, &o.UserID, &phone, &o.Locale, &o.CreatedAt, &o.StoppedAt); err != nil {
			return nil, fmt.Errorf("alerts: opt-ins scan: %w", err)
		}
		o.Phone = domain.Phone(phone)
		out = append(out, o)
	}
	return out, rows.Err()
}

// StopPhone ends the alerts for a number everywhere it is registered. A
// person who asks to be left alone is not asked which household they meant.
func (r *Repo) StopPhone(ctx context.Context, phone domain.Phone, now time.Time) error {
	_, err := r.db(ctx).Exec(ctx,
		`UPDATE hub_whatsapp_optins SET stopped_at = $2 WHERE phone = $1 AND stopped_at IS NULL`,
		string(phone), now)
	if err != nil {
		return fmt.Errorf("alerts: stop: %w", err)
	}
	return nil
}

// --- alerts ---------------------------------------------------------------

// CreateAlert stores an alert once per inventory window.
func (r *Repo) CreateAlert(ctx context.Context, a domain.Alert) (bool, error) {
	devices, err := json.Marshal(a.Devices)
	if err != nil {
		return false, fmt.Errorf("alerts: encode devices: %w", err)
	}
	id := a.ID
	if id == "" {
		id = uuid.NewString()
	}
	tag, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_alerts (id, tenant_id, window_id, devices, created_at, reply_until)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (window_id) DO NOTHING`,
		id, a.TenantID, a.WindowID, devices, a.CreatedAt, a.ReplyUntil)
	if err != nil {
		return false, fmt.Errorf("alerts: create alert: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const alertColumns = `id, tenant_id, window_id, devices, created_at, reply_until`

// AlertByWindow returns the alert of an inventory window.
func (r *Repo) AlertByWindow(ctx context.Context, tenantID, windowID string) (domain.Alert, error) {
	return r.alert(ctx,
		`SELECT `+alertColumns+` FROM hub_alerts WHERE tenant_id = $1 AND window_id = $2`,
		tenantID, windowID)
}

// AlertForReply resolves the alert a reply answers.
//
// A quoted message is the reliable answer: it names the exact alert, even
// when several are open. Without one, the alert still open that was sent to
// that number is used — and only when there is one: with several, the
// newest may not be the one the person is reading, so ErrAmbiguousAlert
// lets the hub ask which (audit C2). A "1" typed later cannot act on an
// alert the household has forgotten either way.
func (r *Repo) AlertForReply(ctx context.Context, phone domain.Phone, quotedProviderID string, now time.Time) (domain.Alert, error) {
	if quotedProviderID != "" {
		a, err := r.alert(ctx,
			`SELECT `+prefixed(alertColumns, "a")+`
			 FROM hub_alerts a
			 JOIN hub_alert_deliveries d ON d.alert_id = a.id
			 JOIN hub_whatsapp_optins o ON o.tenant_id = a.tenant_id AND o.user_id = d.user_id
			 WHERE d.provider_msg_id = $1 AND o.phone = $2 AND a.reply_until > $3
			 LIMIT 1`,
			quotedProviderID, string(phone), now)
		if err == nil {
			return a, nil
		}
		if !errors.Is(err, domain.ErrNoAlert) {
			return domain.Alert{}, err
		}
		// The quoted message is not one of ours, or its alert has expired.
		// Fall through to the open alerts: a household that replied to the
		// wrong bubble still means the alert it is looking at, when there
		// is only one.
	}
	var open int
	if err := r.db(ctx).QueryRow(ctx,
		`SELECT count(DISTINCT a.id)
		 FROM hub_alerts a
		 JOIN hub_alert_deliveries d ON d.alert_id = a.id AND d.channel = 'whatsapp'
		 JOIN hub_whatsapp_optins o ON o.tenant_id = a.tenant_id AND o.user_id = d.user_id
		 WHERE o.phone = $1 AND o.stopped_at IS NULL AND a.reply_until > $2`,
		string(phone), now).Scan(&open); err != nil {
		return domain.Alert{}, fmt.Errorf("alerts: count open alerts: %w", err)
	}
	switch {
	case open == 0:
		return domain.Alert{}, domain.ErrNoAlert
	case open > 1:
		return domain.Alert{}, domain.ErrAmbiguousAlert
	}
	return r.alert(ctx,
		`SELECT `+prefixed(alertColumns, "a")+`
		 FROM hub_alerts a
		 JOIN hub_alert_deliveries d ON d.alert_id = a.id AND d.channel = 'whatsapp'
		 JOIN hub_whatsapp_optins o ON o.tenant_id = a.tenant_id AND o.user_id = d.user_id
		 WHERE o.phone = $1 AND o.stopped_at IS NULL AND a.reply_until > $2
		 ORDER BY a.created_at DESC
		 LIMIT 1`,
		string(phone), now)
}

func (r *Repo) alert(ctx context.Context, query string, args ...any) (domain.Alert, error) {
	var (
		a       domain.Alert
		devices []byte
	)
	err := r.db(ctx).QueryRow(ctx, query, args...).
		Scan(&a.ID, &a.TenantID, &a.WindowID, &devices, &a.CreatedAt, &a.ReplyUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Alert{}, domain.ErrNoAlert
	}
	if err != nil {
		return domain.Alert{}, fmt.Errorf("alerts: read alert: %w", err)
	}
	if err := json.Unmarshal(devices, &a.Devices); err != nil {
		return domain.Alert{}, fmt.Errorf("alerts: decode devices: %w", err)
	}
	return a, nil
}

// RecordDelivery stores what was sent, or why it was not.
func (r *Repo) RecordDelivery(ctx context.Context, d domain.Delivery) error {
	_, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_alert_deliveries
		   (alert_id, tenant_id, user_id, channel, provider, provider_msg_id, sent_at, error)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (alert_id, user_id, channel) DO UPDATE SET
		   provider = EXCLUDED.provider, provider_msg_id = EXCLUDED.provider_msg_id,
		   sent_at = EXCLUDED.sent_at, error = EXCLUDED.error`,
		d.AlertID, d.TenantID, d.UserID, d.Channel, d.Provider, d.ProviderMsgID, d.SentAt, d.Err)
	if err != nil {
		return fmt.Errorf("alerts: record delivery: %w", err)
	}
	return nil
}

// prefixed qualifies a column list with a table alias, so one list serves
// both the plain read and the joined ones.
func prefixed(columns, alias string) string {
	parts := strings.Split(columns, ",")
	for i, c := range parts {
		parts[i] = alias + "." + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

// StopMember ends a member's alerts for one household: they were removed
// from it (issue #42), and what is on its network is no longer theirs to
// be told.
func (r *Repo) StopMember(ctx context.Context, tenantID, userID string, now time.Time) error {
	_, err := r.db(ctx).Exec(ctx,
		`UPDATE hub_whatsapp_optins SET stopped_at = $3 WHERE tenant_id = $1 AND user_id = $2 AND stopped_at IS NULL`,
		tenantID, userID, now)
	if err != nil {
		return fmt.Errorf("alerts: stop member: %w", err)
	}
	// Their phone's app stops hearing about this household too.
	if _, err := r.db(ctx).Exec(ctx,
		`DELETE FROM hub_push_tokens WHERE tenant_id = $1 AND user_id = $2`, tenantID, userID); err != nil {
		return fmt.Errorf("alerts: stop member's push: %w", err)
	}
	return nil
}

// --- push tokens (issue #49) ---------------------------------------------

var _ domain.PushTargets = (*Repo)(nil)

// For lists a household's installations.
func (r *Repo) For(ctx context.Context, tenantID string) ([]domain.PushTarget, error) {
	rows, err := r.db(ctx).Query(ctx,
		`SELECT tenant_id, user_id, platform, token FROM hub_push_tokens
		 WHERE tenant_id = $1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("alerts: push targets: %w", err)
	}
	defer rows.Close()
	var out []domain.PushTarget
	for rows.Next() {
		var t domain.PushTarget
		if err := rows.Scan(&t.TenantID, &t.UserID, &t.Platform, &t.Token); err != nil {
			return nil, fmt.Errorf("alerts: push targets: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Register stores an installation's token for a member. The same token
// registered again — the same phone, or the same phone now signed in as
// another member of the household — belongs to whoever registered it last.
func (r *Repo) Register(ctx context.Context, t domain.PushTarget, now time.Time) error {
	_, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_push_tokens (tenant_id, user_id, token, platform, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $5)
		 ON CONFLICT (tenant_id, token) DO UPDATE SET
		   user_id = EXCLUDED.user_id, platform = EXCLUDED.platform, updated_at = EXCLUDED.updated_at`,
		t.TenantID, t.UserID, t.Token, t.Platform, now)
	if err != nil {
		return fmt.Errorf("alerts: register push token: %w", err)
	}
	return nil
}

// Unregister drops a member's token for a household.
func (r *Repo) Unregister(ctx context.Context, tenantID, userID, token string) error {
	_, err := r.db(ctx).Exec(ctx,
		`DELETE FROM hub_push_tokens WHERE tenant_id = $1 AND user_id = $2 AND token = $3`,
		tenantID, userID, token)
	if err != nil {
		return fmt.Errorf("alerts: unregister push token: %w", err)
	}
	return nil
}

// Forget drops a token everywhere.
func (r *Repo) Forget(ctx context.Context, token string) error {
	if _, err := r.db(ctx).Exec(ctx, `DELETE FROM hub_push_tokens WHERE token = $1`, token); err != nil {
		return fmt.Errorf("alerts: forget push token: %w", err)
	}
	return nil
}
