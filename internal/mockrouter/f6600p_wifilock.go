package mockrouter

// The closed Wi-Fi on the mock box (RFC-007 D4, D7). An access point in
// Allow admits only the devices listed on it, in the one rule table every AP
// shares (one rule per device per AP). A rule means "refuse" in Ban and
// "admit" in Allow, so the mock records each write that reads a list the
// wrong way (WiFiHazards): the trap of RFC-007, and the two quirks measured
// on the live box on 2026-11-27 (W2, W2 bis). Tests assert there is none.
//
// What the live session measured, and what the mock does instead:
//   - W2: Allow switched on with an empty list has no effect, everyone still
//     joins, and a rule added later does not take effect until the AP
//     restarts. The mock keeps such an AP "inert" until its mode changes.
//   - W2 bis: the last rule of an AP already in Allow removed, everyone is
//     refused. The mock follows from the rules: nobody is listed.
//   - W3: a connected station that is not listed is dropped about 40 s after
//     the switch to Allow, and one whose rule is removed in Allow within
//     about 25 s. The mock drops them at once.
//   - W4: a rule added in Allow, and the flips between Allow, Disabled and
//     Ban, take effect at once, with no restart. So does the mock.

import (
	"fmt"
	"strings"
)

// WiFiPolicy is the mode of one access point: Disabled, Ban or Allow.
func (s *F6600P) WiFiPolicy(ap string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wifiACL[ap]
}

// Admits reports whether ap lets mac associate, as the box would answer the
// device: Disabled admits everyone, Ban refuses the listed devices, Allow
// admits the listed ones only (everyone when it was switched on with an
// empty list, W2).
func (s *F6600P) Admits(ap, mac string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	listed := s.listedOn(ap, mac)
	switch s.wifiACL[ap] {
	case "Ban":
		return !listed
	case "Allow":
		return s.wifiInert[ap] || listed
	}
	return true
}

// WiFiHazards lists, in order, each write that made an access point read its
// list the wrong way: Ban straight to Allow, or back, with rules on it (the
// trap), Allow with an empty list (W2), the last rule of an AP in Allow
// removed (W2 bis).
func (s *F6600P) WiFiHazards() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.wifiHazards...)
}

// FailWiFiWrite makes the n-th Wi-Fi access-control write from now (1: the
// next one, rule or mode) answer FAIL and change nothing, as a box that
// refuses a form. 0 cancels.
func (s *F6600P) FailWiFiWrite(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wifiFailIn = n
}

// LoseWiFiRules makes the box accept every add of mac and keep none of them,
// so only a read-back can tell.
func (s *F6600P) LoseWiFiRules(mac string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wifiLost == nil {
		s.wifiLost = map[string]bool{}
	}
	s.wifiLost[strings.ToLower(mac)] = true
}

// RemoveWiFiRule removes mac's rule on ap, as the household would from the
// box's own page.
func (s *F6600P) RemoveWiFiRule(mac, ap string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.wifiRules {
		if strings.EqualFold(r.mac, mac) && r.ap == ap {
			s.deleteWiFiRule(i)
			return
		}
	}
}

// failWiFiWrite counts down FailWiFiWrite and reports whether this write is
// the one to refuse. The caller holds s.mu.
func (s *F6600P) failWiFiWrite() bool {
	if s.wifiFailIn <= 0 {
		return false
	}
	s.wifiFailIn--
	return s.wifiFailIn == 0
}

// listedOn reports whether mac has a rule on ap. The caller holds s.mu.
func (s *F6600P) listedOn(ap, mac string) bool {
	for _, r := range s.wifiRules {
		if r.ap == ap && strings.EqualFold(r.mac, mac) {
			return true
		}
	}
	return false
}

// rulesOn counts the rules bound to ap. The caller holds s.mu.
func (s *F6600P) rulesOn(ap string) int {
	n := 0
	for _, r := range s.wifiRules {
		if r.ap == ap {
			n++
		}
	}
	return n
}

// setWiFiMode switches one access point's mode, the rules untouched, and
// records a switch that reads them the wrong way. The caller holds s.mu.
func (s *F6600P) setWiFiMode(ap, mode string) {
	from := s.wifiACL[ap]
	if from == mode {
		return
	}
	if s.wifiInert == nil {
		s.wifiInert = map[string]bool{}
	}
	n := s.rulesOn(ap)
	if n > 0 && ((from == "Ban" && mode == "Allow") || (from == "Allow" && mode == "Ban")) {
		s.hazard("%s switched from %s to %s with %d rules: every rule now means the opposite", ap, from, mode, n)
	}
	s.wifiACL[ap] = mode
	s.wifiInert[ap] = false
	if mode != "Allow" {
		return
	}
	if n == 0 {
		// W2: the box takes the mode and filters nothing.
		s.wifiInert[ap] = true
		s.hazard("%s switched to Allow with an empty list: the box filters nothing", ap)
		return
	}
	s.dropUnlisted(ap)
}

// deleteWiFiRule removes the i-th rule; in Allow, its device is refused and
// dropped. The caller holds s.mu.
func (s *F6600P) deleteWiFiRule(i int) {
	r := s.wifiRules[i]
	s.wifiRules = append(s.wifiRules[:i], s.wifiRules[i+1:]...)
	if s.wifiACL[r.ap] != "Allow" || s.wifiInert[r.ap] {
		return
	}
	if s.rulesOn(r.ap) == 0 {
		s.hazard("%s lost its last rule while in Allow: everyone is refused", r.ap)
	}
	s.dropUnlisted(r.ap)
}

// dropUnlisted takes off the Wi-Fi every station on ap that is not listed on
// it (W3: about 40 s after the switch on the real box, at once here). The
// caller holds s.mu.
func (s *F6600P) dropUnlisted(ap string) {
	for i := range s.stations {
		st := &s.stations[i]
		if st.AP == ap && st.Associated && !s.listedOn(ap, st.MAC) {
			st.Associated = false
			st.LinkSecs = 0
		}
	}
}

// hazard records a write that read a list the wrong way. The caller holds s.mu.
func (s *F6600P) hazard(format string, args ...any) {
	s.wifiHazards = append(s.wifiHazards, fmt.Sprintf(format, args...))
}

// validWiFiMode reports whether mode is one the box's form offers.
func validWiFiMode(mode string) bool {
	return mode == "Disabled" || mode == "Ban" || mode == "Allow"
}
