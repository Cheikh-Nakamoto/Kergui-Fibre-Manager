package sqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/backup"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// A Kergui backup (owner request, 2026-11-23): what a household told Kergui
// about its devices — names, notes, when each was first seen — and the
// monthly consumption, as a zip of two CSV files that open in a spreadsheet:
//
//	appareils.csv  mac, nom, notes, premiere_vue (RFC 3339), fabricant, hote
//	conso.csv      mac, mois (YYYY-MM), octets
//
// The box's password and the vault never go in it. The format, its reading
// and the import's merge rules live in internal/backup, shared with the hub
// (issue #88). The export reads only the columns every schema version has,
// so a database from an older kergui works.

// BackupFile names the two entries of the archive.
const (
	BackupDevicesFile = backup.DevicesFile
	BackupUsageFile   = backup.UsageFile
)

// ErrBadBackup is an archive that is not a Kergui backup.
var ErrBadBackup = backup.ErrBadBackup

// ImportResult says what an import changed.
type ImportResult struct {
	Devices int `json:"devices"` // devices created or updated
	Named   int `json:"named"`   // devices given a name from the backup
	Usage   int `json:"usage"`   // monthly figures written
	// NamedMACs are the devices the backup gave a name, canonical lower-case.
	NamedMACs []string `json:"-"`
}

type backupDevice struct {
	mac       string
	name      string
	notes     string
	firstSeen int64
	vendor    string
	hostname  string
	lastSeen  int64
}

// ExportBackup writes every router's devices and monthly consumption as a
// backup archive. A device known under several routers appears once: the
// most recently seen record, with its earliest first sighting.
func ExportBackup(ctx context.Context, db *DB) ([]byte, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT mac, custom_name, notes, first_seen, vendor, hostname, last_seen FROM devices`)
	if err != nil {
		return nil, fmt.Errorf("export devices: %w", err)
	}
	byMAC := map[string]backupDevice{}
	for rows.Next() {
		var d backupDevice
		if err := rows.Scan(&d.mac, &d.name, &d.notes, &d.firstSeen, &d.vendor, &d.hostname, &d.lastSeen); err != nil {
			rows.Close()
			return nil, err
		}
		m, err := domain.ParseMAC(d.mac)
		if err != nil {
			continue
		}
		d.mac = strings.ToLower(m.String())
		if prev, ok := byMAC[d.mac]; ok {
			if prev.lastSeen > d.lastSeen {
				prev, d = d, prev
			}
			if prev.firstSeen > 0 && (d.firstSeen == 0 || prev.firstSeen < d.firstSeen) {
				d.firstSeen = prev.firstSeen
			}
			if d.name == "" {
				d.name = prev.name
			}
			if d.notes == "" {
				d.notes = prev.notes
			}
		}
		byMAC[d.mac] = d
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	urows, err := db.sql.QueryContext(ctx, `SELECT mac, period, bytes FROM usage`)
	if err != nil {
		return nil, fmt.Errorf("export usage: %w", err)
	}
	usage := map[[2]string]int64{}
	for urows.Next() {
		var mac, period string
		var n int64
		if err := urows.Scan(&mac, &period, &n); err != nil {
			urows.Close()
			return nil, err
		}
		m, err := domain.ParseMAC(mac)
		if err != nil {
			continue
		}
		k := [2]string{strings.ToLower(m.String()), period}
		if n > usage[k] {
			usage[k] = n
		}
	}
	urows.Close()
	if err := urows.Err(); err != nil {
		return nil, err
	}

	var out backup.Archive
	for _, d := range byMAC {
		out.Devices = append(out.Devices, backup.Device{
			MAC: d.mac, Name: d.name, Notes: d.notes, FirstSeen: d.firstSeen, Vendor: d.vendor, Hostname: d.hostname,
		})
	}
	for k, n := range usage {
		out.Usage = append(out.Usage, backup.Usage{MAC: k[0], Month: k[1], Bytes: n})
	}
	return out.Bytes()
}

// ImportBackup applies a backup archive to one router (the box the app is
// on), in one transaction. The backup wins (owner decision, 2026-11-23): a
// name or notes it carries replace the ones here; an empty one leaves them.
// A device not known here is added, offline. First sightings keep the
// earliest; a month's consumption keeps the larger figure, since both sides
// measured the same box.
func ImportBackup(ctx context.Context, db *DB, routerID string, archive []byte) (ImportResult, error) {
	in, err := backup.Read(archive)
	if err != nil {
		return ImportResult{}, err
	}
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var res ImportResult
	for _, d := range in.Devices {
		m, err := domain.ParseMAC(d.MAC)
		if err != nil {
			continue
		}
		mac := m.String()
		if _, err := tx.ExecContext(ctx, `
INSERT INTO devices (id, router_id, mac, ip, hostname, custom_name, vendor, ssid, first_seen, last_seen, connected, blocked, notes)
VALUES (?,?,?,'',?,?,?,'',?,0,0,0,?)
ON CONFLICT(router_id, mac) DO UPDATE SET
  custom_name = CASE WHEN excluded.custom_name <> '' THEN excluded.custom_name ELSE devices.custom_name END,
  notes       = CASE WHEN excluded.notes <> '' THEN excluded.notes ELSE devices.notes END,
  hostname    = CASE WHEN devices.hostname = '' THEN excluded.hostname ELSE devices.hostname END,
  vendor      = CASE WHEN devices.vendor = '' THEN excluded.vendor ELSE devices.vendor END,
  first_seen  = CASE WHEN excluded.first_seen > 0 AND (devices.first_seen = 0 OR excluded.first_seen < devices.first_seen)
                     THEN excluded.first_seen ELSE devices.first_seen END`,
			routerID+"|"+mac, routerID, mac, d.Hostname, d.Name, d.Vendor, d.FirstSeen, d.Notes); err != nil {
			return ImportResult{}, fmt.Errorf("import %s: %w", mac, err)
		}
		res.Devices++
		if d.Name != "" {
			res.Named++
			res.NamedMACs = append(res.NamedMACs, strings.ToLower(mac))
		}
	}
	for _, u := range in.Usage {
		m, err := domain.ParseMAC(u.MAC)
		if err != nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO usage (router_id, mac, period, bytes) VALUES (?,?,?,?)
ON CONFLICT(router_id, mac, period) DO UPDATE SET bytes = MAX(usage.bytes, excluded.bytes)`,
			routerID, m.String(), u.Month, u.Bytes); err != nil {
			return ImportResult{}, fmt.Errorf("import usage %s %s: %w", m, u.Month, err)
		}
		res.Usage++
	}
	if err := tx.Commit(); err != nil {
		return ImportResult{}, err
	}
	return res, nil
}
