package sqlite

import (
	"context"
	"database/sql"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// AccessRuleRepo persists access-control rules, kept separate from devices.
type AccessRuleRepo struct{ db *sql.DB }

var _ port.AccessRuleRepository = (*AccessRuleRepo)(nil)

// NewAccessRuleRepo wires the repository.
func NewAccessRuleRepo(db *DB) *AccessRuleRepo { return &AccessRuleRepo{db: db.SQL()} }

// ReplaceForRouter atomically replaces the router's rule set with the given rules
// (the router remains the source of truth; the local copy is a mirror).
func (r *AccessRuleRepo) ReplaceForRouter(ctx context.Context, routerID string, rules []domain.AccessRule) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM access_rules WHERE router_id=?`, routerID); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO access_rules (id, router_id, mac, mode, ssid, source, raw_ref, created_at)
VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT(router_id, mac, mode) DO UPDATE SET
  ssid=excluded.ssid, source=excluded.source, raw_ref=excluded.raw_ref`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, rule := range rules {
		id := rule.ID
		if id == "" {
			id = usecase.AccessRuleID(routerID, rule.MAC)
		}
		if _, err := stmt.ExecContext(ctx, id, routerID, rule.MAC.String(), rule.Mode.String(),
			rule.SSID, rule.Source, rule.RawRef, unixOrZero(rule.CreatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListByRouter lists the router's rules ordered by MAC.
func (r *AccessRuleRepo) ListByRouter(ctx context.Context, routerID string) ([]domain.AccessRule, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, router_id, mac, mode, ssid, source, raw_ref, created_at
FROM access_rules WHERE router_id=? ORDER BY mac`, routerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AccessRule
	for rows.Next() {
		var (
			id, rid, mac, mode, ssid, source, rawRef string
			createdAt                                int64
		)
		if err := rows.Scan(&id, &rid, &mac, &mode, &ssid, &source, &rawRef, &createdAt); err != nil {
			return nil, err
		}
		m, err := domain.ParseMAC(mac)
		if err != nil {
			return nil, err
		}
		am, _ := domain.ParseAccessMode(mode)
		out = append(out, domain.AccessRule{
			ID:        id,
			RouterID:  rid,
			MAC:       m,
			Mode:      am,
			SSID:      ssid,
			Source:    source,
			RawRef:    rawRef,
			CreatedAt: timeOrZero(createdAt),
		})
	}
	return out, rows.Err()
}
