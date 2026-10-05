package domain

import (
	"fmt"
	"strings"
)

// AccessMode is the effect of an access-control rule. The project keeps the
// device inventory and the access-control model strictly separate (brief §18);
// AccessMode belongs only to the latter.
type AccessMode string

const (
	// AccessBlock denies the MAC (router "Black List" / "Bloc").
	AccessBlock AccessMode = "block"
	// AccessPermit allows the MAC (router "White List" / "Autorisé").
	AccessPermit AccessMode = "permit"
)

// ParseAccessMode maps the many vendor/locale spellings onto the canonical modes,
// including the French labels Orange Sénégal uses ("bloc" / "autorisé") and the
// black/white-list vocabulary.
func ParseAccessMode(s string) (AccessMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "block", "blocked", "bloc", "bloque", "bloqué", "deny", "denied", "black", "blacklist", "black-list", "ban":
		return AccessBlock, nil
	case "permit", "permitted", "allow", "allowed", "autorise", "autorisé", "white", "whitelist", "white-list":
		return AccessPermit, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidAccessMode, s)
	}
}

// Valid reports whether the mode is one of the known canonical values.
func (m AccessMode) Valid() bool { return m == AccessBlock || m == AccessPermit }

// String returns the canonical value.
func (m AccessMode) String() string { return string(m) }
