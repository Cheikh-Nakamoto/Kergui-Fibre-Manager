package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// HistoryRepo lists the whole device inventory joined with consumption.
type HistoryRepo struct{ db *sql.DB }

var _ port.DeviceHistory = (*HistoryRepo)(nil)

// NewHistoryRepo wires the repository.
func NewHistoryRepo(db *DB) *HistoryRepo { return &HistoryRepo{db: db.SQL()} }

// ListWithUsage implements port.DeviceHistory.
func (r *HistoryRepo) ListWithUsage(ctx context.Context, routerID, period, query string, limit, offset int) ([]domain.DeviceUsage, int, error) {
	where := "d.router_id = ?"
	args := []any{routerID}
	if q := strings.TrimSpace(query); q != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.ToLower(q)) + "%"
		where += ` AND (lower(d.custom_name) LIKE ? ESCAPE '\' OR lower(d.hostname) LIKE ? ESCAPE '\'
			OR lower(d.mac) LIKE ? ESCAPE '\' OR d.ip LIKE ? ESCAPE '\')`
		args = append(args, like, like, like, like)
	}

	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM devices d WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx, `
SELECT d.id, d.router_id, d.mac, d.ip, d.hostname, d.custom_name, d.vendor, d.ssid,
       d.first_seen, d.last_seen, d.connected, d.blocked, d.notes, COALESCE(u.bytes, 0)
FROM devices d
LEFT JOIN usage u ON u.router_id = d.router_id AND u.mac = d.mac AND u.period = ?
WHERE `+where+`
ORDER BY d.last_seen DESC, d.mac
LIMIT ? OFFSET ?`, append(append([]any{period}, args...), limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []domain.DeviceUsage{}
	for rows.Next() {
		var bytes uint64
		d, err := scanDevice(scanFunc(func(dest ...any) error {
			return rows.Scan(append(dest, &bytes)...)
		}))
		if err != nil {
			return nil, 0, err
		}
		out = append(out, domain.DeviceUsage{Device: d, Bytes: bytes})
	}
	return out, total, rows.Err()
}

// scanFunc adapts a closure to the scanner interface used by scanDevice.
type scanFunc func(dest ...any) error

func (f scanFunc) Scan(dest ...any) error { return f(dest...) }
