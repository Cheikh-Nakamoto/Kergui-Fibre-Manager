package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// MAC is a validated, canonicalised MAC address value object. The canonical form
// is lowercase, colon-separated: "aa:bb:cc:dd:ee:ff". MAC is the primary network
// identity of a device (brief §6, §8): IP addresses change, the MAC does not.
//
// The zero value is the "unknown" MAC and reports IsZero() == true.
type MAC struct {
	v string
}

const hexdigits = "0123456789abcdef"

// ParseMAC normalises any common MAC notation (colon, hyphen, dot, or bare) into
// the canonical form. It returns ErrInvalidMAC for anything that is not exactly
// 48 bits of hex.
func ParseMAC(s string) (MAC, error) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ':', '-', '.', ' ':
			return -1
		}
		return r
	}, strings.TrimSpace(s))

	if len(cleaned) != 12 {
		return MAC{}, fmt.Errorf("%w: %q", ErrInvalidMAC, s)
	}
	cleaned = strings.ToLower(cleaned)
	for _, r := range cleaned {
		if !strings.ContainsRune(hexdigits, r) {
			return MAC{}, fmt.Errorf("%w: %q", ErrInvalidMAC, s)
		}
	}

	var b strings.Builder
	b.Grow(17)
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(cleaned[i : i+2])
	}
	return MAC{v: b.String()}, nil
}

// MustMAC is a test/seed helper that panics on an invalid MAC.
func MustMAC(s string) MAC {
	m, err := ParseMAC(s)
	if err != nil {
		panic(err)
	}
	return m
}

// String returns the canonical "aa:bb:cc:dd:ee:ff" form (empty for the zero MAC).
func (m MAC) String() string { return m.v }

// IsZero reports whether the MAC is unset.
func (m MAC) IsZero() bool { return m.v == "" }

// Equal reports value equality (both already canonical).
func (m MAC) Equal(o MAC) bool { return m.v == o.v }

// MarshalJSON renders the MAC as its canonical string.
func (m MAC) MarshalJSON() ([]byte, error) { return []byte(strconv.Quote(m.v)), nil }

// UnmarshalJSON parses the canonical string back. A value object that writes
// itself as a string has to read itself back, or anything that re-reads its
// own JSON fails: the relay could not even find its own address in a device
// list, which is what keeps it from cutting itself (RFC-005, design rule 5).
// An empty string is the unknown MAC, not an error, as ParseMAC's own
// callers expect.
func (m *MAC) UnmarshalJSON(data []byte) error {
	s, err := strconv.Unquote(string(data))
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidMAC, data)
	}
	if strings.TrimSpace(s) == "" {
		*m = MAC{}
		return nil
	}
	parsed, err := ParseMAC(s)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// OUI returns the 24-bit Organisationally Unique Identifier ("aa:bb:cc"), usable
// for vendor lookup. It is empty for the zero MAC.
func (m MAC) OUI() string {
	if len(m.v) < 8 {
		return ""
	}
	return m.v[:8]
}
