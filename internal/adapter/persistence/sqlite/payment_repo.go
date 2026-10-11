package sqlite

import (
	"context"
	"database/sql"
	"errors"
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
order_id, checkout_url, status, created_at, updated_at, paid_at`

// Create inserts a payment.
func (r *PaymentRepo) Create(ctx context.Context, p domain.Payment) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO payments (`+paymentCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.RouterID, p.MAC.String(), p.Period, p.Bytes, p.AmountXOF, p.CustomerName, p.Phone,
		p.OrderID, p.CheckoutURL, string(p.Status), unixOrZero(p.CreatedAt), unixOrZero(p.UpdatedAt), unixOrZero(p.PaidAt))
	return err
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

// Get returns one payment.
func (r *PaymentRepo) Get(ctx context.Context, id string) (domain.Payment, bool, error) {
	p, err := scanPayment(r.db.QueryRowContext(ctx, `SELECT `+paymentCols+` FROM payments WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Payment{}, false, nil
	}
	return p, err == nil, err
}

// ListByMAC returns a device's payments, newest first.
func (r *PaymentRepo) ListByMAC(ctx context.Context, routerID string, mac domain.MAC, limit int) ([]domain.Payment, error) {
	return r.list(ctx, `WHERE router_id=? AND mac=? ORDER BY created_at DESC, id LIMIT ?`, routerID, mac.String(), limit)
}

// ListByStatus returns payments in a status, oldest first.
func (r *PaymentRepo) ListByStatus(ctx context.Context, status domain.PaymentStatus, limit int) ([]domain.Payment, error) {
	return r.list(ctx, `WHERE status=? ORDER BY created_at, id LIMIT ?`, string(status), limit)
}

// PaidBytes sums the consumption covered by paid payments.
func (r *PaymentRepo) PaidBytes(ctx context.Context, routerID string, mac domain.MAC, period string) (uint64, error) {
	var n uint64
	err := r.db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(bytes), 0) FROM payments WHERE router_id=? AND mac=? AND period=? AND status=?`,
		routerID, mac.String(), period, string(domain.PaymentPaid)).Scan(&n)
	return n, err
}

func (r *PaymentRepo) list(ctx context.Context, tail string, args ...any) ([]domain.Payment, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+paymentCols+` FROM payments `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Payment{}
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanPayment(s scanner) (domain.Payment, error) {
	var (
		p                      domain.Payment
		mac, status            string
		created, updated, paid int64
	)
	if err := s.Scan(&p.ID, &p.RouterID, &mac, &p.Period, &p.Bytes, &p.AmountXOF, &p.CustomerName, &p.Phone,
		&p.OrderID, &p.CheckoutURL, &status, &created, &updated, &paid); err != nil {
		return domain.Payment{}, err
	}
	m, err := domain.ParseMAC(mac)
	if err != nil {
		return domain.Payment{}, err
	}
	p.MAC, p.Status = m, domain.PaymentStatus(status)
	p.CreatedAt, p.UpdatedAt, p.PaidAt = timeOrZero(created), timeOrZero(updated), timeOrZero(paid)
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
	rows, err := r.db.QueryContext(ctx, `SELECT key, value FROM settings WHERE key IN ('billing.price_per_gb','billing.min_amount')`)
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
		}
	}
	return s, rows.Err()
}

// SaveBillingSettings stores the settings.
func (r *SettingsRepo) SaveBillingSettings(ctx context.Context, s domain.BillingSettings) error {
	for k, v := range map[string]int64{"billing.price_per_gb": s.PricePerGB, "billing.min_amount": s.MinAmount} {
		if _, err := r.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, strconv.FormatInt(v, 10)); err != nil {
			return err
		}
	}
	return nil
}
