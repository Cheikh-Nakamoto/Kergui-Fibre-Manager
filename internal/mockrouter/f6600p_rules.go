package mockrouter

// The box's two deny lists fill up (RFC-007 § Context, D6): the mock can be
// given a rule limit per list and answers FullAnswer beyond it, the way the
// box refuses an add when its table is full. It can also start from the
// Wi-Fi access control read on a real box (63 rules, RFC-007 R4), hold rules
// the household made in the box's own page, and count the Wi-Fi writes, so a
// test can prove a purge never wrote to the Wi-Fi.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/fixtures"
)

// FullAnswer is what the mock answers to an add beyond its limit: the real
// F6600P's text, measured live on 2026-11-27 (RFC-007 W1, W7).
const FullAnswer = "Le nombre d'entrées a atteint la limite maximale. Supprimez certaines entrées et saisissez-les à nouveau. "

// The live F6600P's limits (RFC-007 W1, W7): 64 Wi-Fi access-control rules
// shared by every AP, 32 firewall MAC filter rules. Pass them to
// SetRuleLimits to test against the real sizes.
const (
	F6600PWiFiRuleLimit     = 64
	F6600PFirewallRuleLimit = 32
)

// SetRuleLimits caps the firewall MAC filter at firewall rules and the shared
// Wi-Fi rule table at wifi rules; 0 means no limit. Rules already there stay.
func (s *F6600P) SetRuleLimits(firewall, wifi int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fwLimit, s.wifiLimit = firewall, wifi
}

// AddFirewallRule adds a firewall MAC filter rule named name, as the
// household would from the box's own page (kergui names its rules after the
// MAC itself).
func (s *F6600P) AddFirewallRule(mac, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addRule(mac)
	s.rules[len(s.rules)-1].name = name
}

// FirewallRule is one firewall MAC filter rule as the mock holds it.
type FirewallRule struct{ MAC, Name string }

// FirewallRules lists the firewall MAC filter rules, in the box's order.
func (s *F6600P) FirewallRules() []FirewallRule {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]FirewallRule, 0, len(s.rules))
	for _, r := range s.rules {
		out = append(out, FirewallRule{MAC: r.mac, Name: r.name})
	}
	return out
}

// AddWiFiRule adds a rule to the shared Wi-Fi table, bound to ap.
func (s *F6600P) AddWiFiRule(mac, ap, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendWiFiRule(wifiRule{mac: mac, ap: ap, name: name})
}

// WiFiRule is one rule of the shared Wi-Fi table as the mock holds it.
type WiFiRule struct{ MAC, AP, Name string }

// WiFiRules lists the shared Wi-Fi table, in the box's order.
func (s *F6600P) WiFiRules() []WiFiRule {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]WiFiRule, 0, len(s.wifiRules))
	for _, r := range s.wifiRules {
		out = append(out, WiFiRule{MAC: r.mac, AP: r.ap, Name: r.name})
	}
	return out
}

// KeepWiFiRuleIDs makes the Wi-Fi rule ids stay after a delete, leaving
// holes (the captured table has some, RFC-007 R6), instead of the default
// renumbering (DEV.ACL3 becoming DEV.ACL2). A rule added later takes the id
// after the highest. Rules already there keep their position as id, or the
// id they were loaded with.
func (s *F6600P) KeepWiFiRuleIDs() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.wifiRules {
		if s.wifiRules[i].id == "" {
			s.wifiRules[i].id = fmt.Sprintf("DEV.ACL%d", i+1)
		}
	}
	s.wifiKeepIDs = true
}

// wifiRuleID is the _InstID of the i-th rule. The caller holds s.mu.
func (s *F6600P) wifiRuleID(i int) string {
	if s.wifiKeepIDs && s.wifiRules[i].id != "" {
		return s.wifiRules[i].id
	}
	return fmt.Sprintf("DEV.ACL%d", i+1)
}

// appendWiFiRule adds r at the end of the table, with the id after the
// highest when ids are kept. The caller holds s.mu.
func (s *F6600P) appendWiFiRule(r wifiRule) {
	if s.wifiKeepIDs {
		highest := 0
		for _, o := range s.wifiRules {
			if n, err := strconv.Atoi(strings.TrimPrefix(o.id, "DEV.ACL")); err == nil && n > highest {
				highest = n
			}
		}
		r.id = fmt.Sprintf("DEV.ACL%d", highest+1)
	}
	s.wifiRules = append(s.wifiRules, r)
}

// SetWiFiPolicy sets one access point's mode (Disabled, Ban or Allow), as the
// box's own page would.
func (s *F6600P) SetWiFiPolicy(ap, mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setWiFiMode(ap, mode)
}

// WiFiRuleCount is how many rules the shared Wi-Fi table holds.
func (s *F6600P) WiFiRuleCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.wifiRules)
}

// WiFiWrites counts the writes accepted or refused on the Wi-Fi access
// control (rules and modes) since the mock started.
func (s *F6600P) WiFiWrites() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wifiWrites
}

// LoadCapturedWiFiACL replaces the Wi-Fi access control with the one read on
// a real box (RFC-007 Step 0): its modes per access point and its 63 rules,
// with their captured ids (holes included) once KeepWiFiRuleIDs is on.
func (s *F6600P) LoadCapturedWiFiACL() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wifiACL = map[string]string{}
	s.wifiInert = map[string]bool{}
	for _, inst := range parseParaInstances(fixtures.ZTEF6600PWLANACLPolicy, "OBJ_WLANAP_ID") {
		if ap, mode := inst["_InstID"], inst["ACLPolicy"]; strings.HasPrefix(ap, "DEV.WIFI.AP") && mode != "" {
			s.wifiACL[ap] = mode
		}
	}
	s.wifiRules = nil
	for _, inst := range parseParaInstances(fixtures.ZTEF6600PWLANMACFilterRule, "OBJ_ACLCFG_ID") {
		s.wifiRules = append(s.wifiRules, wifiRule{
			mac:  inst["MACAddress"],
			ap:   inst["Interface"],
			name: inst["Name"],
			id:   inst["_InstID"],
		})
	}
}
