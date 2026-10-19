// Package infrastructure implements the inventory's repository on Postgres.
// Queries go through txctx.DB so they join the caller's transaction, as the
// kernel's own repositories do.
package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/agile-factory/kernel/kernel/txctx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/inventory/domain"
)

// Repo is the inventory's storage.
type Repo struct{ pool *pgxpool.Pool }

var _ domain.Repository = (*Repo)(nil)

// New wires the repository.
func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) db(ctx context.Context) txctx.DBTX { return txctx.DB(ctx, r.pool) }

// Diff loads the household's inventory, hands it to decide and writes the
// result, all in one transaction.
//
// The household's baseline row is locked first, so two snapshots of the same
// household queue behind each other. Without that lock, two relays pushing at
// once could each decide a MAC is new and give it two device numbers.
func (r *Repo) Diff(ctx context.Context, tenantID string, decide func(domain.Snapshot) (domain.Changes, error)) (domain.Changes, error) {
	var out domain.Changes
	err := txctx.ExecInTx(ctx, r.pool, func(ctx context.Context) error {
		db := r.db(ctx)
		if _, err := db.Exec(ctx,
			`INSERT INTO hub_inventory_baseline (tenant_id, set_at, device_seq)
			 VALUES ($1, to_timestamp(0), 0) ON CONFLICT (tenant_id) DO NOTHING`, tenantID); err != nil {
			return fmt.Errorf("inventory: baseline row: %w", err)
		}
		var (
			inv   domain.Snapshot
			setAt time.Time
		)
		if err := db.QueryRow(ctx,
			`SELECT set_at, device_seq FROM hub_inventory_baseline WHERE tenant_id = $1 FOR UPDATE`,
			tenantID).Scan(&setAt, &inv.NextDeviceSeq); err != nil {
			return fmt.Errorf("inventory: lock baseline: %w", err)
		}
		inv.Baseline = setAt.Unix() > 0

		macs, err := r.macs(ctx, tenantID)
		if err != nil {
			return err
		}
		inv.MACs = macs
		if w, err := r.openWindow(ctx, tenantID); err != nil {
			return err
		} else if w != nil {
			inv.OpenWindow = w
		}

		changes, err := decide(inv)
		if err != nil {
			return err
		}
		out = changes
		return r.apply(ctx, tenantID, changes)
	})
	if err != nil {
		return domain.Changes{}, err
	}
	return out, nil
}

// apply writes what the diff decided.
func (r *Repo) apply(ctx context.Context, tenantID string, ch domain.Changes) error {
	db := r.db(ctx)
	for _, d := range ch.Devices {
		if _, err := db.Exec(ctx,
			`INSERT INTO hub_inventory_devices (tenant_id, device_id, first_seen, last_seen)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (tenant_id, device_id) DO UPDATE SET last_seen = EXCLUDED.last_seen`,
			tenantID, d.ID, d.FirstSeen, d.LastSeen); err != nil {
			return fmt.Errorf("inventory: device: %w", err)
		}
	}
	for _, m := range ch.MACs {
		if _, err := db.Exec(ctx,
			`INSERT INTO hub_inventory_macs (tenant_id, mac, device_id, state, hostname, ip, ssid, last_obs, first_seen, last_seen)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			 ON CONFLICT (tenant_id, mac) DO UPDATE SET
			   device_id = EXCLUDED.device_id, state = EXCLUDED.state,
			   hostname = EXCLUDED.hostname, ip = EXCLUDED.ip, ssid = EXCLUDED.ssid,
			   last_obs = EXCLUDED.last_obs, last_seen = EXCLUDED.last_seen`,
			tenantID, m.MAC, m.DeviceID, string(m.State), m.Hostname, m.IP, m.SSID,
			[]byte(m.LastObs), m.FirstSeen, m.LastSeen); err != nil {
			return fmt.Errorf("inventory: address: %w", err)
		}
		// A device is as recent as its most recent address.
		if _, err := db.Exec(ctx,
			`UPDATE hub_inventory_devices SET last_seen = GREATEST(last_seen, $3)
			 WHERE tenant_id = $1 AND device_id = $2`, tenantID, m.DeviceID, m.LastSeen); err != nil {
			return fmt.Errorf("inventory: device last seen: %w", err)
		}
	}
	if w := ch.Window; w != nil {
		if err := r.saveWindow(ctx, tenantID, *w); err != nil {
			return err
		}
	}
	if len(ch.Devices) > 0 || ch.SetBaseline {
		// device_seq only ever moves forward, and the baseline is stamped on
		// the first snapshot so the next one has something to compare with.
		if _, err := db.Exec(ctx,
			`UPDATE hub_inventory_baseline
			 SET device_seq = GREATEST(device_seq, $2),
			     set_at = CASE WHEN set_at = to_timestamp(0) THEN now() ELSE set_at END
			 WHERE tenant_id = $1`, tenantID, lastDeviceSeq(ch)); err != nil {
			return fmt.Errorf("inventory: device sequence: %w", err)
		}
	}
	return nil
}

// lastDeviceSeq is the number of the newest device the diff created.
func lastDeviceSeq(ch domain.Changes) int {
	seq := 0
	for _, d := range ch.Devices {
		if n := domain.DeviceSeq(d.ID); n > seq {
			seq = n
		}
	}
	return seq
}

func (r *Repo) macs(ctx context.Context, tenantID string) ([]domain.MACRecord, error) {
	rows, err := r.db(ctx).Query(ctx,
		`SELECT mac, device_id, state, hostname, ip, ssid, COALESCE(last_obs, 'null'::jsonb), first_seen, last_seen
		 FROM hub_inventory_macs WHERE tenant_id = $1 ORDER BY mac`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("inventory: addresses: %w", err)
	}
	defer rows.Close()
	var out []domain.MACRecord
	for rows.Next() {
		var (
			m     domain.MACRecord
			state string
			obs   []byte
		)
		if err := rows.Scan(&m.MAC, &m.DeviceID, &state, &m.Hostname, &m.IP, &m.SSID, &obs, &m.FirstSeen, &m.LastSeen); err != nil {
			return nil, fmt.Errorf("inventory: addresses scan: %w", err)
		}
		m.TenantID, m.State, m.LastObs = tenantID, domain.State(state), json.RawMessage(obs)
		out = append(out, m)
	}
	return out, rows.Err()
}

// openWindow returns the household's window still collecting arrivals.
func (r *Repo) openWindow(ctx context.Context, tenantID string) (*domain.Window, error) {
	w := domain.Window{TenantID: tenantID}
	var items []byte
	err := r.db(ctx).QueryRow(ctx,
		`SELECT id, opened_at, closes_at, items FROM hub_inventory_windows
		 WHERE tenant_id = $1 AND announced_at IS NULL`, tenantID).
		Scan(&w.ID, &w.OpenedAt, &w.ClosesAt, &items)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inventory: open window: %w", err)
	}
	if err := json.Unmarshal(items, &w.Items); err != nil {
		return nil, fmt.Errorf("inventory: window items: %w", err)
	}
	return &w, nil
}

// saveWindow opens the household's window or extends it with new arrivals.
func (r *Repo) saveWindow(ctx context.Context, tenantID string, w domain.Window) error {
	items, err := json.Marshal(w.Items)
	if err != nil {
		return fmt.Errorf("inventory: encode window items: %w", err)
	}
	if w.ID == "" {
		w.ID = uuid.NewString()
		if _, err := r.db(ctx).Exec(ctx,
			`INSERT INTO hub_inventory_windows (id, tenant_id, opened_at, closes_at, items)
			 VALUES ($1, $2, $3, $4, $5)`,
			w.ID, tenantID, w.OpenedAt, w.ClosesAt, items); err != nil {
			return fmt.Errorf("inventory: open window: %w", err)
		}
		return nil
	}
	if _, err := r.db(ctx).Exec(ctx,
		`UPDATE hub_inventory_windows SET items = $3
		 WHERE id = $1 AND tenant_id = $2 AND announced_at IS NULL`, w.ID, tenantID, items); err != nil {
		return fmt.Errorf("inventory: extend window: %w", err)
	}
	return nil
}

// DueWindows returns the windows whose time is up and that nobody announced.
func (r *Repo) DueWindows(ctx context.Context, now time.Time, limit int) ([]domain.Window, error) {
	rows, err := r.db(ctx).Query(ctx,
		`SELECT id, tenant_id, opened_at, closes_at, items FROM hub_inventory_windows
		 WHERE announced_at IS NULL AND closes_at <= $1 ORDER BY closes_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("inventory: due windows: %w", err)
	}
	defer rows.Close()
	var out []domain.Window
	for rows.Next() {
		var (
			w     domain.Window
			items []byte
		)
		if err := rows.Scan(&w.ID, &w.TenantID, &w.OpenedAt, &w.ClosesAt, &items); err != nil {
			return nil, fmt.Errorf("inventory: due windows scan: %w", err)
		}
		if err := json.Unmarshal(items, &w.Items); err != nil {
			return nil, fmt.Errorf("inventory: window items: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// CloseWindow marks a window announced and calls announce in the same
// transaction.
//
// The UPDATE claims the window first: with several hub instances, the one
// whose UPDATE finds no row leaves the announcement to whoever won, and the
// household is told once.
func (r *Repo) CloseWindow(ctx context.Context, w domain.Window, now time.Time, announce func(context.Context, domain.Window) error) error {
	return txctx.ExecInTx(ctx, r.pool, func(ctx context.Context) error {
		tag, err := r.db(ctx).Exec(ctx,
			`UPDATE hub_inventory_windows SET announced_at = $2
			 WHERE id = $1 AND announced_at IS NULL`, w.ID, now)
		if err != nil {
			return fmt.Errorf("inventory: close window: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil // another instance announced it
		}
		return announce(ctx, w)
	})
}

// Inventory lists a household's devices and addresses.
func (r *Repo) Inventory(ctx context.Context, tenantID string) ([]domain.Device, []domain.MACRecord, error) {
	rows, err := r.db(ctx).Query(ctx,
		`SELECT device_id, first_seen, last_seen FROM hub_inventory_devices
		 WHERE tenant_id = $1 ORDER BY device_id`, tenantID)
	if err != nil {
		return nil, nil, fmt.Errorf("inventory: devices: %w", err)
	}
	defer rows.Close()
	var devices []domain.Device
	for rows.Next() {
		d := domain.Device{TenantID: tenantID}
		if err := rows.Scan(&d.ID, &d.FirstSeen, &d.LastSeen); err != nil {
			return nil, nil, fmt.Errorf("inventory: devices scan: %w", err)
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	macs, err := r.macs(ctx, tenantID)
	if err != nil {
		return nil, nil, err
	}
	return devices, macs, nil
}

// Window returns one window of a household.
func (r *Repo) Window(ctx context.Context, tenantID, windowID string) (domain.Window, error) {
	w := domain.Window{TenantID: tenantID}
	var items []byte
	err := r.db(ctx).QueryRow(ctx,
		`SELECT id, opened_at, closes_at, items, announced_at FROM hub_inventory_windows
		 WHERE tenant_id = $1 AND id = $2`, tenantID, windowID).
		Scan(&w.ID, &w.OpenedAt, &w.ClosesAt, &items, &w.ClosedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Window{}, domain.ErrNoWindow
	}
	if err != nil {
		return domain.Window{}, fmt.Errorf("inventory: window: %w", err)
	}
	if err := json.Unmarshal(items, &w.Items); err != nil {
		return domain.Window{}, fmt.Errorf("inventory: window items: %w", err)
	}
	return w, nil
}
