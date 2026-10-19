// Package usecase holds the inventory's two jobs: absorbing a snapshot a
// relay signed, and announcing the windows of new devices it opened.
package usecase

import (
	"encoding/json"
	"sort"
	"time"

	coredomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/inventory/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// AlertWindow is how long new arrivals are grouped into one alert (RFC-005).
const AlertWindow = 2 * time.Minute

// Poll is one signed observations snapshot, as the diff needs it.
type Poll struct {
	TenantID string
	AgentID  string
	// AsOf is the moment the relay polled the box, as it signed it.
	AsOf time.Time
	// SHA256 identifies the signed snapshot this poll came from, so an alert
	// can point at evidence the app can verify.
	SHA256 string
	// RelayMAC is the device carrying the relay. It never raises an alert:
	// the relay is a device of the household by definition, and a relay that
	// re-paired must not look like an intruder.
	RelayMAC string
	Degraded bool
	Hosts    []port.ObservationView
}

// Diff decides what one poll changes in the household's inventory.
//
// It is a pure function of the stored inventory and the poll, which is what
// makes every rule below testable without a database: the baseline, the MAC
// that rotated, and the grouping of two arrivals into one alert.
func Diff(inv domain.Snapshot, p Poll, now time.Time, rules coredomain.RecognitionRules, window time.Duration) domain.Changes {
	if window <= 0 {
		window = AlertWindow
	}
	ch := domain.Changes{SetBaseline: !inv.Baseline}

	// What the poll says, by normalised address. A snapshot that repeats an
	// address keeps the first view of it: two rows for one station are the
	// box's own duplicate, not two devices.
	seen := make(map[string]port.ObservationView, len(p.Hosts))
	order := make([]string, 0, len(p.Hosts))
	for _, h := range p.Hosts {
		mac, err := coredomain.ParseMAC(h.MAC)
		if err != nil {
			continue // a station without a usable address cannot be reasoned about
		}
		key := domain.NormaliseMAC(mac.String())
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = h
		order = append(order, key)
	}
	sort.Strings(order) // a stable order means stable device numbering

	// 1. Refresh what we already know. This happens before recognition runs,
	// so a known MAC seen in this same poll can contradict an unknown one
	// (two addresses associated together are two devices).
	known := make(map[string]domain.MACRecord, len(inv.MACs))
	for _, rec := range inv.MACs {
		known[rec.MAC] = rec
	}
	for _, key := range order {
		rec, ok := known[key]
		if !ok {
			continue
		}
		rec = refresh(rec, seen[key], p)
		known[key] = rec
		ch.MACs = append(ch.MACs, rec)
		ch.Touched++
	}

	// 2. Recognise the addresses we have never seen.
	devices := knownDevices(known)
	shared := sharedNames(known, seen)
	seq := inv.NextDeviceSeq
	win := openWindow(inv.OpenWindow, now, window)
	win.TenantID = p.TenantID

	for _, key := range order {
		if _, ok := known[key]; ok {
			continue
		}
		obs, err := seen[key].Observation()
		if err != nil {
			continue
		}
		rec := domain.MACRecord{
			TenantID: p.TenantID, MAC: key, State: domain.StateAssociated,
			Hostname: seen[key].Hostname, IP: seen[key].IPv4, SSID: seen[key].SSID,
			FirstSeen: pollTime(seen[key], p), LastSeen: pollTime(seen[key], p),
		}
		rec.LastObs = mustJSON(seen[key])

		decision := coredomain.Recognise([]coredomain.Observation{obs}, devices, shared, rules)
		switch decision.Outcome {
		case coredomain.Associated:
			rec.DeviceID = string(decision.Device)
		case coredomain.Proposed:
			// A phone that came back under a rotated MAC: recorded against
			// the device it probably belongs to, for the owner to confirm,
			// and silent (RFC-005 H2 as built, departure 1).
			rec.DeviceID = string(decision.Candidates[0].Device)
			rec.State = domain.StateProposed
		default:
			seq++
			id := string(coredomain.FormatDeviceID(seq))
			rec.DeviceID = id
			ch.Devices = append(ch.Devices, domain.Device{
				TenantID: p.TenantID, ID: id, FirstSeen: rec.FirstSeen, LastSeen: rec.LastSeen,
			})
			// A first snapshot is the household as it already is, and the
			// relay's own device is not news either.
			if inv.Baseline && !sameMAC(key, p.RelayMAC) {
				win.Items = append(win.Items, domain.WindowItem{
					No: len(win.Items) + 1, MAC: key, DeviceID: id,
					Hostname: rec.Hostname, IP: rec.IP, SSID: rec.SSID,
					SnapshotSHA256: p.SHA256, AsOf: p.AsOf,
				})
			}
		}
		known[key] = rec
		devices = knownDevices(known)
		ch.MACs = append(ch.MACs, rec)
	}

	if len(win.Items) > 0 {
		ch.Window = &win
	}
	return ch
}

// refresh updates a known address from this poll, keeping what the inventory
// decided about it: a proposal stays a proposal until the owner answers.
func refresh(rec domain.MACRecord, v port.ObservationView, p Poll) domain.MACRecord {
	at := pollTime(v, p)
	rec.LastSeen = at
	if rec.FirstSeen.IsZero() {
		rec.FirstSeen = at
	}
	if v.Hostname != "" {
		rec.Hostname = v.Hostname
	}
	if v.IPv4 != "" {
		rec.IP = v.IPv4
	}
	if v.SSID != "" {
		rec.SSID = v.SSID
	}
	rec.LastObs = mustJSON(v)
	return rec
}

// openWindow continues the household's window when it is still collecting,
// and opens a fresh one otherwise.
func openWindow(cur *domain.Window, now time.Time, window time.Duration) domain.Window {
	if cur != nil && cur.Open(now) {
		return *cur
	}
	return domain.Window{OpenedAt: now, ClosesAt: now.Add(window)}
}

// knownDevices groups the household's addresses into the devices recognition
// compares against. Proposed addresses count: a phone whose MAC rotates every
// time would otherwise look new again at every rotation.
func knownDevices(known map[string]domain.MACRecord) []coredomain.KnownDevice {
	byDevice := map[string][]coredomain.Observation{}
	for _, rec := range known {
		if rec.DeviceID == "" || len(rec.LastObs) == 0 {
			continue
		}
		var v port.ObservationView
		if json.Unmarshal(rec.LastObs, &v) != nil {
			continue
		}
		o, err := v.Observation()
		if err != nil {
			continue
		}
		byDevice[rec.DeviceID] = append(byDevice[rec.DeviceID], o)
	}
	ids := make([]string, 0, len(byDevice))
	for id := range byDevice {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]coredomain.KnownDevice, 0, len(ids))
	for _, id := range ids {
		out = append(out, coredomain.KnownDevice{ID: coredomain.DeviceID(id), Observations: byDevice[id]})
	}
	return out
}

// sharedNames are the host names that prove nothing, because more than one
// device answers to them: a shop's default name, or a family where two
// phones are called the same thing.
//
// A name counts as shared when two addresses carry it in the same poll (they
// are online together, so they are two devices), or when the inventory
// already has it on addresses of two different devices. It must never be
// counted as shared merely because a new address repeats a known name — that
// is exactly the evidence recognition exists to weigh.
func sharedNames(known map[string]domain.MACRecord, seen map[string]port.ObservationView) map[string]bool {
	out := map[string]bool{}

	// Online together in this poll.
	macsNow := map[string]map[string]bool{}
	for mac, v := range seen {
		key, ok := coredomain.IdentityKey(v.Hostname)
		if !ok {
			continue
		}
		if macsNow[key] == nil {
			macsNow[key] = map[string]bool{}
		}
		macsNow[key][mac] = true
	}
	for name, macs := range macsNow {
		if len(macs) > 1 {
			out[name] = true
		}
	}

	// Already on two different devices of the inventory.
	devicesByName := map[string]map[string]bool{}
	for _, rec := range known {
		key, ok := coredomain.IdentityKey(rec.Hostname)
		if !ok || rec.DeviceID == "" {
			continue
		}
		if devicesByName[key] == nil {
			devicesByName[key] = map[string]bool{}
		}
		devicesByName[key][rec.DeviceID] = true
	}
	for name, devices := range devicesByName {
		if len(devices) > 1 {
			out[name] = true
		}
	}
	return out
}

// pollTime is when the observation was made: the station's own timestamp when
// the relay reported one, else the moment it signed the snapshot.
func pollTime(v port.ObservationView, p Poll) time.Time {
	if !v.At.IsZero() {
		return v.At.UTC()
	}
	return p.AsOf.UTC()
}

func sameMAC(a, b string) bool {
	return b != "" && domain.NormaliseMAC(a) == domain.NormaliseMAC(b)
}

// mustJSON encodes an observation for storage. The value comes from a decoded
// snapshot, so it cannot fail to re-encode; an empty body is still safer than
// a panic in a hub serving other households.
func mustJSON(v port.ObservationView) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}
