package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// DeviceRepo persists the device inventory.
type DeviceRepo struct{ db *sql.DB }

var _ port.DeviceRepository = (*DeviceRepo)(nil)

// NewDeviceRepo wires the repository.
func NewDeviceRepo(db *DB) *DeviceRepo { return &DeviceRepo{db: db.SQL()} }

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
SELECT id, router_id, mac, ip, hostname, custom_name, vendor, ssid, first_seen, last_seen, connected, blocked, notes
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

// ListByRouter lists all devices for a router, ordered by MAC.
func (r *DeviceRepo) ListByRouter(ctx context.Context, routerID string) ([]domain.Device, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, router_id, mac, ip, hostname, custom_name, vendor, ssid, first_seen, last_seen, connected, blocked, notes
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
		firstSeen, lastSeen                                              int64
		connected, blocked                                               int
	)
	if err := s.Scan(&id, &routerID, &mac, &ip, &hostname, &customName, &vendor, &ssid, &firstSeen, &lastSeen, &connected, &blocked, &notes); err != nil {
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
	}, nil
}
