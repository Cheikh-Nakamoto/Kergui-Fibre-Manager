package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// DeviceRepo persists the device inventory.
type DeviceRepo struct {
	db *sql.DB
	// now stamps a rename (#95: the labels carry when they were set).
	now func() time.Time
}

var (
	_ port.DeviceRepository = (*DeviceRepo)(nil)
	_ port.LabelRepository  = (*DeviceRepo)(nil)
)

// NewDeviceRepo wires the repository.
func NewDeviceRepo(db *DB) *DeviceRepo { return &DeviceRepo{db: db.SQL(), now: time.Now} }

// WithClock sets the clock a rename is stamped with (tests).
func (r *DeviceRepo) WithClock(now func() time.Time) *DeviceRepo {
	r.now = now
	return r
}

// deviceColumns are what scanDevice reads, in its order.
const deviceColumns = `id, router_id, mac, ip, hostname, custom_name, vendor, ssid, first_seen, last_seen, connected, blocked, notes, label_at`

// Upsert inserts or updates a device, returning created=true on first sight.
func (r *DeviceRepo) Upsert(ctx context.Context, d domain.Device) (bool, error) {
	_, existed, err := r.FindByMAC(ctx, d.RouterID, d.MAC)
	if err != nil {
		return false, err
	}
	_, err = r.db.ExecContext(ctx, `
INSERT INTO devices (id, router_id, mac, ip, hostname, custom_name, vendor, ssid, first_seen, last_seen, connected, blocked, notes)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(router_id, mac) DO UPDATE SET
  ip=excluded.ip, hostname=excluded.hostname, custom_name=excluded.custom_name,
  vendor=excluded.vendor, ssid=excluded.ssid, last_seen=excluded.last_seen,
  connected=excluded.connected, blocked=excluded.blocked, notes=excluded.notes`,
		d.ID, d.RouterID, d.MAC.String(), d.IP.String(), d.Hostname, d.CustomName, d.Vendor, d.SSID,
		unixOrZero(d.FirstSeen), unixOrZero(d.LastSeen), boolToInt(d.Connected), boolToInt(d.Blocked), d.Notes)
	if err != nil {
		return false, err
	}
	return !existed, nil
}

// FindByMAC looks up a single device.
func (r *DeviceRepo) FindByMAC(ctx context.Context, routerID string, mac domain.MAC) (domain.Device, bool, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT `+deviceColumns+`
FROM devices WHERE router_id=? AND mac=?`, routerID, mac.String())
	d, err := scanDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Device{}, false, nil
	}
	if err != nil {
		return domain.Device{}, false, err
	}
	return d, true, nil
}

// SetCustomName updates the user's label for a device, and stamps it (#95).
// The stamp is never earlier than the one it replaces: a rename made here
// is the latest the household made on this phone or PC, even when a label
// from a fast clock elsewhere came in before it, so it is the one that
// travels to the relay and wins there.
func (r *DeviceRepo) SetCustomName(ctx context.Context, routerID string, mac domain.MAC, name string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE devices SET custom_name=?, label_at=MAX(label_at+1, ?) WHERE router_id=? AND mac=?`,
		name, r.now().UnixMilli(), routerID, mac.String())
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrDeviceNotFound
	}
	return nil
}

// ListByRouter lists all devices for a router, ordered by MAC.
func (r *DeviceRepo) ListByRouter(ctx context.Context, routerID string) ([]domain.Device, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT `+deviceColumns+`
FROM devices WHERE router_id=? ORDER BY mac`, routerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanDevice(s scanner) (domain.Device, error) {
	var (
		id, routerID, mac, ip, hostname, customName, vendor, ssid, notes string
		firstSeen, lastSeen, labelAt                                     int64
		connected, blocked                                               int
	)
	if err := s.Scan(&id, &routerID, &mac, &ip, &hostname, &customName, &vendor, &ssid, &firstSeen, &lastSeen, &connected, &blocked, &notes, &labelAt); err != nil {
		return domain.Device{}, err
	}
	m, err := domain.ParseMAC(mac)
	if err != nil {
		return domain.Device{}, err
	}
	ipv, _ := domain.ParseIP(ip)
	return domain.Device{
		ID:         id,
		RouterID:   routerID,
		MAC:        m,
		IP:         ipv,
		Hostname:   hostname,
		CustomName: customName,
		Vendor:     vendor,
		SSID:       ssid,
		FirstSeen:  timeOrZero(firstSeen),
		LastSeen:   timeOrZero(lastSeen),
		Connected:  connected != 0,
		Blocked:    blocked != 0,
		Notes:      notes,
		LabelAt:    milliOrZero(labelAt),
	}, nil
}

func milliOrZero(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// Labels lists the devices of a router the household named, annotated or
// labelled at some point (#95), by MAC.
func (r *DeviceRepo) Labels(ctx context.Context, routerID string) ([]domain.Label, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT mac, custom_name, notes, label_at FROM devices
WHERE router_id=? AND (custom_name <> '' OR notes <> '' OR label_at > 0) ORDER BY mac`, routerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Label{}
	for rows.Next() {
		var mac, name, notes string
		var at int64
		if err := rows.Scan(&mac, &name, &notes, &at); err != nil {
			return nil, err
		}
		m, err := domain.ParseMAC(mac)
		if err != nil {
			continue
		}
		out = append(out, domain.Label{MAC: m, Name: &name, Notes: &notes, At: milliOrZero(at)})
	}
	return out, rows.Err()
}

// ApplyLabels applies labels made elsewhere (#95), in one transaction: last
// write wins. A label replaces the stored one only when it is strictly later
// (millisecond stamps): a tie keeps what is here, so applying the same label
// twice, or both ways, changes nothing. A device not known here is added,
// offline, as a backup import does: its name is there the day it joins. It
// returns the MACs whose label changed.
func (r *DeviceRepo) ApplyLabels(ctx context.Context, routerID string, labels []domain.Label) ([]domain.MAC, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var changed []domain.MAC
	for _, l := range labels {
		if l.MAC.IsZero() || l.At.IsZero() {
			continue
		}
		at := l.At.UnixMilli()
		var name, notes string
		var stored int64
		err := tx.QueryRowContext(ctx, `SELECT custom_name, notes, label_at FROM devices WHERE router_id=? AND mac=?`,
			routerID, l.MAC.String()).Scan(&name, &notes, &stored)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if l.Name != nil {
				name = *l.Name
			}
			if l.Notes != nil {
				notes = *l.Notes
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO devices (id, router_id, mac, ip, hostname, custom_name, vendor, ssid, first_seen, last_seen, connected, blocked, notes, label_at)
VALUES (?,?,?,'','',?,'','',0,0,0,0,?,?)`, routerID+"|"+l.MAC.String(), routerID, l.MAC.String(), name, notes, at); err != nil {
				return nil, err
			}
			changed = append(changed, l.MAC)
			continue
		case err != nil:
			return nil, err
		}
		if at <= stored {
			continue // what is here is as recent or later
		}
		if l.Name != nil {
			name = *l.Name
		}
		if l.Notes != nil {
			notes = *l.Notes
		}
		if _, err := tx.ExecContext(ctx, `UPDATE devices SET custom_name=?, notes=?, label_at=? WHERE router_id=? AND mac=?`,
			name, notes, at, routerID, l.MAC.String()); err != nil {
			return nil, err
		}
		changed = append(changed, l.MAC)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return changed, nil
}
