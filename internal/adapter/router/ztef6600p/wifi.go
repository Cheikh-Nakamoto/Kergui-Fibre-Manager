package ztef6600p

// Wi-Fi access control ("Wi-Fi > Avancé > Contrôle d'accès"): unlike the
// firewall MAC filter, which only drops routed traffic, a Ban rule here makes
// the access point refuse the station, so the device actually leaves the Wi-Fi.
//
// The router keeps one rule list (each rule bound to one AP via Interface) and a
// per-AP mode: Disabled / Ban (blocklist) / Allow (allowlist). Rules mean the
// opposite in Allow mode, so kergui never writes to an AP in Allow mode.

import (
	"context"
	"fmt"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

const (
	wlanAdvancedView = "wlanAdvanced"
	wlanPolicyTag    = "wlan_macfilteraclpolicy_lua.lua"
	wlanRuleTag      = "wlan_macfilterrule_lua.lua"
	wlanStatusTag    = "wlan_wlanstatus_lua.lua"
)

var _ port.WiFiAccessControl = (*Gateway)(nil)

type wlanRule struct {
	InstID    string
	Name      string
	Interface string
	MAC       domain.MAC
}

// parseInstances returns every Instance of the first object whose tag contains
// objSubstr, as maps, or nil (with ok=false) when the document is an error.
func parseInstances(data, objSubstr string) (out []map[string]string, ok bool) {
	root, err := parseAjaxXML(data)
	if err != nil || !isSuccess(root) {
		return nil, false
	}
	obj := findObject(root, objSubstr)
	if obj == nil {
		return nil, true
	}
	for _, inst := range obj.Instances {
		out = append(out, inst.toMap())
	}
	return out, true
}

func parseWLANRules(data string) ([]wlanRule, bool) {
	insts, ok := parseInstances(data, "ACLCFG")
	if !ok {
		return nil, false
	}
	var out []wlanRule
	for _, m := range insts {
		mac, err := domain.ParseMAC(m["MACAddress"])
		if err != nil {
			continue
		}
		out = append(out, wlanRule{InstID: m["_InstID"], Name: m["Name"], Interface: m["Interface"], MAC: mac})
	}
	return out, true
}

func (g *Gateway) wlanData(ctx context.Context, tag string) (string, error) {
	body, _, err := g.sess.Get(ctx, "/?_type=menuData&_tag="+tag)
	if err != nil {
		return "", fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	return string(body), nil
}

func (g *Gateway) wlanRules(ctx context.Context) ([]wlanRule, error) {
	data, err := g.wlanData(ctx, wlanRuleTag)
	if err != nil {
		return nil, err
	}
	rules, ok := parseWLANRules(data)
	if !ok {
		return nil, fmt.Errorf("%w: Wi-Fi rules: %s", domain.ErrSessionExpired, truncate(data, 300))
	}
	return rules, nil
}

// activeAPs returns the enabled access points (e.g. both bands of the main
// SSID plus a guest SSID): a device blocked on one band would otherwise just
// reconnect on the other.
func (g *Gateway) activeAPs(ctx context.Context) ([]string, error) {
	data, err := g.wlanData(ctx, wlanStatusTag)
	if err != nil {
		return nil, err
	}
	insts, ok := parseInstances(data, "WLANAP")
	if !ok {
		return nil, fmt.Errorf("%w: Wi-Fi status: %s", domain.ErrSessionExpired, truncate(data, 300))
	}
	var active []string
	for _, m := range insts {
		if m["Enable"] == "1" {
			active = append(active, m["_InstID"])
			g.log.Debugf("f6600p: active AP %s (%s)", m["_InstID"], m["ESSID"])
		}
	}
	if len(active) == 0 {
		return nil, fmt.Errorf("%w: no enabled Wi-Fi access point found", domain.ErrUnexpectedResponse)
	}
	return active, nil
}

// WiFiBlockedMACs implements port.WiFiAccessControl: MACs with a rule on at
// least one AP in Ban mode.
func (g *Gateway) WiFiBlockedMACs(ctx context.Context) ([]domain.MAC, error) {
	var out []domain.MAC
	err := g.withSessionRetry(ctx, "Wi-Fi rules read", func() error {
		out = nil
		if err := g.loadView(ctx, wlanAdvancedView); err != nil {
			return err
		}
		data, err := g.wlanData(ctx, wlanPolicyTag)
		if err != nil {
			return err
		}
		policies, ok := parseInstances(data, "WLANAP")
		if !ok {
			return fmt.Errorf("%w: Wi-Fi policy: %s", domain.ErrSessionExpired, truncate(data, 300))
		}
		banned := map[string]bool{}
		for _, p := range policies {
			banned[p["_InstID"]] = p["ACLPolicy"] == "Ban"
		}
		rules, err := g.wlanRules(ctx)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, r := range rules {
			if banned[r.Interface] && !seen[r.MAC.String()] {
				seen[r.MAC.String()] = true
				out = append(out, r.MAC)
			}
		}
		return nil
	})
	return out, err
}

// WiFiBlock implements port.WiFiAccessControl.
func (g *Gateway) WiFiBlock(ctx context.Context, mac domain.MAC) error {
	if err := g.withSessionRetry(ctx, "Wi-Fi block", func() error { return g.wifiBlockOnce(ctx, mac) }); err != nil {
		return err
	}
	return g.verifyWiFi(ctx, mac, true, domain.ErrBlockFailed)
}

// WiFiUnblock implements port.WiFiAccessControl.
func (g *Gateway) WiFiUnblock(ctx context.Context, mac domain.MAC) error {
	if err := g.withSessionRetry(ctx, "Wi-Fi unblock", func() error { return g.wifiUnblockOnce(ctx, mac) }); err != nil {
		return err
	}
	return g.verifyWiFi(ctx, mac, false, domain.ErrUnblockFailed)
}

func (g *Gateway) verifyWiFi(ctx context.Context, mac domain.MAC, want bool, failErr error) error {
	if g.opts.SkipWriteVerify {
		return nil
	}
	macs, err := g.WiFiBlockedMACs(ctx)
	if err != nil {
		return err
	}
	got := false
	for _, m := range macs {
		if m.Equal(mac) {
			got = true
		}
	}
	if got != want {
		g.log.Errorf("f6600p: Wi-Fi verification failed for %s: want blocked=%v", mac, want)
		return fmt.Errorf("%w: router accepted the request but the Wi-Fi access list does not reflect it", failErr)
	}
	g.log.Infof("f6600p: verified %s Wi-Fi blocked=%v on the router", mac, want)
	return nil
}

func (g *Gateway) wifiBlockOnce(ctx context.Context, mac domain.MAC) error {
	if err := g.loadView(ctx, wlanAdvancedView); err != nil {
		return err
	}
	data, err := g.wlanData(ctx, wlanPolicyTag)
	if err != nil {
		return err
	}
	policies, ok := parseInstances(data, "WLANAP")
	if !ok || len(policies) == 0 {
		return fmt.Errorf("%w: Wi-Fi policy: %s", domain.ErrSessionExpired, truncate(data, 300))
	}
	g.log.Debugf("f6600p: Wi-Fi policies before write: %v", policies)
	targets, err := g.activeAPs(ctx)
	if err != nil {
		return err
	}
	policyOf := map[string]string{}
	for _, p := range policies {
		policyOf[p["_InstID"]] = p["ACLPolicy"]
	}
	for _, ap := range targets {
		if policyOf[ap] == "Allow" {
			return fmt.Errorf("%w: %s is in Wi-Fi allowlist mode; adding %s there would allow it, not block it",
				domain.ErrBlockFailed, ap, mac)
		}
	}

	rules, err := g.wlanRules(ctx)
	if err != nil {
		return err
	}
	has := map[string]bool{}
	for _, r := range rules {
		if r.MAC.Equal(mac) {
			has[r.Interface] = true
		}
	}
	for _, ap := range targets {
		if has[ap] {
			continue
		}
		body := wlanRuleForm("Apply", wlanRule{InstID: "-1", Name: mac.String(), Interface: ap, MAC: mac}, g.sessionToken)
		if err := g.postSigned(ctx, "/?_type=menuData&_tag="+wlanRuleTag, body, domain.ErrBlockFailed); err != nil {
			return err
		}
	}

	needBan := false
	for _, ap := range targets {
		if policyOf[ap] != "Ban" {
			needBan = true
		}
	}
	if !needBan {
		return nil
	}
	newPolicy := map[string]string{}
	for _, p := range policies {
		newPolicy[p["_InstID"]] = orDefault(p["ACLPolicy"], "Disabled")
	}
	for _, ap := range targets {
		newPolicy[ap] = "Ban"
	}
	body := wlanPolicyForm(policies, newPolicy, g.sessionToken)
	return g.postSigned(ctx, "/?_type=menuData&_tag="+wlanPolicyTag, body, domain.ErrBlockFailed)
}

func (g *Gateway) wifiUnblockOnce(ctx context.Context, mac domain.MAC) error {
	if err := g.loadView(ctx, wlanAdvancedView); err != nil {
		return err
	}
	rules, err := g.wlanRules(ctx)
	if err != nil {
		return err
	}
	removed := 0
	for _, r := range rules {
		if !r.MAC.Equal(mac) {
			continue
		}
		body := wlanRuleForm("Delete", r, g.sessionToken)
		if err := g.postSigned(ctx, "/?_type=menuData&_tag="+wlanRuleTag, body, domain.ErrUnblockFailed); err != nil {
			return err
		}
		removed++
	}
	if removed == 0 {
		g.log.Infof("f6600p: %s has no Wi-Fi rule, nothing to unblock", mac)
	}
	return nil
}

// wlanRuleForm mirrors the MACFilterRule template: _InstID, MACAddress (hidden,
// rebuilt from the sub fields), Name, Interface, then the six sub fields.
func wlanRuleForm(action string, r wlanRule, token string) string {
	octets := strings.Split(strings.ToUpper(r.MAC.String()), ":")
	var b strings.Builder
	add := func(k, v string) { b.WriteString("&" + k + "=" + encodeURIComponent(v)) }
	b.WriteString("IF_ACTION=" + action)
	add("_InstID", r.InstID)
	add("MACAddress", strings.Join(octets, ":"))
	add("Name", r.Name)
	add("Interface", r.Interface)
	for i, o := range octets {
		add(fmt.Sprintf("sub_MACAddress%d", i), o)
	}
	b.WriteString("&_sessionTOKEN=" + token)
	return b.String()
}

// wlanPolicyForm mirrors the MACFilterACLPolicy template: _InstNum, then one
// cloned row per AP (_InstID_i, checked radio ACLPolicy_i; Alias/WLANViewName/
// LockStatus are PostIgnore), then the hidden original row's empty _InstID.
func wlanPolicyForm(policies []map[string]string, policy map[string]string, token string) string {
	var b strings.Builder
	add := func(k, v string) { b.WriteString("&" + k + "=" + encodeURIComponent(v)) }
	b.WriteString("IF_ACTION=Apply")
	add("_InstNum", fmt.Sprintf("%d", len(policies)))
	for i, p := range policies {
		id := p["_InstID"]
		add(fmt.Sprintf("_InstID_%d", i), id)
		add(fmt.Sprintf("ACLPolicy_%d", i), policy[id])
	}
	add("_InstID", "")
	b.WriteString("&_sessionTOKEN=" + token)
	return b.String()
}
