package domain

import (
	"fmt"
	"strings"
)

// ErrWeakPassword refuses a new router admin password before the router is
// asked. It is an ErrInvalidInput: the request, not the router, is wrong.
var ErrWeakPassword = fmt.Errorf("%w: weak router password", ErrInvalidInput)

// BoxPasswordMinLength is the shortest admin password the F6600P accepts.
const BoxPasswordMinLength = 8

// ValidateBoxPassword applies the rule the ZTE F6600P checks in its forced
// password-change page (checkPassStrong plus the form's ASCII rule): at least
// 8 characters, at least one lower-case letter, one upper-case letter and one
// digit, printable ASCII only. The app and the core refuse a weaker password
// up front; the router stays the judge and may still refuse one that passes.
func ValidateBoxPassword(p string) error {
	var lower, upper, digit bool
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c < 0x20 || c > 0x7e:
			return fmt.Errorf("%w: only printable ASCII characters (no accents)", ErrWeakPassword)
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= '0' && c <= '9':
			digit = true
		}
	}
	var missing []string
	if len(p) < BoxPasswordMinLength {
		missing = append(missing, fmt.Sprintf("at least %d characters", BoxPasswordMinLength))
	}
	if !lower {
		missing = append(missing, "a lower-case letter")
	}
	if !upper {
		missing = append(missing, "an upper-case letter")
	}
	if !digit {
		missing = append(missing, "a digit")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: needs %s", ErrWeakPassword, strings.Join(missing, ", "))
	}
	return nil
}
