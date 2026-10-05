// Package domain holds the enterprise entities and value objects of Kergui
// Fibre Manager. It is the innermost ring of the Clean Architecture: it depends
// on nothing but the Go standard library. No HTTP, no SQL, no CLI, no router
// specifics ever leak in here.
package domain

import "errors"

// Domain error taxonomy (see project brief §14). Outer layers wrap concrete
// causes around these sentinels; the CLI maps each to a human message.
var (
	ErrRouterUnreachable   = errors.New("router unreachable")
	ErrAuthFailed          = errors.New("authentication failed")
	ErrSessionExpired      = errors.New("session expired")
	ErrUnsupportedFirmware = errors.New("unsupported firmware")
	ErrUnsupportedModel    = errors.New("unsupported router model")
	ErrInvalidMAC          = errors.New("invalid MAC address")
	ErrInvalidIP           = errors.New("invalid IP address")
	ErrInvalidAccessMode   = errors.New("invalid access-control mode")
	ErrDeviceNotFound      = errors.New("device not found")
	ErrBlockFailed         = errors.New("block operation failed")
	ErrUnblockFailed       = errors.New("unblock operation failed")
	ErrUnexpectedResponse  = errors.New("unexpected router response")
	ErrNotImplemented      = errors.New("not implemented")
)
