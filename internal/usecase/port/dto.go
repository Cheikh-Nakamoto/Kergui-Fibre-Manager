package port

import "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"

// DeviceView is a device enriched with inventory-reconciliation metadata for
// presentation (brief §7, §17: "new device detected").
type DeviceView struct {
	domain.Device
	NewlySeen bool // true if this MAC was not previously in the local inventory
}

// InspectReport is the structured result of the `inspect` diagnostic flow
// (brief §4). It is rendered by a presenter into the report the brief shows.
type InspectReport struct {
	Info            domain.RouterInfo
	Meta            domain.AdapterMeta
	AuthOK          bool
	DeviceCount     int
	AccessRuleCount int
	// Warnings surfaces honesty caveats (e.g. endpoints UNVERIFIED) and any
	// non-fatal problems met while probing.
	Warnings []string
}
