package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// ReminderRepo stores "pay later" choices.
type ReminderRepo struct{ db *sql.DB }

var _ port.ReminderRepository = (*ReminderRepo)(nil)

// NewReminderRepo wires the repository.
func NewReminderRepo(db *DB) *ReminderRepo { return &ReminderRepo{db: db.SQL()} }

// Snoozes maps payment ids to when their reminder may show again.
func (r *ReminderRepo) Snoozes(ctx context.Context, routerID string) (map[string]time.Time, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT payment_id, until FROM payment_snoozes WHERE router_id=?`, routerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var id string
		var until int64
		if err := rows.Scan(&id, &until); err != nil {
			return nil, err
		}
		out[id] = timeOrZero(until)
	}
	return out, rows.Err()
}

// Snooze hides a payment link's reminder until the given time.
func (r *ReminderRepo) Snooze(ctx context.Context, routerID, paymentID string, until time.Time) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO payment_snoozes (router_id, payment_id, until) VALUES (?,?,?)
ON CONFLICT(router_id, payment_id) DO UPDATE SET until=excluded.until`, routerID, paymentID, until.Unix())
	return err
}
