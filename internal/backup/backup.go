// Package backup is a Kergui backup, independent of where it is kept (owner
// request, 2026-11-23): what a household told Kergui about its devices —
// names, notes, when each was first seen — and the monthly consumption, as a
// zip of two CSV files that open in a spreadsheet:
//
//	appareils.csv  mac, nom, notes, premiere_vue (RFC 3339), fabricant, hote
//	conso.csv      mac, mois (YYYY-MM), octets
//
// One format for `kergui export`, the app's « Importer une sauvegarde » and
// the hub's copy (issue #70). The box's password and the vault never go in
// it: Bytes writes these columns and nothing else, whatever an archive read
// carried. Merge is the import's rules, archive to archive, so the hub can
// apply them when two sources send.
package backup

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// The two entries of the archive.
const (
	DevicesFile = "appareils.csv"
	UsageFile   = "conso.csv"
)

var (
	devicesHeader = []string{"mac", "nom", "notes", "premiere_vue", "fabricant", "hote"}
	usageHeader   = []string{"mac", "mois", "octets"}
)

// maxEntry bounds what one CSV of the archive may unpack to.
const maxEntry = 8 << 20

// ErrBadBackup is an archive that is not a Kergui backup.
var ErrBadBackup = errors.New("not a Kergui backup (zip with appareils.csv and conso.csv)")

// Device is one row of appareils.csv. MAC is canonical, lower-case;
// FirstSeen is Unix seconds, 0 when unknown.
type Device struct {
	MAC       string
	Name      string
	Notes     string
	FirstSeen int64
	Vendor    string
	Hostname  string
}

// Usage is one row of conso.csv: a device's bytes over a month (YYYY-MM).
type Usage struct {
	MAC   string
	Month string
	Bytes int64
}

// Archive is a backup's content, rows in the order they came.
type Archive struct {
	Devices []Device
	Usage   []Usage
}

// Read parses an archive. Rows that cannot be applied (an address that is
// not a MAC, a month or a figure that does not parse) are skipped, as the
// import always did; an archive without the two files is ErrBadBackup.
// A spreadsheet's save (a BOM, « ; », the files in a folder) reads too.
func Read(archive []byte) (Archive, error) {
	devRows, useRows, err := readRows(archive)
	if err != nil {
		return Archive{}, err
	}
	var a Archive
	for _, r := range devRows {
		mac, ok := canonicalMAC(r[0])
		if !ok {
			continue
		}
		var first int64
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(r[3])); err == nil {
			first = t.Unix()
		}
		a.Devices = append(a.Devices, Device{
			MAC: mac, Name: strings.TrimSpace(r[1]), Notes: strings.TrimSpace(r[2]), FirstSeen: first,
			Vendor: strings.TrimSpace(r[4]), Hostname: strings.TrimSpace(r[5]),
		})
	}
	for _, r := range useRows {
		mac, ok := canonicalMAC(r[0])
		if !ok {
			continue
		}
		month := strings.TrimSpace(r[1])
		if _, err := time.Parse("2006-01", month); err != nil {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(r[2]), 10, 64)
		if err != nil || n < 0 {
			continue
		}
		a.Usage = append(a.Usage, Usage{MAC: mac, Month: month, Bytes: n})
	}
	return a, nil
}

func canonicalMAC(s string) (string, bool) {
	m, err := domain.ParseMAC(s)
	if err != nil {
		return "", false
	}
	return strings.ToLower(m.String()), true
}

// Bytes writes the archive: the two files, their columns only, rows sorted
// by MAC (then month), so the same content always gives the same bytes.
func (a Archive) Bytes() ([]byte, error) {
	devices := append([]Device(nil), a.Devices...)
	sort.SliceStable(devices, func(i, j int) bool { return devices[i].MAC < devices[j].MAC })
	usage := append([]Usage(nil), a.Usage...)
	sort.SliceStable(usage, func(i, j int) bool {
		if usage[i].MAC != usage[j].MAC {
			return usage[i].MAC < usage[j].MAC
		}
		return usage[i].Month < usage[j].Month
	})

	devCSV := [][]string{devicesHeader}
	for _, d := range devices {
		first := ""
		if d.FirstSeen > 0 {
			first = time.Unix(d.FirstSeen, 0).UTC().Format(time.RFC3339)
		}
		devCSV = append(devCSV, []string{d.MAC, d.Name, d.Notes, first, d.Vendor, d.Hostname})
	}
	useCSV := [][]string{usageHeader}
	for _, u := range usage {
		useCSV = append(useCSV, []string{u.MAC, u.Month, strconv.FormatInt(u.Bytes, 10)})
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct {
		name string
		rows [][]string
	}{{DevicesFile, devCSV}, {UsageFile, useCSV}} {
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

// Merge applies incoming onto stored with the import's rules (owner
// decision, 2026-11-23): incoming's names and notes win, an empty one leaves
// the stored; a device either side knows is kept; a vendor or host name
// keeps the first known; the earliest first sighting; a month keeps the
// larger figure, since both sides measured the same box. Rows are applied in
// order, so a device listed twice folds as a sequential import does. The
// result has one row per device and per month, sorted.
func Merge(stored, incoming Archive) Archive {
	devices := map[string]Device{}
	var order []string
	apply := func(d Device) {
		cur, ok := devices[d.MAC]
		if !ok {
			devices[d.MAC] = d
			order = append(order, d.MAC)
			return
		}
		if d.Name != "" {
			cur.Name = d.Name
		}
		if d.Notes != "" {
			cur.Notes = d.Notes
		}
		if cur.Hostname == "" {
			cur.Hostname = d.Hostname
		}
		if cur.Vendor == "" {
			cur.Vendor = d.Vendor
		}
		if d.FirstSeen > 0 && (cur.FirstSeen == 0 || d.FirstSeen < cur.FirstSeen) {
			cur.FirstSeen = d.FirstSeen
		}
		devices[d.MAC] = cur
	}
	for _, d := range stored.Devices {
		apply(d)
	}
	for _, d := range incoming.Devices {
		apply(d)
	}

	usage := map[[2]string]int64{}
	for _, list := range [][]Usage{stored.Usage, incoming.Usage} {
		for _, u := range list {
			k := [2]string{u.MAC, u.Month}
			if n, ok := usage[k]; !ok || u.Bytes > n {
				usage[k] = u.Bytes
			}
		}
	}

	var out Archive
	sort.Strings(order)
	for _, m := range order {
		out.Devices = append(out.Devices, devices[m])
	}
	for k, n := range usage {
		out.Usage = append(out.Usage, Usage{MAC: k[0], Month: k[1], Bytes: n})
	}
	sort.Slice(out.Usage, func(i, j int) bool {
		if out.Usage[i].MAC != out.Usage[j].MAC {
			return out.Usage[i].MAC < out.Usage[j].MAC
		}
		return out.Usage[i].Month < out.Usage[j].Month
	})
	return out
}

func readRows(archive []byte) (devices, usage [][]string, err error) {
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
			raw, err := io.ReadAll(io.LimitReader(rc, maxEntry))
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
	if devices, err = read(DevicesFile, devicesHeader); err != nil {
		return nil, nil, err
	}
	if usage, err = read(UsageFile, usageHeader); err != nil {
		return nil, nil, err
	}
	return devices, usage, nil
}
