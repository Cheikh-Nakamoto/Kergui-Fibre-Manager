package sqlite

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// PaymentRepo persists payment requests.
type PaymentRepo struct{ db *sql.DB }

var _ port.PaymentRepository = (*PaymentRepo)(nil)

// NewPaymentRepo wires the repository.
func NewPaymentRepo(db *DB) *PaymentRepo { return &PaymentRepo{db: db.SQL()} }

const paymentCols = `id, router_id, mac, period, bytes, amount_xof, customer_name, phone,
order_id, checkout_url, status, created_at, updated_at, paid_at, member_id, kind, due_at`

// Create inserts a payment and its items.
func (r *PaymentRepo) Create(ctx context.Context, p domain.Payment) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if p.Kind == "" {
		p.Kind = domain.PaymentTest
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO payments (`+paymentCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.RouterID, p.MAC.String(), p.Period, p.Bytes, p.AmountXOF, p.CustomerName, p.Phone,
		p.OrderID, p.CheckoutURL, string(p.Status), unixOrZero(p.CreatedAt), unixOrZero(p.UpdatedAt), unixOrZero(p.PaidAt),
		p.MemberID, string(p.Kind), unixOrZero(p.DueAt)); err != nil {
		return err
	}
	items := p.Items
	if len(items) == 0 && !p.MAC.IsZero() {
		items = []domain.PaymentItem{{MAC: p.MAC, Bytes: p.Bytes}}
	}
	for _, it := range items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO payment_items (payment_id, mac, bytes) VALUES (?,?,?)`,
			p.ID, it.MAC.String(), it.Bytes); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Update rewrites the mutable fields of a payment.
func (r *PaymentRepo) Update(ctx context.Context, p domain.Payment) error {
	res, err := r.db.ExecContext(ctx, `
UPDATE payments SET order_id=?, checkout_url=?, status=?, amount_xof=?, updated_at=?, paid_at=? WHERE id=?`,
		p.OrderID, p.CheckoutURL, string(p.Status), p.AmountXOF, unixOrZero(p.UpdatedAt), unixOrZero(p.PaidAt), p.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrPaymentNotFound
	}
	return nil
}

// Get returns one payment with its items.
func (r *PaymentRepo) Get(ctx context.Context, id string) (domain.Payment, bool, error) {
	ps, err := r.list(ctx, `WHERE id=?`, id)
	if err != nil || len(ps) == 0 {
		return domain.Payment{}, false, err
	}
	return ps[0], true, nil
}

// ListByMAC returns the payments covering a device, newest first.
func (r *PaymentRepo) ListByMAC(ctx context.Context, routerID string, mac domain.MAC, limit int) ([]domain.Payment, error) {
	return r.list(ctx, `WHERE router_id=? AND id IN (SELECT payment_id FROM payment_items WHERE mac=?)
ORDER BY created_at DESC, id LIMIT ?`, routerID, mac.String(), limit)
}

// ListByStatus returns payments in a status, oldest first.
func (r *PaymentRepo) ListByStatus(ctx context.Context, status domain.PaymentStatus, limit int) ([]domain.Payment, error) {
	return r.list(ctx, `WHERE status=? ORDER BY created_at, id LIMIT ?`, string(status), limit)
}

// ListByPeriod returns the payments of a kind for one period, newest first.
func (r *PaymentRepo) ListByPeriod(ctx context.Context, routerID, period string, kind domain.PaymentKind) ([]domain.Payment, error) {
	return r.list(ctx, `WHERE router_id=? AND period=? AND kind=? ORDER BY created_at DESC, id`, routerID, period, string(kind))
}

// PaidBytes sums a device's consumption covered by paid payments.
func (r *PaymentRepo) PaidBytes(ctx context.Context, routerID string, mac domain.MAC, period string) (uint64, error) {
	var n uint64
	err := r.db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(i.bytes), 0) FROM payment_items i JOIN payments p ON p.id = i.payment_id
WHERE p.router_id=? AND i.mac=? AND p.period=? AND p.status=?`,
		routerID, mac.String(), period, string(domain.PaymentPaid)).Scan(&n)
	return n, err
}

func (r *PaymentRepo) list(ctx context.Context, tail string, args ...any) ([]domain.Payment, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+paymentCols+` FROM payments `+tail, args...)
	if err != nil {
		return nil, err
	}
	out := []domain.Payment{}
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Items, err = r.items(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *PaymentRepo) items(ctx context.Context, paymentID string) ([]domain.PaymentItem, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT mac, bytes FROM payment_items WHERE payment_id=? ORDER BY mac`, paymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.PaymentItem{}
	for rows.Next() {
		var mac string
		var it domain.PaymentItem
		if err := rows.Scan(&mac, &it.Bytes); err != nil {
			return nil, err
		}
		if it.MAC, err = domain.ParseMAC(mac); err != nil {
			continue
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func scanPayment(s scanner) (domain.Payment, error) {
	var (
		p                           domain.Payment
		mac, status, kind           string
		created, updated, paid, due int64
	)
	if err := s.Scan(&p.ID, &p.RouterID, &mac, &p.Period, &p.Bytes, &p.AmountXOF, &p.CustomerName, &p.Phone,
		&p.OrderID, &p.CheckoutURL, &status, &created, &updated, &paid, &p.MemberID, &kind, &due); err != nil {
		return domain.Payment{}, err
	}
	if mac != "" {
		m, err := domain.ParseMAC(mac)
		if err != nil {
			return domain.Payment{}, err
		}
		p.MAC = m
	}
	p.Status, p.Kind = domain.PaymentStatus(status), domain.PaymentKind(kind)
	p.CreatedAt, p.UpdatedAt, p.PaidAt, p.DueAt = timeOrZero(created), timeOrZero(updated), timeOrZero(paid), timeOrZero(due)
	return p, nil
}

// SettingsRepo stores admin settings as key/value rows.
type SettingsRepo struct{ db *sql.DB }

var _ port.SettingsRepository = (*SettingsRepo)(nil)

// NewSettingsRepo wires the repository.
func NewSettingsRepo(db *DB) *SettingsRepo { return &SettingsRepo{db: db.SQL()} }

// BillingSettings returns the stored settings, defaulting missing keys.
func (r *SettingsRepo) BillingSettings(ctx context.Context) (domain.BillingSettings, error) {
	s := domain.DefaultBillingSettings
	rows, err := r.db.QueryContext(ctx, `SELECT key, value FROM settings WHERE key LIKE 'billing.%'`)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return s, err
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			continue
		}
		switch k {
		case "billing.price_per_gb":
			s.PricePerGB = n
		case "billing.min_amount":
			s.MinAmount = n
		case "billing.due_days":
			s.DueDays = n
		case "billing.grace_days":
			s.GraceDays = n
		case "billing.enforce_cuts":
			s.EnforceCuts = n == 1
		case "billing.cap_gb":
			s.CapGB = n
		}
	}
	return s, rows.Err()
}

// SaveBillingSettings stores the settings.
func (r *SettingsRepo) SaveBillingSettings(ctx context.Context, s domain.BillingSettings) error {
	enforce := int64(0)
	if s.EnforceCuts {
		enforce = 1
	}
	for k, v := range map[string]int64{
		"billing.price_per_gb": s.PricePerGB, "billing.min_amount": s.MinAmount,
		"billing.due_days": s.DueDays, "billing.grace_days": s.GraceDays, "billing.enforce_cuts": enforce,
		"billing.cap_gb": s.CapGB,
	} {
		if _, err := r.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, strconv.FormatInt(v, 10)); err != nil {
			return err
		}
	}
	return nil
}
