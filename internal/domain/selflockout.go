package domain

import (
	"errors"
	"fmt"
)

// ErrSelfLockout refuses a write that would cut the device running Kergui
// off the box (UX audit 2026-11-26, C1): « Activer » and « Rendre l'accès »
// are free forever, but an app that can no longer reach the box cannot run
// them. The household would need a cable or the box's own web page.
//
// The concrete error is a *SelfLockoutError, which wraps it.
var ErrSelfLockout = errors.New("refusing to cut the device running Kergui off the box")

// Why a write would cut the device running Kergui off the box.
const (
	// SelfLockoutDevice: blocking this device, or cutting its Wi-Fi access.
	SelfLockoutDevice = "self_device"
	// SelfLockoutNetwork: turning off the Wi-Fi network this device is on.
	SelfLockoutNetwork = "self_network"
	// SelfLockoutLastNetwork: turning off the last Wi-Fi network still on.
	SelfLockoutLastNetwork = "last_network"
)

// SelfLockoutError says which guard refused the write, and on what: a MAC
// for SelfLockoutDevice, a network's name for the other two.
type SelfLockoutError struct {
	Reason string
	Target string
}

func (e *SelfLockoutError) Error() string {
	switch e.Reason {
	case SelfLockoutDevice:
		return fmt.Sprintf("%v: %s is the device running Kergui", ErrSelfLockout, e.Target)
	case SelfLockoutNetwork:
		return fmt.Sprintf("%v: the device running Kergui is on %q", ErrSelfLockout, e.Target)
	case SelfLockoutLastNetwork:
		return fmt.Sprintf("%v: %q is the last Wi-Fi network on", ErrSelfLockout, e.Target)
	}
	return ErrSelfLockout.Error()
}

// Unwrap makes errors.Is(err, ErrSelfLockout) hold.
func (e *SelfLockoutError) Unwrap() error { return ErrSelfLockout }
