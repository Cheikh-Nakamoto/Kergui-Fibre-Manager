package sqlite

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// A Kergui backup (owner request, 2026-11-23): what a household told Kergui
// about its devices — names, notes, when each was first seen — and the
// monthly consumption, as a zip of two CSV files that open in a spreadsheet:
//
//	appareils.csv  mac, nom, notes, premiere_vue (RFC 3339), fabricant, hote
//	conso.csv      mac, mois (YYYY-MM), octets
//
// The box's password and the vault never go in it. The export reads only the
// columns every schema version has, so a database from an older kergui works.

// BackupFile names the two entries of the archive.
const (
	BackupDevicesFile = "appareils.csv"
	BackupUsageFile   = "conso.csv"
)

var (
	backupDevicesHeader = []string{"mac", "nom", "notes", "premiere_vue", "fabricant", "hote"}
	backupUsageHeader   = []string{"mac", "mois", "octets"}
)

// ErrBadBackup is an archive that is not a Kergui backup.
var ErrBadBackup = errors.New("sqlite: not a Kergui backup (zip with appareils.csv and conso.csv)")

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

	macs := make([]string, 0, len(byMAC))
	for m := range byMAC {
		macs = append(macs, m)
	}
	sort.Strings(macs)
	devCSV := [][]string{backupDevicesHeader}
	for _, m := range macs {
		d := byMAC[m]
		first := ""
		if d.firstSeen > 0 {
			first = time.Unix(d.firstSeen, 0).UTC().Format(time.RFC3339)
		}
		devCSV = append(devCSV, []string{d.mac, d.name, d.notes, first, d.vendor, d.hostname})
	}
	keys := make([][2]string, 0, len(usage))
	for k := range usage {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	useCSV := [][]string{backupUsageHeader}
	for _, k := range keys {
		useCSV = append(useCSV, []string{k[0], k[1], strconv.FormatInt(usage[k], 10)})
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct {
		name string
		rows [][]string
	}{{BackupDevicesFile, devCSV}, {BackupUsageFile, useCSV}} {
		w, err := zw.Create(f.name)
		if err != nil {
			return nil, err
		}
		cw := csv.NewWriter(w)
		if err := cw.WriteAll(f.rows); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ImportBackup applies a backup archive to one router (the box the app is
// on), in one transaction. The backup wins (owner decision, 2026-11-23): a
// name or notes it carries replace the ones here; an empty one leaves them.
// A device not known here is added, offline. First sightings keep the
// earliest; a month's consumption keeps the larger figure, since both sides
// measured the same box.
func ImportBackup(ctx context.Context, db *DB, routerID string, archive []byte) (ImportResult, error) {
	devRows, useRows, err := readBackup(archive)
	if err != nil {
		return ImportResult{}, err
	}
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var res ImportResult
	for _, r := range devRows {
		m, err := domain.ParseMAC(r[0])
		if err != nil {
			continue
		}
		mac := m.String()
		name, notes := strings.TrimSpace(r[1]), strings.TrimSpace(r[2])
		var first int64
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(r[3])); err == nil {
			first = t.Unix()
		}
		vendor, host := strings.TrimSpace(r[4]), strings.TrimSpace(r[5])
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
			routerID+"|"+mac, routerID, mac, host, name, vendor, first, notes); err != nil {
			return ImportResult{}, fmt.Errorf("import %s: %w", mac, err)
		}
		res.Devices++
		if name != "" {
			res.Named++
			res.NamedMACs = append(res.NamedMACs, strings.ToLower(mac))
		}
	}
	for _, r := range useRows {
		m, err := domain.ParseMAC(r[0])
		if err != nil {
			continue
		}
		period := strings.TrimSpace(r[1])
		if _, err := time.Parse("2006-01", period); err != nil {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(r[2]), 10, 64)
		if err != nil || n < 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO usage (router_id, mac, period, bytes) VALUES (?,?,?,?)
ON CONFLICT(router_id, mac, period) DO UPDATE SET bytes = MAX(usage.bytes, excluded.bytes)`,
			routerID, m.String(), period, n); err != nil {
			return ImportResult{}, fmt.Errorf("import usage %s %s: %w", m, period, err)
		}
		res.Usage++
	}
	if err := tx.Commit(); err != nil {
		return ImportResult{}, err
	}
	return res, nil
}

const maxBackupEntry = 8 << 20

func readBackup(archive []byte) (devices, usage [][]string, err error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, nil, ErrBadBackup
	}
	read := func(name string, header []string) ([][]string, error) {
		for _, f := range zr.File {
			// A backup re-zipped by hand may sit in a folder.
			if f.Name != name && !strings.HasSuffix(f.Name, "/"+name) {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, ErrBadBackup
			}
			raw, err := io.ReadAll(io.LimitReader(rc, maxBackupEntry))
			rc.Close()
			if err != nil {
				return nil, ErrBadBackup
			}
			// A spreadsheet saves a BOM and may use « ; ».
			raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
			cr := csv.NewReader(bytes.NewReader(raw))
			if bytes.Count(bytes.SplitN(raw, []byte("\n"), 2)[0], []byte(";")) >= len(header)-1 {
				cr.Comma = ';'
			}
			cr.FieldsPerRecord = -1
			rows, err := cr.ReadAll()
			if err != nil || len(rows) == 0 || strings.ToLower(strings.TrimSpace(rows[0][0])) != header[0] {
				return nil, ErrBadBackup
			}
			out := make([][]string, 0, len(rows)-1)
			for _, r := range rows[1:] {
				for len(r) < len(header) {
					r = append(r, "")
				}
				out = append(out, r)
			}
			return out, nil
		}
		return nil, ErrBadBackup
	}
	if devices, err = read(BackupDevicesFile, backupDevicesHeader); err != nil {
		return nil, nil, err
	}
	if usage, err = read(BackupUsageFile, backupUsageHeader); err != nil {
		return nil, nil, err
	}
	return devices, usage, nil
}
