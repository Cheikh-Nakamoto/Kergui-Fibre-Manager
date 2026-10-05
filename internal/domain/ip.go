package domain

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// IP is a validated IP address value object. It wraps net/netip.Addr. The zero
// value is "unknown" (IsZero() == true); an IP is a mutable attribute of a
// device, never its identity (brief §8).
type IP struct {
	addr netip.Addr
}

// ParseIP validates an IPv4/IPv6 address. An empty string yields the zero IP
// without error (devices may legitimately have no current lease).
func ParseIP(s string) (IP, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return IP{}, nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return IP{}, fmt.Errorf("%w: %q", ErrInvalidIP, s)
	}
	return IP{addr: a}, nil
}

// MustIP is a test/seed helper that panics on an invalid IP.
func MustIP(s string) IP {
	ip, err := ParseIP(s)
	if err != nil {
		panic(err)
	}
	return ip
}

// String returns the textual form, empty for the zero IP.
func (ip IP) String() string {
	if !ip.addr.IsValid() {
		return ""
	}
	return ip.addr.String()
}

// IsZero reports whether the IP is unset.
func (ip IP) IsZero() bool { return !ip.addr.IsValid() }

// MarshalJSON renders the IP as its textual form ("" for the zero IP).
func (ip IP) MarshalJSON() ([]byte, error) { return []byte(strconv.Quote(ip.String())), nil }
