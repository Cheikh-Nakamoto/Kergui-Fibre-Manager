package mockrouter

// The box's two deny lists fill up (RFC-007 § Context, D6): the mock can be
// given a rule limit per list and answers FullAnswer beyond it, the way the
// box refuses an add when its table is full. It can also start from the
// Wi-Fi access control read on a real box (63 rules, RFC-007 R4), hold rules
// the household made in the box's own page, and count the Wi-Fi writes, so a
// test can prove a purge never wrote to the Wi-Fi.

import (
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/fixtures"
)

// FullAnswer is what the mock answers to an add beyond its limit. The real
// box's text is measured in the live write session (RFC-007 W1/W7, #101);
// until then the adapter recognises this one among its list of answers.
const FullAnswer = "Le nombre de règles a atteint le maximum."

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
	s.wifiRules = append(s.wifiRules, wifiRule{mac: mac, ap: ap, name: name})
}

// SetWiFiPolicy sets one access point's mode (Disabled, Ban or Allow), as the
// box's own page would.
func (s *F6600P) SetWiFiPolicy(ap, mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wifiACL[ap] = mode
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
// a real box (RFC-007 Step 0): its modes per access point and its 63 rules.
func (s *F6600P) LoadCapturedWiFiACL() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wifiACL = map[string]string{}
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
		})
	}
}
