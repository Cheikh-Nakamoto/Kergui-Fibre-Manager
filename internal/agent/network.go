package agent

import (
	"strings"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// RFC-005 design rule 2, on the relay's side (#45). The hub decides who
// holds the lease from the network it has on record, but the hub transports
// and the owner decides: the relay also reads the network the owner signed
// in the trust bundle, and stands down on its own when it is not on it. A
// relay standing down tells the hub on every heartbeat (released), so the
// hub never grants it the lease, and it never logs in to the box.

// How the relay knows it is in front of the household's box.
const (
	// CheckSignature: the Wi-Fi (SSID, and BSSID when registered) and the
	// box certificate both match what the owner signed.
	CheckSignature = "signature"
	// CheckBoxIdentity: the box certificate alone. The relay reports no
	// Wi-Fi (a PC on Ethernet; a phone without the location permission,
	// which Android requires to read the SSID), or the owner registered
	// none.
	CheckBoxIdentity = "box-identity"
)

// Seen is what the relay sees of its network on one round.
type Seen struct {
	SSID, BSSID string
	// RouterPin is the SHA-256 of the certificate the box presents; empty
	// when the box did not answer.
	RouterPin string
}

// NetworkCheck is the relay's verdict on one round.
type NetworkCheck struct {
	OK bool
	// Check is what proved it (CheckSignature or CheckBoxIdentity) when OK.
	Check string
	// Reason says why it stands down, or why only the box was checked.
	Reason string
}

// CheckNetwork compares what the relay sees with the network the owner
// signed. It is the same rule as the hub's (internal/hub/relay/domain
// Eligibility), read from a source the hub cannot change.
func CheckNetwork(n trust.Network, s Seen) NetworkCheck {
	pin := strings.TrimSpace(s.RouterPin)
	if pin == "" {
		return NetworkCheck{Reason: "the box does not answer on this network"}
	}
	if n.RouterPin != "" && !strings.EqualFold(pin, strings.TrimSpace(n.RouterPin)) {
		return NetworkCheck{Reason: "this is not the household's box"}
	}
	if s.SSID == "" && s.BSSID == "" {
		return NetworkCheck{OK: true, Check: CheckBoxIdentity,
			Reason: "no Wi-Fi name reported (location permission refused, or not on Wi-Fi): only the box identity is checked"}
	}
	if len(n.SSIDs) > 0 && s.SSID != "" && !containsFold(n.SSIDs, s.SSID) {
		return NetworkCheck{Reason: "this phone is on another Wi-Fi network than the household's"}
	}
	if len(n.BSSIDs) > 0 && s.BSSID != "" && !containsFold(n.BSSIDs, s.BSSID) {
		return NetworkCheck{Reason: "this phone is on a Wi-Fi access point the household did not register"}
	}
	if len(n.SSIDs) == 0 && len(n.BSSIDs) == 0 {
		return NetworkCheck{OK: true, Check: CheckBoxIdentity,
			Reason: "the household registered no Wi-Fi signature: only the box identity is checked"}
	}
	return NetworkCheck{OK: true, Check: CheckSignature}
}

func containsFold(list []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), want) {
			return true
		}
	}
	return false
}

// What the relay is doing, as Status reports it.
const (
	StatusStarting = "starting"
	// StatusActive: it holds the lease and works the box.
	StatusActive = "active"
	// StatusStandby: on the right network, waiting for the lease.
	StatusStandby = "standby"
	// StatusOffline: the hub does not answer; the relay does not touch the
	// box, having no way to know it is still the holder.
	StatusOffline = "offline"
	// StatusWrongNetwork: not on the network the owner signed; it stands
	// down and never logs in.
	StatusWrongNetwork = "wrong-network"
	// StatusPaused: it holds the lease, but the owner's phone holds the
	// box at home and the relay leaves it alone until they leave (#94).
	StatusPaused = "paused"
)

// Status is the relay's last round, for the phone's notification and its
// Settings screen. It carries no secret.
type Status struct {
	State  string
	Reason string
	// Check is how the network was verified on the last round that got
	// that far ("" before).
	Check string
	Epoch int64
	// AutoReject is whether it enforces the household's auto-reject rule:
	// a phone's own watch service then leaves that to it.
	AutoReject bool
	At         time.Time
}

// statusBox guards Status: Run writes it, the app reads it from another
// thread.
type statusBox struct {
	mu sync.Mutex
	s  Status
}

func (b *statusBox) get() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.s
}

func (b *statusBox) set(s Status) {
	b.mu.Lock()
	b.s = s
	b.mu.Unlock()
}

// Status returns what the relay did on its last round.
func (a *Agent) Status() Status { return a.status.get() }

// checkNetwork reads the network the owner signed in the bundle the relay
// holds. Without a box probe (tests, a relay wired without one) there is
// nothing to compare, and the hub's own check stands alone.
func (a *Agent) checkNetwork(s Seen) NetworkCheck {
	if a.cfg.Router == nil {
		return NetworkCheck{OK: true}
	}
	var n trust.Network
	if b := a.verifier.Bundle(); b != nil {
		n = b.Network
	}
	return CheckNetwork(n, s)
}
