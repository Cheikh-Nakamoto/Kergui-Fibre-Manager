package domain

import "time"

// Device is an entry in the network inventory: "who is present on the network?"
// (brief §18). Its identity is the MAC (brief §6, §8). The IP, hostname and
// connection state are mutable observations; CustomName is the user's own label,
// independent of whatever name the router reports.
type Device struct {
	ID         string
	MAC        MAC
	IP         IP
	Hostname   string // name reported by the router / DHCP
	CustomName string // name chosen by the user (brief §6)
	Vendor     string // derived from the MAC OUI when available
	SSID       string
	FirstSeen  time.Time
	LastSeen   time.Time
	Connected  bool
	Blocked    bool
	RouterID   string
	Notes      string
	// LabelAt is when the household last set CustomName or Notes (#95), on
	// the clock of the phone or PC that made the edit. The owner's phone and
	// the household's relay each keep their own copy of the labels; the
	// newer one wins when they meet ("last write wins"). Zero when the
	// labels were never edited, or were edited before labels carried a time.
	LabelAt time.Time `json:",omitzero"`
}

// Label is what the household says about one device, apart from what the
// box reports: its name and notes, and when they were set (#95). A nil Name
// or Notes leaves that field as it is: a rename does not wipe the notes.
type Label struct {
	MAC   MAC
	Name  *string
	Notes *string
	At    time.Time
}

// DisplayName is the best human label for the device: the user's custom name,
// else the router hostname, else the raw MAC.
func (d Device) DisplayName() string {
	switch {
	case d.CustomName != "":
		return d.CustomName
	case d.Hostname != "":
		return d.Hostname
	default:
		return d.MAC.String()
	}
}
