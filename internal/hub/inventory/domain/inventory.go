// Package domain holds the hub's reference inventory: which devices a
// household has, which MAC addresses belong to each, and the alert windows
// that group the arrivals of new ones.
//
// The hub owns this inventory because it is the only party that sees every
// snapshot over time (RFC-005, design rule 6). It still decides nothing on
// its own: every record here comes from a snapshot a relay signed.
package domain

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// State is how settled a MAC's binding is.
type State string

const (
	// StateAssociated: the MAC belongs to that device, because it was seen
	// under it or because the owner confirmed it.
	StateAssociated State = "associated"
	// StateProposed: recognition has corroborating evidence only (a phone
	// that came back under a rotated MAC with the same host name). The owner
	// confirms or rejects it; the hub never binds it alone, and it raises no
	// alert — alerting on a device the household already knows would teach
	// people to ignore the alerts.
	StateProposed State = "proposed"
)

// Errors the handlers map to status codes.
var (
	ErrNoInventory = errors.New("inventory: this household has no inventory yet")
	ErrNoWindow    = errors.New("inventory: no such alert window")
)

// Device is one device of the household's inventory.
type Device struct {
	TenantID  string    `json:"-"`
	ID        string    `json:"id"` // "DEVICE-000042"
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

// MACRecord is one address bound to, or proposed for, a device.
type MACRecord struct {
	TenantID string `json:"-"`
	// MAC is normalised (lowercase, colon-separated) by the domain's own
	// parser before it gets here.
	MAC       string          `json:"mac"`
	DeviceID  string          `json:"device_id"`
	State     State           `json:"state"`
	Hostname  string          `json:"hostname,omitempty"`
	IP        string          `json:"ip,omitempty"`
	SSID      string          `json:"ssid,omitempty"`
	LastObs   json.RawMessage `json:"-"` // the observation as the relay signed it
	FirstSeen time.Time       `json:"first_seen"`
	LastSeen  time.Time       `json:"last_seen"`
}

// Window groups the devices that showed up close together, so a household
// that plugs in three things gets one message instead of three (RFC-005: a
// 2-minute window).
type Window struct {
	ID       string
	TenantID string
	OpenedAt time.Time
	ClosesAt time.Time
	Items    []WindowItem
	ClosedAt *time.Time
}

// WindowItem is one new device in a window. No is what the owner answers in
// WhatsApp ("1", "2"), and it is assigned here so the number means the same
// thing in the message, in the reply and in the audit trail.
//
// SnapshotSHA256 and AsOf point at the signed snapshot the item came from:
// the app can ask the hub for that snapshot and check the relay's signature,
// so an alert the hub invented has nothing to show.
type WindowItem struct {
	No             int       `json:"no"`
	MAC            string    `json:"mac"`
	DeviceID       string    `json:"device_id"`
	Hostname       string    `json:"hostname,omitempty"`
	IP             string    `json:"ip,omitempty"`
	SSID           string    `json:"ssid,omitempty"`
	SnapshotSHA256 string    `json:"snapshot_sha256"`
	AsOf           time.Time `json:"as_of"`
}

// Open reports whether the window is still collecting arrivals at now.
func (w Window) Open(now time.Time) bool { return w.ClosedAt == nil && now.Before(w.ClosesAt) }

// Snapshot is the inventory state one ingest works from.
type Snapshot struct {
	// Baseline is true once the household's first snapshot has been
	// absorbed. Before that, everything on the Wi-Fi is "already there": a
	// household that has just paired a relay must not be told that each of
	// its own phones is a stranger.
	Baseline bool
	MACs     []MACRecord
	// NextDeviceSeq numbers the next device ("DEVICE-000042").
	NextDeviceSeq int
	// OpenWindow is the household's window still collecting arrivals, if any.
	OpenWindow *Window
}

// Changes is what one ingest decided. It is a value, so the diff stays a pure
// function of the snapshot plus the observations, and the repository is the
// only thing that writes.
type Changes struct {
	// Devices are the devices to create, each already referenced by a record
	// in MACs.
	Devices []Device
	// MACs are the records to insert or update.
	MACs []MACRecord
	// Window is the window to open or extend; nil when nothing new arrived.
	Window *Window
	// SetBaseline records that this household's inventory now has a
	// reference state.
	SetBaseline bool
	// Touched is how many known MACs this snapshot refreshed, for the log.
	Touched int
}

// NormaliseMAC is the inventory's key form for an address: lowercase, no
// separators removed (the domain's parser already canonicalises it), so two
// spellings of one address cannot become two devices.
func NormaliseMAC(mac string) string { return strings.ToLower(strings.TrimSpace(mac)) }

// EventDeviceFirstSeen is the outbox event one closed window produces. The
// alerts module subscribes to it; nothing else in the hub may invent one,
// because every item of a window points at a snapshot a relay signed.
const EventDeviceFirstSeen = "router.device_first_seen"

// DeviceSeq reads the number back out of a device id ("DEVICE-000042" → 42),
// and 0 for anything else. The sequence is what keeps two devices from being
// numbered alike, so it is read from the id rather than counted from the rows.
func DeviceSeq(id string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "DEVICE-"))
	if err != nil {
		return 0
	}
	return n
}
