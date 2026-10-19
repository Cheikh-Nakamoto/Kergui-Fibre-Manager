// Package snapshot is the wire contract of the state a relay pushes to the
// hub on its own: what kinds there are, and what each body looks like.
//
// It sits apart from both sides on purpose. The relay (internal/agent) signs
// these bytes and the hub (internal/hub/inventory) reads them, and neither
// should own the other's format. The signature covers the exact bytes that
// travelled (trust.Signed), so a field added here never invalidates a
// snapshot signed by an older relay.
package snapshot

import (
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// The kinds a relay pushes. RFC-005 sets the cadence: devices and
// observations every 60 s, SSIDs every 5 min.
const (
	// KindDevices is the device list the app opens on. Its body is the
	// /api/devices JSON as it is, because H1 already serves it to the app
	// unchanged.
	KindDevices = "devices"
	// KindObservations is everything one poll says about each station. It is
	// what the hub's inventory diff and device recognition work on.
	KindObservations = "observations"
	// KindSSIDs is the household's Wi-Fi networks.
	KindSSIDs = "ssids"
)

// Observations is the body of an observations snapshot.
type Observations struct {
	// RelayMAC is the MAC of the device carrying the relay, when it could
	// work it out (it matches its own LAN IP in the device list). The hub
	// keeps it out of the alerts and refuses to let the WhatsApp key block
	// it: cutting the relay would end the household's remote access.
	RelayMAC string `json:"relay_mac,omitempty"`
	// Degraded says the relay could not read the full per-station view and
	// derived these observations from the device list instead. The hub still
	// uses them, but it knows they carry no lease and no radio.
	Degraded bool                   `json:"degraded,omitempty"`
	Hosts    []port.ObservationView `json:"hosts"`
}

// SSIDs is the body of an SSID snapshot. Passwords are never in it: the
// router's own are not read back to anyone (core rule C5).
type SSIDs struct {
	RelayMAC string            `json:"relay_mac,omitempty"`
	SSIDs    []domain.WiFiSSID `json:"ssids"`
}
