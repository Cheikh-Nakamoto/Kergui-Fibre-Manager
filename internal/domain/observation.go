package domain

import "time"

// Observation is what one router poll says about one station (one MAC),
// merged from every table that mentions it. Fields the router did not report
// are zero. It holds only what the router's own admin pages display.
type Observation struct {
	MAC        MAC
	At         time.Time // poll time; every observation of one poll shares it
	IPv4       IP
	Hostname   string // DHCP option 12, as the router stores it
	SSID       string
	Band       string // "2.4G", "5G" or "LAN"
	Associated bool   // listed by the Wi-Fi association table at At
	AssocSince time.Time
	Lease      *Lease
	Radio      RadioCaps
}

// Lease is the router's DHCPv4 lease for the MAC at the time of the poll.
type Lease struct {
	// Seq is the router's lease instance number. The F6600P allocates a new
	// one for every DHCP grant, so a new Seq means a new DHCP transaction.
	Seq       int
	GrantedAt time.Time
	ExpiresAt time.Time
	Port      string // "SSID1", "LAN2", ...
}

// RadioCaps is what the station's Wi-Fi chip negotiated on one band. Many
// devices of one model share it, so recognition only uses it to keep two
// devices apart, never to link two MACs.
type RadioCaps struct {
	Standard    string // "802.11n", "802.11ac", "802.11ax"
	BTM         bool   // 802.11v BSS transition management
	RRM         bool   // 802.11k radio resource management
	Security    string // "WPA3", "WPA2", ...
	MaxLinkMbps int
}

// Known reports whether the router described the radio at all.
func (r RadioCaps) Known() bool { return r.Standard != "" }
