package ztef6600p

// Wi-Fi access control ("Wi-Fi > Avancé > Contrôle d'accès"): unlike the
// firewall MAC filter, which only drops routed traffic, a Ban rule here makes
// the access point refuse the station, so the device actually leaves the Wi-Fi.
//
// The router keeps one rule list (each rule bound to one AP via Interface) and a
// per-AP mode: Disabled / Ban (blocklist) / Allow (allowlist). Rules mean the
// opposite in Allow mode, so WiFiBlock never writes to an AP in Allow mode and
// WiFiUnblock only removes rules on APs in Ban. Only the closed Wi-Fi
// (ApplyWiFiLock, SetWiFiMode, at the end of this file) writes allow rules.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

const (
	wlanAdvancedView = "wlanAdvanced"
	wlanPolicyTag    = "wlan_macfilteraclpolicy_lua.lua"
	wlanRuleTag      = "wlan_macfilterrule_lua.lua"
	wlanStatusTag    = "wlan_wlanstatus_lua.lua"
	// The status page belongs to the "local network status" view: the router
	// answers SessionTimeout unless that view was loaded first.
	wlanStatusView = "localNetStatus"
)

var (
	_ port.WiFiAccessControl = (*Gateway)(nil)
	_ port.WiFiRuleReader    = (*Gateway)(nil)
	_ port.WiFiAllowList     = (*Gateway)(nil)
)

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
	insts, _, err := g.wlanStatus(ctx)
	if err != nil {
		return nil, err
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

// wlanStatus reads the Wi-Fi status page: every AP (Enable, ESSID, its radio
// in WLANViewName) and the band of each radio.
func (g *Gateway) wlanStatus(ctx context.Context) (aps []map[string]string, bandOf map[string]string, err error) {
	if err := g.loadView(ctx, wlanStatusView); err != nil {
		return nil, nil, err
	}
	data, err := g.wlanData(ctx, wlanStatusTag)
	// Back to the access-control view: the writes that follow need it.
	if verr := g.loadView(ctx, wlanAdvancedView); verr != nil && err == nil {
		err = verr
	}
	if err != nil {
		return nil, nil, err
	}
	aps, ok := parseInstances(data, "WLANAP")
	if !ok {
		return nil, nil, fmt.Errorf("%w: Wi-Fi status: %s", domain.ErrSessionExpired, truncate(data, 300))
	}
	radios, _ := parseInstances(data, "WLANSETTING")
	bandOf = map[string]string{}
	for _, r := range radios {
		bandOf[r["_InstID"]] = r["Band"]
	}
	return aps, bandOf, nil
}

// WiFiBlockedMACs implements port.WiFiAccessControl: MACs with a rule on at
// least one AP in Ban mode.
func (g *Gateway) WiFiBlockedMACs(ctx context.Context) ([]domain.MAC, error) {
	refusals, err := g.wifiRefusals(ctx)
	if err != nil {
		return nil, err
	}
	var out []domain.MAC
	seen := map[string]bool{}
	for _, r := range refusals {
		if !seen[r.MAC.String()] {
			seen[r.MAC.String()] = true
			out = append(out, r.MAC)
		}
	}
	return out, nil
}

// WiFiRules implements port.WiFiRuleReader: each rule on an AP in Ban mode,
// with its AP in SSID and whether kergui wrote it (RFC-007 D6).
func (g *Gateway) WiFiRules(ctx context.Context) ([]domain.AccessRule, error) {
	refusals, err := g.wifiRefusals(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.AccessRule, 0, len(refusals))
	for _, r := range refusals {
		out = append(out, domain.AccessRule{
			MAC:      r.MAC,
			Mode:     domain.AccessBlock,
			SSID:     r.Interface,
			Source:   ID,
			RawRef:   r.InstID,
			ByKergui: wlanRuleByKergui(r),
		})
	}
	return out, nil
}

// wifiRefusals reads the rules that refuse a device: those bound to an AP
// whose policy is Ban. On an AP in Allow a rule admits; in Disabled it does
// nothing.
func (g *Gateway) wifiRefusals(ctx context.Context) ([]wlanRule, error) {
	var out []wlanRule
	err := g.withSessionRetry(ctx, "Wi-Fi rules read", func() error {
		out = nil
		acl, err := g.readACL(ctx)
		if err != nil {
			return err
		}
		for _, r := range acl.rules {
			if acl.mode(r.Interface) == domain.WiFiModeBan {
				out = append(out, r)
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
		body := wlanRuleForm("Apply", wlanRule{InstID: "-1", Name: wlanRuleName(mac, ap), Interface: ap, MAC: mac}, g.sessionToken)
		if err := g.postSigned(ctx, "/?_type=menuData&_tag="+wlanRuleTag, body, domain.ErrBlockFailed); err != nil {
			if errors.Is(err, errBoxFull) {
				// Full: whatever this loop added before stays, as a
				// second try would add it anyway.
				n := distinctMACs(rules)
				return &domain.AccessListFullError{Kind: domain.AccessListWiFi, Limit: n, Used: n}
			}
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

// wifiUnblockOnce removes mac's rules on the APs in Ban, and only there: on
// an AP in Allow the rule admits the device, and removing an AP's last one
// would refuse everyone (RFC-007 W2 bis); on an AP in Disabled it is the
// allow list kept for the next close (D4).
func (g *Gateway) wifiUnblockOnce(ctx context.Context, mac domain.MAC) error {
	acl, err := g.readACL(ctx)
	if err != nil {
		return err
	}
	// The router renumbers its rules after each deletion (DEV.ACL3 becomes
	// DEV.ACL2), or leaves holes, so the list is re-read before every delete
	// instead of deleting from one stale snapshot.
	removed := 0
	for attempt := 0; attempt < 16; attempt++ {
		rules, err := g.wlanRules(ctx)
		if err != nil {
			return err
		}
		var target *wlanRule
		for i := range rules {
			if rules[i].MAC.Equal(mac) && acl.mode(rules[i].Interface) == domain.WiFiModeBan {
				target = &rules[i]
				break
			}
		}
		if target == nil {
			if removed == 0 {
				g.log.Infof("f6600p: %s has no Wi-Fi refusal, nothing to unblock", mac)
			}
			return nil
		}
		if err := g.loadView(ctx, wlanAdvancedView); err != nil {
			return err
		}
		body := wlanRuleForm("Delete", *target, g.sessionToken)
		if err := g.postSigned(ctx, "/?_type=menuData&_tag="+wlanRuleTag, body, domain.ErrUnblockFailed); err != nil {
			return err
		}
		removed++
	}
	return fmt.Errorf("%w: Wi-Fi rules for %s keep coming back", domain.ErrUnblockFailed, mac)
}

func distinctMACs(rules []wlanRule) int {
	seen := map[string]bool{}
	for _, r := range rules {
		seen[r.MAC.String()] = true
	}
	return len(seen)
}

// wlanRuleByKergui reports whether the rule carries the name wlanRuleName
// gives it: "<mac> APn", its own MAC and its own access point. Any other
// name was typed by the household in the box's page.
func wlanRuleByKergui(r wlanRule) bool {
	macPart, apPart, ok := strings.Cut(strings.TrimSpace(r.Name), " ")
	if !ok {
		return false
	}
	m, err := domain.ParseMAC(macPart)
	return err == nil && m.Equal(r.MAC) && wlanRuleName(r.MAC, r.Interface) == r.MAC.String()+" "+apPart
}

// wlanRuleName names a rule after the device and the access point: the router
// refuses two rules with the same name, even on different access points.
func wlanRuleName(mac domain.MAC, ap string) string {
	short := ap
	if i := strings.LastIndex(ap, "."); i >= 0 {
		short = ap[i+1:]
	}
	return mac.String() + " " + short // at most 17+1+4 characters, under the 32 allowed
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

// ---------------------------------------------------------------------------
// The closed Wi-Fi (RFC-007 D4, D7): port.WiFiAllowList.
//
// The trap: a rule refuses on an AP in Ban and admits on an AP in Allow, so an
// AP is never switched between the two with rules on it. ApplyWiFiLock
// converges the target APs to a target state (mode + rule set) rather than
// running a script, so a run cut short (the relay and the phone take the one
// admin session from each other, #94) is finished, or undone, by the next
// writer, from whatever state it left:
//
//  1. snapshot the modes and the whole table; refuse a target the table
//     cannot hold (WiFiLockBox.CheckFits) before any write;
//  2. an AP whose mode must change goes to Disabled first, one policy POST
//     (an AP already in the target mode, or already Disabled, stays);
//  3. the rules: on an AP held in Allow, the missing ones are added before the
//     extra ones go (the last rule of an AP in Allow removed refuses
//     everyone, W2 bis); elsewhere the extra ones go first (they free room),
//     the table re-read before each delete (the box renumbers, or leaves
//     holes, R6);
//  4. read the table back: each target AP holds exactly the target devices,
//     this phone included (the use case puts it in the target);
//  5. the target mode, one policy POST, never Allow on an empty list (W2:
//     the box would filter nothing), then read modes and rules back.
//
// Any failure converges back to the snapshot the same way, and reads back.
//
// Measured live on 2026-11-27 (W3, W4): a rule added in Allow and the mode
// flips take effect at once; a station that is not listed is dropped about
// 40 s after the switch to Allow.
// ---------------------------------------------------------------------------

// wlanACL is one read of the Wi-Fi access control: the policy rows as the
// box serves them (the policy form repeats them all) and the shared table.
type wlanACL struct {
	policies []map[string]string
	rules    []wlanRule
}

// has reports whether the box has the access point ap.
func (a wlanACL) has(ap string) bool {
	for _, p := range a.policies {
		if p["_InstID"] == ap {
			return true
		}
	}
	return false
}

// mode is ap's mode, Disabled when the box leaves it blank, "" when the box
// has no such AP.
func (a wlanACL) mode(ap string) string {
	for _, p := range a.policies {
		if p["_InstID"] == ap {
			return orDefault(p["ACLPolicy"], domain.WiFiModeDisabled)
		}
	}
	return ""
}

// box is the domain's view of this read: every AP the policy page lists, the
// rules by AP, and the measured table size.
func (a wlanACL) box() domain.WiFiLockBox {
	b := domain.WiFiLockBox{Rules: map[string][]domain.MAC{}, Limit: wifiRuleLimit, Used: len(a.rules)}
	for _, p := range a.policies {
		b.APs = append(b.APs, domain.WiFiLockAP{ID: p["_InstID"], Mode: a.mode(p["_InstID"])})
	}
	for _, r := range a.rules {
		b.Rules[r.Interface] = append(b.Rules[r.Interface], r.MAC)
	}
	return b
}

// on returns the devices listed on ap.
func (a wlanACL) on(ap string) []domain.MAC {
	var out []domain.MAC
	for _, r := range a.rules {
		if r.Interface == ap {
			out = append(out, r.MAC)
		}
	}
	return out
}

// readACL reads the policies and the table, from the access-control view.
func (g *Gateway) readACL(ctx context.Context) (wlanACL, error) {
	if err := g.loadView(ctx, wlanAdvancedView); err != nil {
		return wlanACL{}, err
	}
	data, err := g.wlanData(ctx, wlanPolicyTag)
	if err != nil {
		return wlanACL{}, err
	}
	policies, ok := parseInstances(data, "WLANAP")
	if !ok || len(policies) == 0 {
		return wlanACL{}, fmt.Errorf("%w: Wi-Fi policy: %s", domain.ErrSessionExpired, truncate(data, 300))
	}
	rules, err := g.wlanRules(ctx)
	if err != nil {
		return wlanACL{}, err
	}
	return wlanACL{policies: policies, rules: rules}, nil
}

// WiFiLockBox implements port.WiFiAllowList: the enabled APs with their name,
// band and mode, every rule of the shared table by AP, and its size.
func (g *Gateway) WiFiLockBox(ctx context.Context) (domain.WiFiLockBox, error) {
	var out domain.WiFiLockBox
	err := g.withSessionRetry(ctx, "Wi-Fi lock read", func() error {
		status, bandOf, err := g.wlanStatus(ctx)
		if err != nil {
			return err
		}
		acl, err := g.readACL(ctx)
		if err != nil {
			return err
		}
		out = acl.box()
		out.APs = nil
		for _, ap := range status {
			if ap["Enable"] != "1" {
				continue
			}
			id := ap["_InstID"]
			out.APs = append(out.APs, domain.WiFiLockAP{
				ID: id, Band: bandOf[ap["WLANViewName"]], Name: ap["ESSID"], Mode: orDefault(acl.mode(id), domain.WiFiModeDisabled),
			})
		}
		return nil
	})
	return out, err
}

// apTarget is what one AP must end as: its mode and its rules (the device and
// the name; the AP is ap).
type apTarget struct {
	ap    string
	mode  string
	rules []wlanRule
}

// wants reports whether mac is one of the AP's target devices.
func (t apTarget) wants(mac domain.MAC) bool {
	for _, r := range t.rules {
		if r.MAC.Equal(mac) {
			return true
		}
	}
	return false
}

// ApplyWiFiLock implements port.WiFiAllowList (see the steps above).
func (g *Gateway) ApplyWiFiLock(ctx context.Context, t domain.WiFiLockTarget) error {
	if err := t.Validate(); err != nil {
		return err
	}
	var snap wlanACL
	err := g.withSessionRetry(ctx, "Wi-Fi lock snapshot", func() error {
		var err error
		snap, err = g.readACL(ctx)
		return err
	})
	if err != nil {
		return err
	}
	for _, ap := range t.APs {
		if !snap.has(ap) {
			return fmt.Errorf("%w: the box has no access point %q", domain.ErrInvalidInput, ap)
		}
	}
	if err := snap.box().CheckFits(t); err != nil {
		return err
	}

	want := make([]apTarget, 0, len(t.APs))
	back := make([]apTarget, 0, len(t.APs))
	for _, ap := range t.APs {
		w := apTarget{ap: ap, mode: t.Mode}
		for _, m := range t.MACs {
			w.rules = append(w.rules, wlanRule{MAC: m, Interface: ap, Name: wlanRuleName(m, ap)})
		}
		want = append(want, w)
		b := apTarget{ap: ap, mode: snap.mode(ap)}
		for _, r := range snap.rules {
			if r.Interface == ap {
				b.rules = append(b.rules, r)
			}
		}
		back = append(back, b)
	}

	g.log.Infof("f6600p: Wi-Fi lock: %v to %s with %d devices", t.APs, t.Mode, len(t.MACs))
	err = g.withSessionRetry(ctx, "Wi-Fi lock", func() error { return g.converge(ctx, want, t.APs) })
	if err == nil {
		g.log.Infof("f6600p: Wi-Fi lock: %v in %s, verified on the router", t.APs, t.Mode)
		return nil
	}
	g.log.Errorf("f6600p: Wi-Fi lock failed (%v), back to the snapshot", err)
	if rbErr := g.withSessionRetry(ctx, "Wi-Fi lock rollback", func() error { return g.converge(ctx, back, t.APs) }); rbErr != nil {
		g.log.Errorf("f6600p: Wi-Fi lock rollback failed: %v", rbErr)
		return fmt.Errorf("%w; and the return to the previous state failed: %w", err, rbErr)
	}
	g.log.Infof("f6600p: Wi-Fi lock: back to the snapshot, verified on the router")
	return err
}

// converge brings the APs of plan to their target, from whatever state the
// box is in (see the steps above). aps are the target's APs, for a full
// answer counted in places.
func (g *Gateway) converge(ctx context.Context, plan []apTarget, aps []string) error {
	acl, err := g.readACL(ctx)
	if err != nil {
		return err
	}
	// Step 2: an AP that must change mode filters nothing while its rules
	// change, so no rule is ever read the wrong way.
	held := map[string]string{}
	for _, p := range plan {
		cur := acl.mode(p.ap)
		if cur == p.mode || cur == domain.WiFiModeDisabled {
			held[p.ap] = cur
		} else {
			held[p.ap] = domain.WiFiModeDisabled
		}
	}
	if err := g.setModes(ctx, acl, held); err != nil {
		return err
	}

	// Step 3.
	inAllow := func(p apTarget) bool { return held[p.ap] == domain.WiFiModeAllow }
	if err := g.deleteExtras(ctx, plan, func(p apTarget) bool { return !inAllow(p) }); err != nil {
		return err
	}
	if err := g.addMissing(ctx, plan, inAllow, aps); err != nil {
		return err
	}
	if err := g.deleteExtras(ctx, plan, inAllow); err != nil {
		return err
	}

	// Step 4.
	if acl, err = g.readACL(ctx); err != nil {
		return err
	}
	if err := acl.holds(plan); err != nil {
		return err
	}

	// Step 5.
	final := map[string]string{}
	var toAllow []string
	for _, p := range plan {
		final[p.ap] = p.mode
		if p.mode == domain.WiFiModeAllow && acl.mode(p.ap) != domain.WiFiModeAllow {
			toAllow = append(toAllow, p.ap)
		}
	}
	if len(toAllow) > 0 {
		if err := acl.box().CheckModeSwitch(toAllow, domain.WiFiModeAllow); err != nil {
			return err
		}
	}
	if err := g.setModes(ctx, acl, final); err != nil {
		return err
	}
	if acl, err = g.readACL(ctx); err != nil {
		return err
	}
	for _, p := range plan {
		if got := acl.mode(p.ap); got != p.mode {
			return fmt.Errorf("%w: %s is in %s after the write, want %s", domain.ErrUnexpectedResponse, p.ap, got, p.mode)
		}
	}
	return acl.holds(plan)
}

// holds reports whether each AP of plan lists exactly its target devices,
// naming the ones missing (this phone's absence would refuse it once
// closed) and the ones in excess.
func (a wlanACL) holds(plan []apTarget) error {
	for _, p := range plan {
		got := a.on(p.ap)
		var missing, extra []string
		for _, r := range p.rules {
			if !slices.ContainsFunc(got, r.MAC.Equal) {
				missing = append(missing, r.MAC.String())
			}
		}
		for _, m := range got {
			if !p.wants(m) {
				extra = append(extra, m.String())
			}
		}
		if len(missing) > 0 || len(extra) > 0 {
			return fmt.Errorf("%w: the Wi-Fi list of %s does not match after the write (missing %v, in excess %v)",
				domain.ErrUnexpectedResponse, p.ap, missing, extra)
		}
	}
	return nil
}

// setModes posts the policy form with the APs of modes switched, the others
// as they are, when one of them changes.
func (g *Gateway) setModes(ctx context.Context, acl wlanACL, modes map[string]string) error {
	next := map[string]string{}
	changed := false
	for _, p := range acl.policies {
		id := p["_InstID"]
		next[id] = acl.mode(id)
		if m, ok := modes[id]; ok && m != next[id] {
			next[id] = m
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := g.loadView(ctx, wlanAdvancedView); err != nil {
		return err
	}
	g.log.Infof("f6600p: Wi-Fi modes %v", modes)
	body := wlanPolicyForm(acl.policies, next, g.sessionToken)
	return g.postSigned(ctx, "/?_type=menuData&_tag="+wlanPolicyTag, body, domain.ErrUnexpectedResponse)
}

// deleteExtras removes, on the APs of plan that pick selects, every rule of
// a device the AP's target does not hold, re-reading the table before each
// delete.
func (g *Gateway) deleteExtras(ctx context.Context, plan []apTarget, pick func(apTarget) bool) error {
	for range 4 * wifiRuleLimit {
		rules, err := g.wlanRules(ctx)
		if err != nil {
			return err
		}
		victim := extraRule(rules, plan, pick, false)
		if victim == nil {
			return nil
		}
		if err := g.deleteWLANRule(ctx, *victim); err != nil {
			return err
		}
	}
	return fmt.Errorf("%w: the Wi-Fi rules keep coming back", domain.ErrUnexpectedResponse)
}

// extraRule finds a rule on an AP of plan that pick selects whose device the
// target does not hold. With keepOne, it is never an AP's last rule.
func extraRule(rules []wlanRule, plan []apTarget, pick func(apTarget) bool, keepOne bool) *wlanRule {
	for _, p := range plan {
		if !pick(p) {
			continue
		}
		on := 0
		for _, r := range rules {
			if r.Interface == p.ap {
				on++
			}
		}
		for i := range rules {
			if rules[i].Interface == p.ap && !p.wants(rules[i].MAC) && (!keepOne || on > 1) {
				return &rules[i]
			}
		}
	}
	return nil
}

func (g *Gateway) deleteWLANRule(ctx context.Context, r wlanRule) error {
	if err := g.loadView(ctx, wlanAdvancedView); err != nil {
		return err
	}
	body := wlanRuleForm("Delete", r, g.sessionToken)
	return g.postSigned(ctx, "/?_type=menuData&_tag="+wlanRuleTag, body, domain.ErrUnexpectedResponse)
}

// addMissing adds each target device an AP of plan does not list. On an AP
// held in Allow, with the table full, an extra rule on such an AP goes first
// (never its last). A « plein » answer is an *AccessListFullError counted in
// places over aps, as the box holds them.
func (g *Gateway) addMissing(ctx context.Context, plan []apTarget, inAllow func(apTarget) bool, aps []string) error {
	rules, err := g.wlanRules(ctx)
	if err != nil {
		return err
	}
	for _, p := range plan {
		for _, want := range p.rules {
			if slices.ContainsFunc(rules, func(r wlanRule) bool { return r.Interface == p.ap && r.MAC.Equal(want.MAC) }) {
				continue
			}
			if inAllow(p) && len(rules) >= wifiRuleLimit {
				if victim := extraRule(rules, plan, inAllow, true); victim != nil {
					if err := g.deleteWLANRule(ctx, *victim); err != nil {
						return err
					}
				}
			}
			if err := g.loadView(ctx, wlanAdvancedView); err != nil {
				return err
			}
			r := wlanRule{InstID: "-1", Name: want.Name, Interface: p.ap, MAC: want.MAC}
			err := g.postSigned(ctx, "/?_type=menuData&_tag="+wlanRuleTag, wlanRuleForm("Apply", r, g.sessionToken), domain.ErrUnexpectedResponse)
			if errors.Is(err, errBoxFull) {
				return g.fullError(ctx, aps, planDevices(plan))
			}
			if err != nil {
				return err
			}
			if rules, err = g.wlanRules(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// planDevices counts the distinct devices plan lists.
func planDevices(plan []apTarget) int {
	seen := map[string]bool{}
	for _, p := range plan {
		for _, r := range p.rules {
			seen[r.MAC.String()] = true
		}
	}
	return len(seen)
}

// fullError turns the box's « plein » into places: the table holds as many
// rules as it can, minus those on the APs that stay open, shared by aps.
func (g *Gateway) fullError(ctx context.Context, aps []string, used int) error {
	rules, err := g.wlanRules(ctx)
	if err != nil {
		return err
	}
	shut := map[string]bool{}
	for _, ap := range aps {
		shut[ap] = true
	}
	open := 0
	for _, r := range rules {
		if !shut[r.Interface] {
			open++
		}
	}
	places, _ := domain.WiFiPlaces(len(rules), open, len(aps))
	return &domain.AccessListFullError{Kind: domain.AccessListWiFi, Limit: places, Used: used}
}

// SetWiFiMode implements port.WiFiAllowList: one policy POST, the rules kept
// (open light, the add window, the re-close), then read back. It refuses
// Allow on an AP with no rule (W2), and a flip straight between Ban and
// Allow, which would read every rule the other way (ApplyWiFiLock rewrites
// the rules for that).
func (g *Gateway) SetWiFiMode(ctx context.Context, aps []string, mode string) error {
	return g.withSessionRetry(ctx, "Wi-Fi mode", func() error {
		acl, err := g.readACL(ctx)
		if err != nil {
			return err
		}
		if err := acl.box().CheckModeSwitch(aps, mode); err != nil {
			return err
		}
		modes := map[string]string{}
		for _, ap := range aps {
			cur := acl.mode(ap)
			if (cur == domain.WiFiModeBan && mode == domain.WiFiModeAllow) || (cur == domain.WiFiModeAllow && mode == domain.WiFiModeBan) {
				return fmt.Errorf("%w: %s is in %s, its rules would mean the opposite in %s", domain.ErrInvalidInput, ap, cur, mode)
			}
			modes[ap] = mode
		}
		if err := g.setModes(ctx, acl, modes); err != nil {
			return err
		}
		if acl, err = g.readACL(ctx); err != nil {
			return err
		}
		for _, ap := range aps {
			if got := acl.mode(ap); got != mode {
				return fmt.Errorf("%w: %s is in %s after the write, want %s", domain.ErrUnexpectedResponse, ap, got, mode)
			}
		}
		g.log.Infof("f6600p: Wi-Fi %v in %s, verified on the router", aps, mode)
		return nil
	})
}
