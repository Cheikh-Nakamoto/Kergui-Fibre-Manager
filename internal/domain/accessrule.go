package domain

import "time"

// AccessRule is an entry in the access-control model: "who is allowed on the
// network?" (brief §18). It is deliberately a separate entity from Device — a
// rule can exist for a MAC that has never been seen, and a device can be present
// without any rule. Rules are keyed on the MAC, never the IP (brief §8).
type AccessRule struct {
	ID        string
	RouterID  string
	MAC       MAC
	Mode      AccessMode
	SSID      string // the WLAN this rule applies to, when the router scopes it
	Source    string // where the rule was read from (adapter id / page)
	RawRef    string // opaque handle the adapter may need to modify/delete it later
	CreatedAt time.Time
}
