// Package domain holds the backups module's types (issue #88): one current
// Kergui backup per household and box, and the few versions it replaced.
package domain

import (
	"errors"
	"regexp"
	"time"
)

// Errors the handlers map to status codes.
var (
	ErrNotFound = errors.New("backups: this box has no backup yet")
	ErrBadBox   = errors.New("backups: not a box id")
	ErrTooLarge = errors.New("backups: archive too large")
)

// MaxArchive bounds an archive sent to the hub. A household's backup is a
// few kilobytes (a few hundred devices, a year of months); this leaves room
// for a spreadsheet's re-save without letting a phone fill the database.
const MaxArchive = 2 << 20

// boxID is the core's id for a box (usecase.RouterID: the router URL as a
// slug, "192-168-1-1", "router" by default).
var boxID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)

// ValidBox reports whether s is a box id as the core keys it.
func ValidBox(s string) bool { return boxID.MatchString(s) }

// SourceKind says who sent a backup.
type SourceKind string

const (
	// SourcePhone is an owner's (or admin's) phone, with its kernel token.
	SourcePhone SourceKind = "phone"
	// SourceRelay is one of the household's relays (a PC, a dedicated
	// phone), with its kga_ token.
	SourceRelay SourceKind = "relay"
)

// Source is the sender of a backup: the member's user id for a phone, the
// agent id for a relay.
type Source struct {
	Kind SourceKind `json:"kind"`
	ID   string     `json:"id"`
}

// Backup is a box's backup as the hub keeps it: the canonical archive (the
// two CSV files and their columns, nothing else), who sent the last part
// and when, and what it holds.
type Backup struct {
	TenantID  string
	BoxID     string
	Archive   []byte
	Source    Source
	UpdatedAt time.Time
	Devices   int
	Months    int
}
