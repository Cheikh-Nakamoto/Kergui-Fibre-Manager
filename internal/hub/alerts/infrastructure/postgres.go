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
// when several are open. Without one, the newest alert still open that was
// sent to that number is used — and only that one, so a "1" typed later
// cannot act on an alert the household has forgotten.
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
		// Fall through to the newest open alert: a household that replied
		// to the wrong bubble still means the alert it is looking at.
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
