package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// UsageRepo persists traffic samples and per-period consumption.
type UsageRepo struct{ db *sql.DB }

var _ port.UsageRepository = (*UsageRepo)(nil)

// NewUsageRepo wires the repository.
func NewUsageRepo(db *DB) *UsageRepo { return &UsageRepo{db: db.SQL()} }

// LastSamples returns the most recent counters stored for each device.
func (r *UsageRepo) LastSamples(ctx context.Context, routerID string) (map[domain.MAC]domain.TrafficCounters, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT mac, rx, tx, link_secs FROM traffic_last WHERE router_id=?`, routerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[domain.MAC]domain.TrafficCounters{}
	for rows.Next() {
		var macStr string
		var c domain.TrafficCounters
		if err := rows.Scan(&macStr, &c.Rx, &c.Tx, &c.LinkSecs); err != nil {
			return nil, err
		}
		mac, err := domain.ParseMAC(macStr)
		if err != nil {
			continue
		}
		out[mac] = c
	}
	return out, rows.Err()
}

// RecordSamples stores samples and adds deltas to usage in one transaction.
func (r *UsageRepo) RecordSamples(ctx context.Context, routerID, period string,
	samples map[domain.MAC]domain.TrafficCounters, deltas map[domain.MAC]uint64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().Unix()
	for mac, c := range samples {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO traffic_last (router_id, mac, rx, tx, link_secs, sampled_at) VALUES (?,?,?,?,?,?)
ON CONFLICT(router_id, mac) DO UPDATE SET
  rx=excluded.rx, tx=excluded.tx, link_secs=excluded.link_secs, sampled_at=excluded.sampled_at`,
			routerID, mac.String(), c.Rx, c.Tx, c.LinkSecs, now); err != nil {
			return err
		}
	}
	for mac, d := range deltas {
		if d == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO usage (router_id, mac, period, bytes) VALUES (?,?,?,?)
ON CONFLICT(router_id, mac, period) DO UPDATE SET bytes = bytes + excluded.bytes`,
			routerID, mac.String(), period, d); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UsageByPeriod returns each device's consumption for period.
func (r *UsageRepo) UsageByPeriod(ctx context.Context, routerID, period string) ([]domain.Usage, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT mac, bytes FROM usage WHERE router_id=? AND period=? ORDER BY bytes DESC`, routerID, period)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Usage{}
	for rows.Next() {
		var macStr string
		var b uint64
		if err := rows.Scan(&macStr, &b); err != nil {
			return nil, err
		}
		mac, err := domain.ParseMAC(macStr)
		if err != nil {
			continue
		}
		out = append(out, domain.Usage{MAC: mac, Period: period, Bytes: b})
	}
	return out, rows.Err()
}
