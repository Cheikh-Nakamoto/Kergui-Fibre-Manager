package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// MemberRepo persists members and their devices.
type MemberRepo struct{ db *sql.DB }

var _ port.MemberRepository = (*MemberRepo)(nil)

// NewMemberRepo wires the repository.
func NewMemberRepo(db *DB) *MemberRepo { return &MemberRepo{db: db.SQL()} }

// Create inserts a member.
func (r *MemberRepo) Create(ctx context.Context, m domain.Member) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO members (id, router_id, name, phone, kind, active, protected, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		m.ID, m.RouterID, m.Name, m.Phone, string(m.Kind), boolToInt(m.Active), boolToInt(m.Protected), unixOrZero(m.CreatedAt))
	return err
}

// Update rewrites a member's details.
func (r *MemberRepo) Update(ctx context.Context, m domain.Member) error {
	res, err := r.db.ExecContext(ctx, `
UPDATE members SET name=?, phone=?, kind=?, active=?, protected=? WHERE router_id=? AND id=?`,
		m.Name, m.Phone, string(m.Kind), boolToInt(m.Active), boolToInt(m.Protected), m.RouterID, m.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrMemberNotFound
	}
	return nil
}

// Delete removes a member and frees their devices.
func (r *MemberRepo) Delete(ctx context.Context, routerID, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM members WHERE router_id=? AND id=?`, routerID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrMemberNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM member_devices WHERE router_id=? AND member_id=?`, routerID, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Get returns one member with their MACs.
func (r *MemberRepo) Get(ctx context.Context, routerID, id string) (domain.Member, bool, error) {
	ms, err := r.query(ctx, `WHERE router_id=? AND id=?`, routerID, id)
	if err != nil || len(ms) == 0 {
		return domain.Member{}, false, err
	}
	return ms[0], true, nil
}

// List returns all members with their MACs, by name.
func (r *MemberRepo) List(ctx context.Context, routerID string) ([]domain.Member, error) {
	return r.query(ctx, `WHERE router_id=? ORDER BY lower(name), id`, routerID)
}

// Assign moves a device to a member, or unassigns it when memberID is "".
func (r *MemberRepo) Assign(ctx context.Context, routerID string, mac domain.MAC, memberID string) error {
	if memberID == "" {
		_, err := r.db.ExecContext(ctx, `DELETE FROM member_devices WHERE router_id=? AND mac=?`, routerID, mac.String())
		return err
	}
	if _, ok, err := r.Get(ctx, routerID, memberID); err != nil {
		return err
	} else if !ok {
		return domain.ErrMemberNotFound
	}
	_, err := r.db.ExecContext(ctx, `
INSERT INTO member_devices (router_id, mac, member_id) VALUES (?,?,?)
ON CONFLICT(router_id, mac) DO UPDATE SET member_id=excluded.member_id`, routerID, mac.String(), memberID)
	return err
}

func (r *MemberRepo) query(ctx context.Context, tail string, args ...any) ([]domain.Member, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, router_id, name, phone, kind, active, protected, created_at FROM members `+tail, args...)
	if err != nil {
		return nil, err
	}
	out := []domain.Member{}
	for rows.Next() {
		var m domain.Member
		var kind string
		var active, protected int
		var created int64
		if err := rows.Scan(&m.ID, &m.RouterID, &m.Name, &m.Phone, &kind, &active, &protected, &created); err != nil {
			rows.Close()
			return nil, err
		}
		m.Kind, m.Active, m.Protected, m.CreatedAt = domain.MemberKind(kind), active != 0, protected != 0, timeOrZero(created)
		m.MACs = []domain.MAC{}
		out = append(out, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		macs, err := r.db.QueryContext(ctx, `SELECT mac FROM member_devices WHERE router_id=? AND member_id=? ORDER BY mac`,
			out[i].RouterID, out[i].ID)
		if err != nil {
			return nil, err
		}
		for macs.Next() {
			var s string
			if err := macs.Scan(&s); err != nil {
				macs.Close()
				return nil, err
			}
			if mac, err := domain.ParseMAC(s); err == nil {
				out[i].MACs = append(out[i].MACs, mac)
			}
		}
		macs.Close()
	}
	return out, nil
}

// CutRepo remembers the devices cut for an unpaid invoice.
type CutRepo struct{ db *sql.DB }

var _ port.CutRepository = (*CutRepo)(nil)

// NewCutRepo wires the repository.
func NewCutRepo(db *DB) *CutRepo { return &CutRepo{db: db.SQL()} }

// Cuts maps each cut device to the payment that caused it.
func (r *CutRepo) Cuts(ctx context.Context, routerID string) (map[domain.MAC]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT mac, payment_id FROM enforcement_cuts WHERE router_id=?`, routerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[domain.MAC]string{}
	for rows.Next() {
		var mac, pid string
		if err := rows.Scan(&mac, &pid); err != nil {
			return nil, err
		}
		if m, err := domain.ParseMAC(mac); err == nil {
			out[m] = pid
		}
	}
	return out, rows.Err()
}

// RecordCut stores a cut.
func (r *CutRepo) RecordCut(ctx context.Context, routerID string, mac domain.MAC, paymentID string) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO enforcement_cuts (router_id, mac, payment_id, cut_at) VALUES (?,?,?,?)
ON CONFLICT(router_id, mac) DO UPDATE SET payment_id=excluded.payment_id, cut_at=excluded.cut_at`,
		routerID, mac.String(), paymentID, time.Now().Unix())
	return err
}

// RemoveCut forgets a cut once the device is restored.
func (r *CutRepo) RemoveCut(ctx context.Context, routerID string, mac domain.MAC) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM enforcement_cuts WHERE router_id=? AND mac=?`, routerID, mac.String())
	return err
}
