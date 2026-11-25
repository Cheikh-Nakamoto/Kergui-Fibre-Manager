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
	ErrInvalidInput        = errors.New("invalid input")
	ErrNothingToPay        = errors.New("nothing to pay")
	ErrPaymentUnavailable  = errors.New("payment provider unavailable")
	ErrPaymentNotFound     = errors.New("payment not found")
	ErrMemberNotFound      = errors.New("member not found")
	// ErrPasswordChangeRequired: the credentials were accepted, but the router
	// will not open its interface until the admin password is changed (a new
	// F6600P signed into with the password on its label). The session is kept
	// open so the change can follow at once.
	ErrPasswordChangeRequired = errors.New("the router requires a new admin password")
	// ErrPasswordRejected: the router refused the new admin password; the
	// wrapped message is the router's own.
	ErrPasswordRejected = errors.New("the router refused the new password")
	// ErrSessionHeld: another client holds the router's single admin session
	// and the caller chose not to evict it. A relay yields that way to the
	// owner's phone at home (#94): the owner's direct session comes first.
	ErrSessionHeld = errors.New("another admin session holds the router")
)
