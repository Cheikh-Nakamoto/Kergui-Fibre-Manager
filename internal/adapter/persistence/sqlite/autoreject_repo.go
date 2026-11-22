package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// AutoRejectRepo persists the auto-reject setting and its tracked devices.
type AutoRejectRepo struct{ db *sql.DB }

var _ port.AutoRejectRepository = (*AutoRejectRepo)(nil)

// NewAutoRejectRepo wires the repository.
func NewAutoRejectRepo(db *DB) *AutoRejectRepo { return &AutoRejectRepo{db: db.SQL()} }

// AutoRejectSettings returns the box's setting, off when never saved.
func (r *AutoRejectRepo) AutoRejectSettings(ctx context.Context, routerID string) (domain.AutoRejectSettings, error) {
	var enabled int
	var since int64
	err := r.db.QueryRowContext(ctx, `SELECT enabled, since FROM autoreject_settings WHERE router_id=?`, routerID).Scan(&enabled, &since)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AutoRejectSettings{}, nil
	}
	if err != nil {
		return domain.AutoRejectSettings{}, err
	}
	return domain.AutoRejectSettings{Enabled: enabled != 0, Since: timeOrZero(since)}, nil
}

// SaveAutoRejectSettings stores the box's setting.
func (r *AutoRejectRepo) SaveAutoRejectSettings(ctx context.Context, routerID string, s domain.AutoRejectSettings) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO autoreject_settings (router_id, enabled, since) VALUES (?,?,?)
ON CONFLICT(router_id) DO UPDATE SET enabled=excluded.enabled, since=excluded.since`,
		routerID, boolToInt(s.Enabled), unixOrZero(s.Since))
	return err
}

// AutoRejectDevices returns the devices tracked for the box, by MAC.
func (r *AutoRejectRepo) AutoRejectDevices(ctx context.Context, routerID string) (map[domain.MAC]domain.AutoRejectDevice, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT mac, first_seen, state, decided_at FROM autoreject_devices WHERE router_id=?`, routerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[domain.MAC]domain.AutoRejectDevice{}
	for rows.Next() {
		var macStr, state string
		var first, decided int64
		if err := rows.Scan(&macStr, &first, &state, &decided); err != nil {
			return nil, err
		}
		mac, err := domain.ParseMAC(macStr)
		if err != nil {
			continue
		}
		out[mac] = domain.AutoRejectDevice{MAC: mac, FirstSeen: timeOrZero(first), State: domain.AutoRejectState(state), DecidedAt: timeOrZero(decided)}
	}
	return out, rows.Err()
}

// SaveAutoRejectDevice inserts or replaces one tracked device.
func (r *AutoRejectRepo) SaveAutoRejectDevice(ctx context.Context, routerID string, d domain.AutoRejectDevice) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO autoreject_devices (router_id, mac, first_seen, state, decided_at) VALUES (?,?,?,?,?)
ON CONFLICT(router_id, mac) DO UPDATE SET
  first_seen=excluded.first_seen, state=excluded.state, decided_at=excluded.decided_at`,
		routerID, d.MAC.String(), unixOrZero(d.FirstSeen), string(d.State), unixOrZero(d.DecidedAt))
	return err
}

// ClearAutoRejectDevices forgets every device tracked for the box.
func (r *AutoRejectRepo) ClearAutoRejectDevices(ctx context.Context, routerID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM autoreject_devices WHERE router_id=?`, routerID)
	return err
}
