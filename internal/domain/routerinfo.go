package domain

import "time"

// RouterInfo is the result of non-destructive discovery plus whatever an
// authenticated adapter can read about the device itself (brief §10, §15).
type RouterInfo struct {
	BaseURL      string
	Vendor       string
	Model        string
	Firmware     string
	HardwareVer  string
	HTTPServer   string // Server response header
	Title        string // <title> of the login page
	AdapterID    string // adapter suggested for this router ("" if none matched)
	Confidence   int    // 0..100 confidence in the suggested adapter
	Reachable    bool
	DiscoveredAt time.Time
}

// AdapterMeta describes a router adapter and the operations it documents. The
// Verified flags record whether an endpoint has been confirmed on real hardware
// — the project's core honesty contract: never present a hypothesis as fact
// (brief §3, §4).
type AdapterMeta struct {
	ID            string   // stable id, e.g. "zte_f660"
	Vendor        string   // e.g. "ZTE"
	Models        []string // e.g. ["F660", "ZXHN F660"]
	Verified      bool     // true only once read endpoints are validated on a real device
	WriteVerified bool     // true only once write endpoints (block/unblock) are validated on a real device
	Markers       []string // case-insensitive substrings that fingerprint this model during discovery
	Endpoints     []EndpointDoc
}

// EndpointDoc documents one router operation for the `inspect` diagnostic report.
type EndpointDoc struct {
	Operation string // "login", "devices", "access_rules", "block", "unblock", ...
	Method    string // "GET" / "POST"
	Path      string // request path on the router
	Verified  bool   // confirmed against a real router?
	Notes     string
}
