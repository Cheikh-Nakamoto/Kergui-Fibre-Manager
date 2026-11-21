// Package infrastructure implements the licensing module's store on
// Postgres. Queries go through txctx.DB so they join the transaction
// InAccount opens, as the kernel's own repositories do.
package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agile-factory/kernel/kernel/txctx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

// Repo is the licensing store.
type Repo struct{ pool *pgxpool.Pool }

var _ usecase.Store = (*Repo)(nil)

// New wires the store.
func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) db(ctx context.Context) txctx.DBTX { return txctx.DB(ctx, r.pool) }

const uniqueViolation = "23505"

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == uniqueViolation
}

// InAccount locks the account row for the transaction: every write that
// reads a subscription first goes through here, so two webhooks, a trial
// and a transfer of the same account take turns.
func (r *Repo) InAccount(ctx context.Context, id domain.AccountID, fn func(context.Context) error) error {
	return txctx.ExecInTx(ctx, r.pool, func(ctx context.Context) error {
		var got string
		err := r.db(ctx).QueryRow(ctx,
			`SELECT id::text FROM hub_licensing_accounts WHERE id = $1 FOR UPDATE`, string(id)).Scan(&got)
		if errors.Is(err, pgx.ErrNoRows) {
			return usecase.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("licensing: lock account: %w", err)
		}
		return fn(ctx)
	})
}

// --- accounts -----------------------------------------------------------------

// EnsureAccount returns the account of a number, creating it with a's id
// if there is none. Two sign-ins racing end on the same row.
func (r *Repo) EnsureAccount(ctx context.Context, a usecase.Account) (usecase.Account, error) {
	_, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_licensing_accounts (id, phone, created_at) VALUES ($1, $2, $3)
		 ON CONFLICT (phone) DO NOTHING`, string(a.ID), string(a.Phone), a.CreatedAt)
	if err != nil {
		return usecase.Account{}, fmt.Errorf("licensing: ensure account: %w", err)
	}
	return r.AccountByPhone(ctx, a.Phone)
}

func (r *Repo) AccountByID(ctx context.Context, id domain.AccountID) (usecase.Account, error) {
	return r.account(ctx, `SELECT id::text, phone, created_at FROM hub_licensing_accounts WHERE id = $1`, string(id))
}

func (r *Repo) AccountByPhone(ctx context.Context, p domain.Phone) (usecase.Account, error) {
	return r.account(ctx, `SELECT id::text, phone, created_at FROM hub_licensing_accounts WHERE phone = $1`, string(p))
}

func (r *Repo) account(ctx context.Context, q string, arg string) (usecase.Account, error) {
	var (
		a         usecase.Account
		id, phone string
	)
	err := r.db(ctx).QueryRow(ctx, q, arg).Scan(&id, &phone, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return usecase.Account{}, usecase.ErrNotFound
	}
	if err != nil {
		return usecase.Account{}, fmt.Errorf("licensing: read account: %w", err)
	}
	a.ID, a.Phone = domain.AccountID(id), domain.Phone(phone)
	return a, nil
}

// --- OTP ----------------------------------------------------------------------

func (r *Repo) CreateChallenge(ctx context.Context, c usecase.Challenge) error {
	_, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_licensing_otp_challenges (id, phone, code_hash, expires_at, attempts, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		c.ID, string(c.Phone), c.CodeHash, c.ExpiresAt, c.Attempts, c.CreatedAt)
	if err != nil {
		return fmt.Errorf("licensing: create code: %w", err)
	}
	return nil
}

func (r *Repo) ChallengesSince(ctx context.Context, p domain.Phone, since time.Time) (int, time.Time, error) {
	var (
		n      int
		latest *time.Time
	)
	err := r.db(ctx).QueryRow(ctx,
		`SELECT count(*), max(created_at) FROM hub_licensing_otp_challenges WHERE phone = $1 AND created_at >= $2`,
		string(p), since).Scan(&n, &latest)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("licensing: count codes: %w", err)
	}
	if latest == nil {
		return n, time.Time{}, nil
	}
	return n, *latest, nil
}

func (r *Repo) LatestChallenge(ctx context.Context, p domain.Phone) (usecase.Challenge, error) {
	var (
		c     usecase.Challenge
		phone string
	)
	err := r.db(ctx).QueryRow(ctx,
		`SELECT id::text, phone, code_hash, expires_at, attempts, consumed_at, created_at
		 FROM hub_licensing_otp_challenges WHERE phone = $1 ORDER BY created_at DESC, id DESC LIMIT 1`,
		string(p)).Scan(&c.ID, &phone, &c.CodeHash, &c.ExpiresAt, &c.Attempts, &c.ConsumedAt, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return usecase.Challenge{}, usecase.ErrNotFound
	}
	if err != nil {
		return usecase.Challenge{}, fmt.Errorf("licensing: read code: %w", err)
	}
	c.Phone = domain.Phone(phone)
	return c, nil
}

func (r *Repo) AddAttempt(ctx context.Context, id string, max int) (bool, error) {
	tag, err := r.db(ctx).Exec(ctx,
		`UPDATE hub_licensing_otp_challenges SET attempts = attempts + 1 WHERE id = $1 AND attempts < $2`, id, max)
	if err != nil {
		return false, fmt.Errorf("licensing: count attempt: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Repo) ConsumeChallenge(ctx context.Context, id string, at time.Time) (bool, error) {
	tag, err := r.db(ctx).Exec(ctx,
		`UPDATE hub_licensing_otp_challenges SET consumed_at = $2 WHERE id = $1 AND consumed_at IS NULL`, id, at)
	if err != nil {
		return false, fmt.Errorf("licensing: spend code: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// --- sessions -----------------------------------------------------------------

func (r *Repo) CreateSession(ctx context.Context, hash []byte, s usecase.Session) error {
	_, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_licensing_sessions (token_hash, account_id, device, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5)`, hash, string(s.Account), s.Device, s.CreatedAt, s.ExpiresAt)
	if err != nil {
		return fmt.Errorf("licensing: create session: %w", err)
	}
	return nil
}

func (r *Repo) SessionByHash(ctx context.Context, hash []byte) (usecase.Session, error) {
	var (
		s  usecase.Session
		id string
	)
	err := r.db(ctx).QueryRow(ctx,
		`SELECT account_id::text, device, created_at, expires_at, revoked_at
		 FROM hub_licensing_sessions WHERE token_hash = $1`, hash).
		Scan(&id, &s.Device, &s.CreatedAt, &s.ExpiresAt, &s.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return usecase.Session{}, usecase.ErrNotFound
	}
	if err != nil {
		return usecase.Session{}, fmt.Errorf("licensing: read session: %w", err)
	}
	s.Account = domain.AccountID(id)
	return s, nil
}

func (r *Repo) RevokeSession(ctx context.Context, hash []byte, at time.Time) error {
	tag, err := r.db(ctx).Exec(ctx,
		`UPDATE hub_licensing_sessions SET revoked_at = COALESCE(revoked_at, $2) WHERE token_hash = $1`, hash, at)
	if err != nil {
		return fmt.Errorf("licensing: revoke session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return usecase.ErrNotFound
	}
	return nil
}

// --- subscriptions ------------------------------------------------------------

const subColumns = `account_id::text, box_id, tier, expires_at, reminder_sent_for`

func scanSub(row pgx.Row) (domain.Subscription, error) {
	var (
		s        domain.Subscription
		acc, box string
		tier     string
		reminder *time.Time
	)
	if err := row.Scan(&acc, &box, &tier, &s.ExpiresAt, &reminder); err != nil {
		return domain.Subscription{}, err
	}
	s.Account, s.BoxID, s.Tier = domain.AccountID(acc), domain.BoxID(box), domain.Tier(tier)
	s.ExpiresAt = s.ExpiresAt.UTC()
	if reminder != nil {
		s.ReminderSentFor = reminder.UTC()
	}
	return s, nil
}

func (r *Repo) Subscription(ctx context.Context, a domain.AccountID, b domain.BoxID) (domain.Subscription, error) {
	s, err := scanSub(r.db(ctx).QueryRow(ctx,
		`SELECT `+subColumns+` FROM hub_licensing_subscriptions WHERE account_id = $1 AND box_id = $2`,
		string(a), string(b)))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Subscription{}, usecase.ErrNotFound
	}
	if err != nil {
		return domain.Subscription{}, fmt.Errorf("licensing: read subscription: %w", err)
	}
	return s, nil
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (r *Repo) SaveSubscription(ctx context.Context, s domain.Subscription) error {
	_, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_licensing_subscriptions (account_id, box_id, tier, expires_at, reminder_sent_for, updated_at)
		 VALUES ($1, $2, $3, $4, $5, now())
		 ON CONFLICT (account_id, box_id) DO UPDATE SET
		   tier = EXCLUDED.tier, expires_at = EXCLUDED.expires_at,
		   reminder_sent_for = EXCLUDED.reminder_sent_for, updated_at = now()`,
		string(s.Account), string(s.BoxID), string(s.Tier), s.ExpiresAt, nullTime(s.ReminderSentFor))
	if err != nil {
		return fmt.Errorf("licensing: save subscription: %w", err)
	}
	return nil
}

func (r *Repo) DeleteSubscription(ctx context.Context, a domain.AccountID, b domain.BoxID) error {
	_, err := r.db(ctx).Exec(ctx,
		`DELETE FROM hub_licensing_subscriptions WHERE account_id = $1 AND box_id = $2`, string(a), string(b))
	if err != nil {
		return fmt.Errorf("licensing: delete subscription: %w", err)
	}
	return nil
}

func (r *Repo) ListSubscriptions(ctx context.Context, a domain.AccountID) ([]domain.Subscription, error) {
	return r.subs(ctx,
		`SELECT `+subColumns+` FROM hub_licensing_subscriptions WHERE account_id = $1 ORDER BY box_id`, string(a))
}

func (r *Repo) subs(ctx context.Context, q string, args ...any) ([]domain.Subscription, error) {
	rows, err := r.db(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("licensing: list subscriptions: %w", err)
	}
	defer rows.Close()
	var out []domain.Subscription
	for rows.Next() {
		s, err := scanSub(rows)
		if err != nil {
			return nil, fmt.Errorf("licensing: list subscriptions: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// --- trial and transfers --------------------------------------------------------

func (r *Repo) TrialUsed(ctx context.Context, p domain.Phone, b domain.BoxID) (phoneUsed, boxUsed bool, err error) {
	err = r.db(ctx).QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM hub_licensing_trials WHERE phone = $1),
		        EXISTS (SELECT 1 FROM hub_licensing_trials WHERE box_id = $2)`,
		string(p), string(b)).Scan(&phoneUsed, &boxUsed)
	if err != nil {
		return false, false, fmt.Errorf("licensing: read trials: %w", err)
	}
	return phoneUsed, boxUsed, nil
}

func (r *Repo) RecordTrial(ctx context.Context, t usecase.Trial) error {
	_, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_licensing_trials (phone, box_id, account_id, started_at) VALUES ($1, $2, $3, $4)`,
		string(t.Phone), string(t.BoxID), string(t.Account), t.StartedAt)
	if isUnique(err) {
		return usecase.ErrTrialRace
	}
	if err != nil {
		return fmt.Errorf("licensing: record trial: %w", err)
	}
	return nil
}

func (r *Repo) TransfersSince(ctx context.Context, a domain.AccountID, since time.Time) ([]time.Time, error) {
	rows, err := r.db(ctx).Query(ctx,
		`SELECT at FROM hub_licensing_transfers WHERE account_id = $1 AND at >= $2 ORDER BY at`, string(a), since)
	if err != nil {
		return nil, fmt.Errorf("licensing: list transfers: %w", err)
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var t time.Time
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("licensing: list transfers: %w", err)
		}
		out = append(out, t.UTC())
	}
	return out, rows.Err()
}

func (r *Repo) RecordTransfer(ctx context.Context, t usecase.Transfer) error {
	_, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_licensing_transfers (account_id, from_box_id, to_box_id, at) VALUES ($1, $2, $3, $4)`,
		string(t.Account), string(t.From), string(t.To), t.At)
	if err != nil {
		return fmt.Errorf("licensing: record transfer: %w", err)
	}
	return nil
}

// --- payments ---------------------------------------------------------------------

const payColumns = `id::text, provider, provider_ref, account_id::text, box_id, pass_code, amount_xof, status,
	checkout_url, created_at, credited_at`

func scanPayment(row pgx.Row) (usecase.Payment, error) {
	var (
		p            usecase.Payment
		acc, box, st string
	)
	err := row.Scan(&p.ID, &p.Provider, &p.Reference, &acc, &box, &p.PassCode, &p.AmountXOF, &st,
		&p.CheckoutURL, &p.CreatedAt, &p.CreditedAt)
	p.Account, p.BoxID, p.Status = domain.AccountID(acc), domain.BoxID(box), usecase.PaymentStatus(st)
	return p, err
}

// CreatePayment records a payment. A reference already recorded is
// ErrPaymentMismatch: the caller looked it up first, so it is a race or a
// reuse, never a replay to honour twice.
func (r *Repo) CreatePayment(ctx context.Context, p usecase.Payment) error {
	_, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_licensing_payments
		   (id, provider, provider_ref, account_id, box_id, pass_code, amount_xof, status, checkout_url, created_at, credited_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		p.ID, p.Provider, p.Reference, string(p.Account), string(p.BoxID), p.PassCode, p.AmountXOF,
		string(p.Status), p.CheckoutURL, p.CreatedAt, p.CreditedAt)
	if isUnique(err) {
		return usecase.ErrPaymentMismatch
	}
	if err != nil {
		return fmt.Errorf("licensing: create payment: %w", err)
	}
	return nil
}

func (r *Repo) PaymentByRef(ctx context.Context, provider, ref string) (usecase.Payment, error) {
	p, err := scanPayment(r.db(ctx).QueryRow(ctx,
		`SELECT `+payColumns+` FROM hub_licensing_payments WHERE provider = $1 AND provider_ref = $2`, provider, ref))
	if errors.Is(err, pgx.ErrNoRows) {
		return usecase.Payment{}, usecase.ErrNotFound
	}
	if err != nil {
		return usecase.Payment{}, fmt.Errorf("licensing: read payment: %w", err)
	}
	return p, nil
}

func (r *Repo) SetPaymentStatus(ctx context.Context, id string, st usecase.PaymentStatus, at time.Time) error {
	tag, err := r.db(ctx).Exec(ctx,
		`UPDATE hub_licensing_payments SET status = $2,
		   credited_at = CASE WHEN $2 = 'credited' THEN $3::timestamptz ELSE credited_at END
		 WHERE id = $1`, id, string(st), at)
	if err != nil {
		return fmt.Errorf("licensing: update payment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return usecase.ErrNotFound
	}
	return nil
}

func (r *Repo) PendingPayments(ctx context.Context, provider string, since time.Time, limit int) ([]usecase.Payment, error) {
	rows, err := r.db(ctx).Query(ctx,
		`SELECT `+payColumns+` FROM hub_licensing_payments
		 WHERE provider = $1 AND status = 'pending' AND created_at >= $2 ORDER BY created_at LIMIT $3`,
		provider, since, limit)
	if err != nil {
		return nil, fmt.Errorf("licensing: list pending payments: %w", err)
	}
	defer rows.Close()
	var out []usecase.Payment
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			return nil, fmt.Errorf("licensing: list pending payments: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- reminders --------------------------------------------------------------------

func (r *Repo) DueReminders(ctx context.Context, now, until time.Time, limit int) ([]domain.Subscription, error) {
	return r.subs(ctx,
		`SELECT `+subColumns+` FROM hub_licensing_subscriptions
		 WHERE tier IN ('basic', 'max') AND expires_at > $1 AND expires_at <= $2
		   AND reminder_sent_for IS DISTINCT FROM expires_at
		 ORDER BY expires_at LIMIT $3`, now, until, limit)
}

// ClaimReminder is a conditional update: only one sweep, on one hub
// instance, flips reminder_sent_for for a given expiry.
func (r *Repo) ClaimReminder(ctx context.Context, s domain.Subscription, at time.Time) (bool, error) {
	claimed := false
	err := txctx.ExecInTx(ctx, r.pool, func(ctx context.Context) error {
		tag, err := r.db(ctx).Exec(ctx,
			`UPDATE hub_licensing_subscriptions SET reminder_sent_for = expires_at
			 WHERE account_id = $1 AND box_id = $2 AND expires_at = $3
			   AND reminder_sent_for IS DISTINCT FROM expires_at`,
			string(s.Account), string(s.BoxID), s.ExpiresAt)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		claimed = true
		_, err = r.db(ctx).Exec(ctx,
			`INSERT INTO hub_licensing_reminders (account_id, box_id, expires_at, sent_at) VALUES ($1, $2, $3, $4)
			 ON CONFLICT (account_id, box_id, expires_at) DO UPDATE SET sent_at = EXCLUDED.sent_at`,
			string(s.Account), string(s.BoxID), s.ExpiresAt, at)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("licensing: claim reminder: %w", err)
	}
	return claimed, nil
}

func (r *Repo) ReleaseReminder(ctx context.Context, s domain.Subscription) error {
	return txctx.ExecInTx(ctx, r.pool, func(ctx context.Context) error {
		if _, err := r.db(ctx).Exec(ctx,
			`UPDATE hub_licensing_subscriptions SET reminder_sent_for = $4
			 WHERE account_id = $1 AND box_id = $2 AND expires_at = $3`,
			string(s.Account), string(s.BoxID), s.ExpiresAt, nullTime(s.ReminderSentFor)); err != nil {
			return fmt.Errorf("licensing: release reminder: %w", err)
		}
		_, err := r.db(ctx).Exec(ctx,
			`DELETE FROM hub_licensing_reminders WHERE account_id = $1 AND box_id = $2 AND expires_at = $3`,
			string(s.Account), string(s.BoxID), s.ExpiresAt)
		return err
	})
}
