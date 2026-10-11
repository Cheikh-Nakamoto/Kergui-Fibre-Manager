package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// IdentityRepo persists identity flags and blacklist cuts.
type IdentityRepo struct{ db *sql.DB }

var _ port.IdentityRepository = (*IdentityRepo)(nil)

// NewIdentityRepo wires the repository.
func NewIdentityRepo(db *DB) *IdentityRepo { return &IdentityRepo{db: db.SQL()} }

// Identities returns every stored identity state.
func (r *IdentityRepo) Identities(ctx context.Context, routerID string) (map[string]domain.IdentityState, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT identity, shared, blacklisted_at, lifted_at FROM identities WHERE router_id=?`, routerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]domain.IdentityState{}
	for rows.Next() {
		var id string
		var shared int
		var bl, lifted int64
		if err := rows.Scan(&id, &shared, &bl, &lifted); err != nil {
			return nil, err
		}
		out[id] = domain.IdentityState{Shared: shared != 0, BlacklistedAt: timeOrZero(bl), LiftedAt: timeOrZero(lifted)}
	}
	return out, rows.Err()
}

func (r *IdentityRepo) upsert(ctx context.Context, routerID, identity, set string, args ...any) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO identities (router_id, identity) VALUES (?,?)
ON CONFLICT(router_id, identity) DO NOTHING`, routerID, identity)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE identities SET `+set+` WHERE router_id=? AND identity=?`,
		append(args, routerID, identity)...)
	return err
}

// MarkShared flags an identity as used by several devices.
func (r *IdentityRepo) MarkShared(ctx context.Context, routerID, identity string) error {
	return r.upsert(ctx, routerID, identity, `shared=1`)
}

// SetBlacklisted starts a blacklist.
func (r *IdentityRepo) SetBlacklisted(ctx context.Context, routerID, identity string, at time.Time) error {
	return r.upsert(ctx, routerID, identity, `blacklisted_at=?`, at.Unix())
}

// Lift ends a blacklist; losses are counted afresh from at.
func (r *IdentityRepo) Lift(ctx context.Context, routerID, identity string, at time.Time) error {
	return r.upsert(ctx, routerID, identity, `lifted_at=?`, at.Unix())
}

// BlacklistCuts maps each device cut by a blacklist to its identity.
func (r *IdentityRepo) BlacklistCuts(ctx context.Context, routerID string) (map[domain.MAC]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT mac, identity FROM blacklist_cuts WHERE router_id=?`, routerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[domain.MAC]string{}
	for rows.Next() {
		var mac, id string
		if err := rows.Scan(&mac, &id); err != nil {
			return nil, err
		}
		if m, err := domain.ParseMAC(mac); err == nil {
			out[m] = id
		}
	}
	return out, rows.Err()
}

// RecordBlacklistCut stores a blacklist cut.
func (r *IdentityRepo) RecordBlacklistCut(ctx context.Context, routerID string, mac domain.MAC, identity string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO blacklist_cuts (router_id, mac, identity, cut_at) VALUES (?,?,?,?)
ON CONFLICT(router_id, mac) DO UPDATE SET identity=excluded.identity, cut_at=excluded.cut_at`,
		routerID, mac.String(), identity, time.Now().Unix())
	return err
}

// RemoveBlacklistCut forgets a blacklist cut.
func (r *IdentityRepo) RemoveBlacklistCut(ctx context.Context, routerID string, mac domain.MAC) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM blacklist_cuts WHERE router_id=? AND mac=?`, routerID, mac.String())
	return err
}
