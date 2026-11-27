package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// WiFiLockRepo persists the closed-Wi-Fi intent of each box and its devices
// « déconnectés du Wi-Fi » (RFC-007 D5, D7).
type WiFiLockRepo struct{ db *sql.DB }

var _ port.WiFiLockRepository = (*WiFiLockRepo)(nil)

// NewWiFiLockRepo wires the repository.
func NewWiFiLockRepo(db *DB) *WiFiLockRepo { return &WiFiLockRepo{db: db.SQL()} }

// WiFiLockIntent returns the box's intent, open when never saved.
func (r *WiFiLockRepo) WiFiLockIntent(ctx context.Context, routerID string) (domain.WiFiLockIntent, error) {
	var closed, visitorsClosed int
	var household, visitors string
	var until, since int64
	err := r.db.QueryRowContext(ctx, `
SELECT closed, household_aps, visitor_aps, visitors_closed, window_until, since
FROM wifi_lock WHERE router_id=?`, routerID).Scan(&closed, &household, &visitors, &visitorsClosed, &until, &since)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WiFiLockIntent{}, nil
	}
	if err != nil {
		return domain.WiFiLockIntent{}, err
	}
	i := domain.WiFiLockIntent{
		Closed:         closed != 0,
		VisitorsClosed: visitorsClosed != 0,
		WindowUntil:    timeOrZero(until),
		Since:          timeOrZero(since),
	}
	if i.HouseholdAPs, err = decodeAPs(household); err != nil {
		return domain.WiFiLockIntent{}, err
	}
	if i.VisitorAPs, err = decodeAPs(visitors); err != nil {
		return domain.WiFiLockIntent{}, err
	}
	return i, nil
}

// SaveWiFiLockIntent stores the box's intent.
func (r *WiFiLockRepo) SaveWiFiLockIntent(ctx context.Context, routerID string, i domain.WiFiLockIntent) error {
	household, err := encodeAPs(i.HouseholdAPs)
	if err != nil {
		return err
	}
	visitors, err := encodeAPs(i.VisitorAPs)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
INSERT INTO wifi_lock (router_id, closed, household_aps, visitor_aps, visitors_closed, window_until, since)
VALUES (?,?,?,?,?,?,?)
ON CONFLICT(router_id) DO UPDATE SET
  closed=excluded.closed, household_aps=excluded.household_aps, visitor_aps=excluded.visitor_aps,
  visitors_closed=excluded.visitors_closed, window_until=excluded.window_until, since=excluded.since`,
		routerID, boolToInt(i.Closed), household, visitors, boolToInt(i.VisitorsClosed),
		unixOrZero(i.WindowUntil), unixOrZero(i.Since))
	return err
}

// WiFiCutMACs lists the box's devices « déconnectés du Wi-Fi », sorted.
func (r *WiFiLockRepo) WiFiCutMACs(ctx context.Context, routerID string) ([]domain.MAC, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT mac FROM wifi_cut_devices WHERE router_id=? ORDER BY mac`, routerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.MAC
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if m, err := domain.ParseMAC(raw); err == nil {
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

// SetWiFiCut sets (cut) or clears a device's « déconnecté du Wi-Fi » flag.
func (r *WiFiLockRepo) SetWiFiCut(ctx context.Context, routerID string, mac domain.MAC, cut bool) error {
	if mac.IsZero() {
		return domain.ErrInvalidMAC
	}
	q := `DELETE FROM wifi_cut_devices WHERE router_id=? AND mac=?`
	if cut {
		q = `INSERT INTO wifi_cut_devices (router_id, mac) VALUES (?,?) ON CONFLICT(router_id, mac) DO NOTHING`
	}
	_, err := r.db.ExecContext(ctx, q, routerID, mac.String())
	return err
}

func encodeAPs(aps []string) (string, error) {
	if aps == nil {
		aps = []string{}
	}
	b, err := json.Marshal(aps)
	return string(b), err
}

func decodeAPs(raw string) ([]string, error) {
	var aps []string
	if raw == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(raw), &aps); err != nil {
		return nil, err
	}
	if len(aps) == 0 {
		return nil, nil
	}
	return aps, nil
}
