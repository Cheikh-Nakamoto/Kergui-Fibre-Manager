package domain

import (
	"errors"
	"strings"

	alerts "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

// AccountID identifies an account. An account is a phone number verified
// over WhatsApp (RFC-006, Accounts); the id is the hub's own key for it, so
// the number can change without re-signing every box's pass.
type AccountID string

// Phone is an account's number in E.164. It is the alerts module's type, so
// the number a person verified for Pro and the number their alerts go to
// compare equal.
type Phone = alerts.Phone

// ParsePhone normalises a number to E.164.
func ParsePhone(s string) (Phone, error) { return alerts.ParsePhone(s) }

// BoxID is the box a pass binds to: the router's serial number
// (domain.RouterInfo.SerialNumber, e.g. "ZTEG1A2B3C4D"). RouterID is not
// used because it is derived from the base URL, the same in every household
// (RFC-006, Box identity).
type BoxID string

// maxBoxIDLen is generous: the F6600P's serial is 12 characters.
const maxBoxIDLen = 64

// ErrInvalidBoxID is an empty or implausible serial number. An empty one
// means the model does not expose its serial yet, so Pro cannot bind to it.
var ErrInvalidBoxID = errors.New("licensing: not a usable box serial number")

// ParseBoxID normalises a serial number to upper case, as the router adapter
// does, and accepts only letters and digits.
func ParseBoxID(s string) (BoxID, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" || len(s) > maxBoxIDLen {
		return "", ErrInvalidBoxID
	}
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return "", ErrInvalidBoxID
		}
	}
	return BoxID(s), nil
}
