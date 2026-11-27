package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// RulePurgeRepo remembers when each box's stale rules were last purged.
type RulePurgeRepo struct{ db *sql.DB }

var _ port.RulePurgeRepository = (*RulePurgeRepo)(nil)

// NewRulePurgeRepo wires the repository.
func NewRulePurgeRepo(db *DB) *RulePurgeRepo { return &RulePurgeRepo{db: db.SQL()} }

// LastRulePurge returns when the box was last purged, zero when never.
func (r *RulePurgeRepo) LastRulePurge(ctx context.Context, routerID string) (time.Time, error) {
	var at int64
	err := r.db.QueryRowContext(ctx, `SELECT last_at FROM rule_purges WHERE router_id=?`, routerID).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return timeOrZero(at), nil
}

// SaveRulePurge records a purge of the box at at.
func (r *RulePurgeRepo) SaveRulePurge(ctx context.Context, routerID string, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO rule_purges (router_id, last_at) VALUES (?,?)
ON CONFLICT(router_id) DO UPDATE SET last_at=excluded.last_at`, routerID, unixOrZero(at))
	return err
}
