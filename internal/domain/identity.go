package domain

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Identity-loss policy: a device that reappears under a new random MAC loses
// the link to its consumption and its cut. Three such losses within the window
// blacklist it.
const (
	IdentityLossLimit  = 3
	IdentityLossWindow = 30 * 24 * time.Hour
)

// IsRandomized reports whether the MAC is locally administered, i.e. a private
// / random address chosen by the phone rather than its factory address.
func (m MAC) IsRandomized() bool {
	if len(m.v) < 2 {
		return false
	}
	b, err := strconv.ParseUint(m.v[:2], 16, 8)
	return err == nil && b&0x02 != 0
}

var (
	macLike      = regexp.MustCompile(`^[0-9a-f]{2}([:-]?[0-9a-f]{2}){5}$`)
	genericNames = map[string]bool{
		"": true, "iphone": true, "ipad": true, "ipod": true, "android": true, "localhost": true,
		"unknown": true, "*": true, "phone": true, "mobile": true,
	}
)

// IdentityKey turns a router-reported hostname into a key that identifies one
// physical device across MAC changes. ok is false for names many devices
// share ("iPhone", "android-1a2b…", empty), which must never be used to tie
// two addresses together.
func IdentityKey(hostname string) (key string, ok bool) {
	k := strings.ToLower(strings.TrimSpace(hostname))
	k = strings.TrimSuffix(k, ".local")
	k = strings.TrimSuffix(k, ".lan")
	if genericNames[k] || macLike.MatchString(k) ||
		strings.HasPrefix(k, "android-") || strings.HasPrefix(k, "android_") {
		return "", false
	}
	return k, true
}

// IdentityState is what kergui remembers about one identity.
type IdentityState struct {
	Shared        bool // two addresses with this name were online together: two devices
	BlacklistedAt time.Time
	LiftedAt      time.Time
}

// Blacklisted reports whether a blacklist is in force.
func (s IdentityState) Blacklisted() bool {
	return !s.BlacklistedAt.IsZero() && !s.LiftedAt.After(s.BlacklistedAt)
}

// Sighting is one address seen under an identity.
type Sighting struct {
	MAC       MAC
	FirstSeen time.Time
}

// IdentityLosses counts the address changes of one identity since from: every
// random address first seen at or after from, except the identity's very first
// address (that one is the device, not a change).
func IdentityLosses(sightings []Sighting, from time.Time) int {
	if len(sightings) < 2 {
		return 0
	}
	first := sightings[0]
	for _, s := range sightings[1:] {
		if s.FirstSeen.Before(first.FirstSeen) {
			first = s
		}
	}
	n := 0
	for _, s := range sightings {
		if s.MAC.Equal(first.MAC) || !s.MAC.IsRandomized() || s.FirstSeen.Before(from) {
			continue
		}
		n++
	}
	return n
}
