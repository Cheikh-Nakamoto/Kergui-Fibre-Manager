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
