// Package ztef6600p implements the router adapter for the ZTE ZXHN F6600P
// (Orange Livebox Fibre). This model uses a completely different web interface
// from the older F660 family: HTTPS with self-signed cert, AJAX XML data via
// Lua pages, sha256(password+token) authentication, and a menuView-preload
// requirement for data pages. It implements port.RouterPort directly rather
// than using the zte.Profile/Gateway, since the protocol is too different.
//
// Protocol summary (observed 2026-10-10, firmware ZTEGF660006SN):
//   - Login: GET login_entry → sess_token; GET login_token → numeric token;
//     POST login_entry with sha256(password+token)
//   - Session: cookie SID_HTTPS_, no _sessionTOKEN needed for GET
//   - Data: GET menuView/<pageId> first (preload), then GET menuData/<area_lua.lua>
//   - Format: XML <ajax_response_xml_root> with <OBJ_*_ID>/<Instance>/<ParaName>/<ParaValue>
//   - Write: POST form fields (DOM order) + _sessionTOKEN taken from the
//     menuView's inline `_sessionTmpToken = "\x.."`, with header
//     Check = base64(RSA_PKCS1v15(sha256hex(body))); IF_ACTION=Apply|Delete
package ztef6600p

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/httpkit"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

const (
	ID            = "zte_f6600p"
	sessionCookie = "SID_HTTPS_"
	macFilterURL  = "/?_type=menuData&_tag=firewall_macfilterv3_lua.lua"

	filterGlobalTag = "firewall_filterglobal_lua.lua"
)

type Gateway struct {
	sess         *httpkit.Session
	opts         port.RouterOptions
	creds        domain.Credentials // retained for automatic session refresh
	sessionToken string             // _sessionTOKEN for POST requests
	rsaPubKey    *rsa.PublicKey     // extracted from the main page JS
	forced       *forcedPassword    // set while the router demands a new password (firstpassword.go)
	log          port.Logger
}

var (
	_ port.RouterPort          = (*Gateway)(nil)
	_ port.MACFilterSwitch     = (*Gateway)(nil)
	_ port.MACFilterModeReader = (*Gateway)(nil)
	_ port.TrafficMeter        = (*Gateway)(nil)
)

func New(baseURL string, opts port.RouterOptions) (*Gateway, error) {
	s, err := httpkit.NewSession(baseURL, opts)
	if err != nil {
		return nil, err
	}
	log := opts.Logger
	if log == nil {
		log = nopLogger{}
	}
	return &Gateway{sess: s, opts: opts, log: log}, nil
}

func Register(f *router.Factory) {
	f.Register(Meta(), func(baseURL string, opts port.RouterOptions) (port.RouterPort, error) {
		return New(baseURL, opts)
	})
}

func Meta() domain.AdapterMeta {
	return domain.AdapterMeta{
		ID:       ID,
		Vendor:   "ZTE",
		Models:   []string{"F6600P", "ZXHN F6600P"},
		Verified: true,
		Markers:  []string{"f6600p", "zxhn f6600p", "ZTEGF6600P", "&#70;&#54;&#54;&#48;&#48;&#80;"},
		Endpoints: []domain.EndpointDoc{
			{Operation: "login", Method: "POST", Path: "/?_type=loginData&_tag=login_entry", Verified: true, Notes: "sha256(password+token)"},
			{Operation: "router_info", Method: "GET", Path: "/?_type=menuData&_tag=devmgr_statusmgr_lua.lua", Verified: true},
			{Operation: "devices", Method: "GET", Path: "/?_type=menuData&_tag=wlan_client_stat_lua.lua", Verified: true},
			{Operation: "access_rules", Method: "GET", Path: macFilterURL, Verified: true},
			{Operation: "block", Method: "POST", Path: macFilterURL, Verified: true, Notes: "IF_ACTION=Apply + RSA Check header; validated live 2026-10-10, fw ZTEGF660006SN"},
			{Operation: "unblock", Method: "POST", Path: macFilterURL, Verified: true, Notes: "IF_ACTION=Delete + RSA Check header; validated live 2026-10-10, fw ZTEGF660006SN"},
			// Wi-Fi access control ("Couper l'accès", wifi.go): the access point
			// refuses the station, unlike block, which only cuts its Internet.
			// Not yet confirmed on a live box by the owner: Verified stays false.
			{Operation: "wifi_blocked", Method: "GET", Path: "/?_type=menuData&_tag=" + wlanRuleTag, Verified: true, Notes: "after the wlanAdvanced view; rules (OBJ_ACLCFG_ID) count only on APs whose " + wlanPolicyTag + " ACLPolicy is Ban; validated live 2026-11-27 (RFC-007 W8, read-back of WiFiUnblock), fw ZTEGF660006SN"},
			{Operation: "wifi_block", Method: "POST", Path: "/?_type=menuData&_tag=" + wlanRuleTag, Verified: false, Notes: "IF_ACTION=Apply, one rule per enabled AP + RSA Check header; then " + wlanPolicyTag + " ACLPolicy=Ban; refused on an AP in Allow mode; reread to confirm. 2026-11-27: the same rule and policy forms refused a phone at association on a test AP (RFC-007 W8), but WiFiBlock itself was not run: the table was full (63/64)"},
			{Operation: "wifi_unblock", Method: "POST", Path: "/?_type=menuData&_tag=" + wlanRuleTag, Verified: true, Notes: "IF_ACTION=Delete per rule, list reread before each delete (the box renumbers); reread to confirm; validated live 2026-11-27 on a test AP in Ban (RFC-007 W8), fw ZTEGF660006SN"},
			// The closed Wi-Fi (RFC-007 D4, D7, wifi.go): an AP in Allow admits
			// only the devices listed on it.
			{Operation: "wifi_lock", Method: "GET", Path: "/?_type=menuData&_tag=" + wlanPolicyTag, Verified: false, Notes: "after the wlanAdvanced view: ACLPolicy per AP (Disabled/Ban/Allow) and the shared rule table of " + wlanRuleTag + " (64 rules for every AP); enabled APs, names and bands from " + wlanStatusTag + ". 2026-11-27 (RFC-007 W1-W4): the table size and the Allow mode measured live on a test AP; WiFiLockBox itself not yet run on a live box"},
			{Operation: "wifi_lock_write", Method: "POST", Path: "/?_type=menuData&_tag=" + wlanPolicyTag, Verified: false, Notes: "ApplyWiFiLock: snapshot and capacity check, APs changing mode to Disabled first, rules rewritten on " + wlanRuleTag + " (re-read before each delete; in Allow, added before deleted), read-back, then the mode, read-back; back to the snapshot on any failure. SetWiFiMode: one policy POST, never Ban<->Allow, never Allow on an empty list. 2026-11-27 (RFC-007 W2-W4): an empty Allow filters nothing, the last rule removed in Allow refuses everyone, adds and mode flips take effect at once, strangers dropped ~40 s after the switch; ApplyWiFiLock itself not yet run on a live box"},
			{Operation: "wifi_ssids", Method: "GET", Path: "/?_type=menuData&_tag=" + wlanSSIDTag, Verified: true, Notes: "after the wlanBasic view; secrets AES-decrypted with its _sessionTmpToken; read live 2026-10-12"},
			{Operation: "admin_password_first", Method: "POST", Path: changePwdURL, Verified: false, Notes: "only when the box demands it after the first sign-in: action=changepwd, Password/NewPassword AES-encrypted + RSA 'encode' key, _sessionTOKEN from doChgpwd; page captured 2026-11-25, change not yet validated live"},
			{Operation: "wifi_ssid_set", Method: "POST", Path: "/?_type=menuData&_tag=" + wlanSSIDTag, Verified: true, Notes: "whole template_WLANSSIDConf form, secrets AES-encrypted + RSA 'encode' key; on/off, rename and password validated live by the owner 2026-11"},
		},
	}
}

func (g *Gateway) Meta() domain.AdapterMeta { return Meta() }

func (g *Gateway) Login(ctx context.Context, creds domain.Credentials) error {
	g.forced = nil
	// Step 1: get session token
	body, status, err := g.sess.Get(ctx, "/?_type=loginData&_tag=login_entry")
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	if status != 200 {
		return fmt.Errorf("%w: login_entry returned %d", domain.ErrUnexpectedResponse, status)
	}

	var sessResp struct {
		SessToken string `json:"sess_token"`
	}
	if err := json.Unmarshal(body, &sessResp); err != nil {
		return fmt.Errorf("%w: cannot parse session response: %v", domain.ErrUnexpectedResponse, err)
	}

	// Step 2: get numeric login token
	tokenBody, status, err := g.sess.Get(ctx, "/?_type=loginData&_tag=login_token")
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	if status != 200 {
		return fmt.Errorf("%w: login_token returned %d", domain.ErrUnexpectedResponse, status)
	}
	token := parseLoginToken(string(tokenBody))
	if token == "" {
		return fmt.Errorf("%w: empty login token", domain.ErrUnexpectedResponse)
	}

	// Step 3: sha256(password + token) and POST login
	hash := sha256hex(creds.Password + token)
	loginForm := fmt.Sprintf("action=login&Username=%s&Password=%s&_sessionTOKEN=%s",
		creds.Username, hash, sessResp.SessToken)

	body, status, err = g.sess.PostRaw(ctx, "/?_type=loginData&_tag=login_entry", loginForm)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	if status != 200 {
		return fmt.Errorf("%w: login POST returned %d", domain.ErrAuthFailed, status)
	}

	var loginResp struct {
		SessToken   string `json:"sess_token"`
		NeedRefresh bool   `json:"login_need_refresh"`
		LoginErrMsg string `json:"loginErrMsg"`
	}
	if err := json.Unmarshal(body, &loginResp); err != nil {
		return fmt.Errorf("%w: cannot parse login response", domain.ErrUnexpectedResponse)
	}
	if loginResp.LoginErrMsg != "" {
		g.log.Warnf("f6600p: login rejected: %s", loginResp.LoginErrMsg)
		return fmt.Errorf("%w: %s", domain.ErrAuthFailed, loginResp.LoginErrMsg)
	}

	// Step 4: load main page to establish full session, extract RSA key and _sessionTOKEN
	mainBody, _, err := g.sess.Get(ctx, "/")
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}

	mainHTML := string(mainBody)
	if isPreemptPage(mainHTML) {
		if mainHTML, err = g.preempt(ctx, mainHTML); err != nil {
			return err
		}
	}
	if isForcedPasswordPage(mainHTML) {
		// A new box signed into with its label password: the session stays
		// open for ChangeAdminPassword, nothing else works until then.
		fp, err := parseForcedPage(mainHTML)
		if err != nil {
			return err
		}
		g.forced = fp
		g.log.Warnf("f6600p: the router demands a new admin password before opening its interface")
		return domain.ErrPasswordChangeRequired
	}
	if pubKey := extractRSAPublicKey(mainHTML); pubKey != nil {
		g.rsaPubKey = pubKey
	}

	switch {
	case parseJSString(mainHTML, "_sessionTmpToken") != "":
		g.sessionToken = parseJSString(mainHTML, "_sessionTmpToken")
	case extractSessionTokenFromHTML(mainHTML) != "":
		g.sessionToken = extractSessionTokenFromHTML(mainHTML)
	default:
		g.sessionToken = loginResp.SessToken
	}
	g.log.Infof("f6600p: logged in as %s (session token len %d, RSA key found: %v)",
		creds.Username, len(g.sessionToken), g.rsaPubKey != nil)

	if !g.sess.HasCookie(sessionCookie) {
		g.log.Errorf("f6600p: login POST accepted but no %s cookie was set", sessionCookie)
		return domain.ErrAuthFailed
	}
	g.creds = creds
	return nil
}

// menuData loads a data page, preloading the menuView if needed.
// If the session has expired and credentials are available, it automatically
// re-logs in and retries once.
func (g *Gateway) menuData(ctx context.Context, viewTag, dataTag string) ([]byte, error) {
	body, err := g.menuDataOnce(ctx, viewTag, dataTag)
	if err != nil && errors.Is(err, domain.ErrSessionExpired) && g.creds.Username != "" {
		g.log.Infof("f6600p: session expired reading %s, logging in again", dataTag)
		if loginErr := g.Login(ctx, g.creds); loginErr != nil {
			return nil, fmt.Errorf("%w: session refresh failed: %w", domain.ErrSessionExpired, loginErr)
		}
		return g.menuDataOnce(ctx, viewTag, dataTag)
	}
	return body, err
}

func (g *Gateway) menuDataOnce(ctx context.Context, viewTag, dataTag string) ([]byte, error) {
	if _, _, err := g.sess.Get(ctx, "/?_type=menuView&_tag="+viewTag); err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}

	body, status, err := g.sess.Get(ctx, "/?_type=menuData&_tag="+dataTag)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	switch {
	case status == 200:
		root, parseErr := parseAjaxXML(string(body))
		if parseErr == nil && !isSuccess(root) {
			return nil, fmt.Errorf("%w: %s", domain.ErrSessionExpired, root.ErrorStr)
		}
		return body, nil
	case status == 401 || status == 403:
		return nil, domain.ErrSessionExpired
	default:
		return nil, fmt.Errorf("%w: status %d", domain.ErrUnexpectedResponse, status)
	}
}

func (g *Gateway) RouterInfo(ctx context.Context) (*domain.RouterInfo, error) {
	body, err := g.menuData(ctx, "statusMgr", "devmgr_statusmgr_lua.lua")
	if err != nil {
		return nil, err
	}
	model, fw, hw := parseRouterInfo(string(body))
	serial := parseSerialNumber(string(body))
	return &domain.RouterInfo{
		BaseURL:      g.sess.BaseURL(),
		Vendor:       "ZTE",
		Model:        model,
		Firmware:     fw,
		HardwareVer:  hw,
		SerialNumber: serial,
		AdapterID:    ID,
		Reachable:    true,
		DiscoveredAt: time.Now(),
	}, nil
}

// SerialNumber implements port.BoxIdentifier: the serial carried by the
// router_info response (verified on a real box, RFC-006), upper-cased.
func (g *Gateway) SerialNumber(ctx context.Context) (string, error) {
	body, err := g.menuData(ctx, "statusMgr", "devmgr_statusmgr_lua.lua")
	if err != nil {
		return "", err
	}
	serial := parseSerialNumber(string(body))
	if serial == "" {
		return "", fmt.Errorf("%w: router_info carries no serial number", domain.ErrUnexpectedResponse)
	}
	return serial, nil
}

func (g *Gateway) Devices(ctx context.Context) ([]domain.Device, error) {
	body, err := g.menuData(ctx, "localNetStatus", "wlan_client_stat_lua.lua")
	if err != nil {
		return nil, err
	}
	devs := parseWLANClients(string(body))

	// Cross-reference ACL to flag blocked devices
	if rules, err := g.AccessRules(ctx); err == nil {
		blocked := make(map[string]bool, len(rules))
		for _, r := range rules {
			if r.Mode == domain.AccessBlock {
				blocked[r.MAC.String()] = true
			}
		}
		for i := range devs {
			if blocked[devs[i].MAC.String()] {
				devs[i].Blocked = true
			}
		}
	}
	return devs, nil
}

// TrafficCounters implements port.TrafficMeter from the Wi-Fi client list.
func (g *Gateway) TrafficCounters(ctx context.Context) (map[domain.MAC]domain.TrafficCounters, error) {
	body, err := g.menuData(ctx, "localNetStatus", "wlan_client_stat_lua.lua")
	if err != nil {
		return nil, err
	}
	c := parseWLANCounters(string(body))
	if c == nil {
		return nil, fmt.Errorf("%w: cannot parse Wi-Fi client counters", domain.ErrUnexpectedResponse)
	}
	return c, nil
}

func (g *Gateway) AccessRules(ctx context.Context) ([]domain.AccessRule, error) {
	body, err := g.menuData(ctx, "filterCriteria", "firewall_macfilterv3_lua.lua")
	if err != nil {
		return nil, err
	}
	return parseMACFilter(string(body)), nil
}

func (g *Gateway) Block(ctx context.Context, mac domain.MAC) error {
	return g.changeACL(ctx, mac, true)
}

func (g *Gateway) Unblock(ctx context.Context, mac domain.MAC) error {
	return g.changeACL(ctx, mac, false)
}

func (g *Gateway) changeACL(ctx context.Context, mac domain.MAC, block bool) error {
	failErr := domain.ErrBlockFailed
	if !block {
		failErr = domain.ErrUnblockFailed
	}
	err := g.changeACLOnce(ctx, mac, block, failErr)
	if errors.Is(err, domain.ErrSessionExpired) && g.creds.Username != "" {
		g.log.Warnf("f6600p: session expired during write (%v), logging in again and retrying once", err)
		if loginErr := g.Login(ctx, g.creds); loginErr != nil {
			return fmt.Errorf("%w: session refresh failed: %w", domain.ErrSessionExpired, loginErr)
		}
		err = g.changeACLOnce(ctx, mac, block, failErr)
	}
	if err != nil {
		return err
	}
	if g.opts.SkipWriteVerify {
		g.log.Warnf("f6600p: write verification skipped for %s", mac)
		return nil
	}
	return g.verifyACL(ctx, mac, block, failErr)
}

// changeACLOnce reproduces what the router's own "Filtre MAC" page does: load
// the filterCriteria view (which injects the current _sessionTmpToken), read the
// rule list, then POST the form fields in DOM order with the RSA Check header.
func (g *Gateway) changeACLOnce(ctx context.Context, mac domain.MAC, block bool, failErr error) error {
	if err := g.loadWriteView(ctx); err != nil {
		return err
	}

	data, status, err := g.sess.Get(ctx, macFilterURL)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	g.log.Debugf("f6600p: MAC filter before write (HTTP %d): %s", status, truncate(string(data), 2000))
	if root, perr := parseAjaxXML(string(data)); perr == nil && !isSuccess(root) {
		return fmt.Errorf("%w: %s", domain.ErrSessionExpired, root.ErrorStr)
	}
	entries := parseMACFilterEntries(string(data))
	var existing *macFilterEntry
	for i := range entries {
		if entries[i].MAC.Equal(mac) {
			existing = &entries[i]
			break
		}
	}

	var postBody string
	if block {
		if existing != nil {
			g.log.Infof("f6600p: %s already in MAC filter (rule %s), nothing to do", mac, existing.InstID)
			return nil
		}
		postBody = macFilterForm("Apply", macFilterEntry{
			InstID: "-1", Name: mac.String(), MAC: mac, Type: "Route", Protocol: "ALL",
		}, g.sessionToken)
	} else {
		if existing == nil {
			g.log.Infof("f6600p: %s not in MAC filter, nothing to unblock", mac)
			return nil
		}
		postBody = macFilterForm("Delete", *existing, g.sessionToken)
	}
	err = g.postSigned(ctx, macFilterURL, postBody, failErr)
	if block && errors.Is(err, errBoxFull) {
		// One rule per device: the box is full at the count it holds.
		return &domain.AccessListFullError{Kind: domain.AccessListFirewall, Limit: len(entries), Used: len(entries)}
	}
	return err
}

// errBoxFull marks a write the box refused with one of its « plein » answers
// (fullAnswers). The caller, who knows which list it wrote to, turns it into
// a domain.AccessListFullError.
var errBoxFull = errors.New("the box says its list is full")

// Rule limits measured on a live F6600P on 2026-11-27 (RFC-007 W1, W7): the
// Wi-Fi access-control table holds 64 rules shared by every access point
// (one rule per MAC per AP), the firewall MAC filter 32 rules.
const (
	wifiRuleLimit     = 64
	firewallRuleLimit = 32
)

// fullAnswers are the IF_ERRORSTR texts, lower-cased, by which a box refuses
// an add because its list is full. Matched as substrings. The F6600P's own
// text, measured live on 2026-11-27 (RFC-007 W1 and W7, IF_ERRORID -12, the
// same for the Wi-Fi rule table and the firewall MAC filter), comes first.
var fullAnswers = []string{
	"le nombre d'entrées a atteint la limite maximale",
	"a atteint le maximum",
	"atteint la limite",
	"reached the maximum",
	"reached its maximum",
	"maximum number",
	"liste pleine",
	"list is full",
	"table is full",
}

// isFullAnswer reports whether the box's answer is one of fullAnswers.
func isFullAnswer(s string) bool {
	l := strings.ToLower(s)
	for _, a := range fullAnswers {
		if strings.Contains(l, a) {
			return true
		}
	}
	return false
}

// loadWriteView loads the filterCriteria view, which the router requires before
// a write and which injects the current _sessionTmpToken.
func (g *Gateway) loadWriteView(ctx context.Context) error {
	return g.loadView(ctx, "filterCriteria")
}

// loadView loads a menuView page (required before its data pages are usable)
// and picks up the _sessionTmpToken it injects.
func (g *Gateway) loadView(ctx context.Context, viewTag string) error {
	view, _, err := g.sess.Get(ctx, "/?_type=menuView&_tag="+viewTag)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	if strings.Contains(string(view), "SessionTimeout") {
		return fmt.Errorf("%w: %s view returned SessionTimeout", domain.ErrSessionExpired, viewTag)
	}
	if tok := parseJSString(string(view), "_sessionTmpToken"); tok != "" {
		g.sessionToken = tok
	} else {
		g.log.Warnf("f6600p: no _sessionTmpToken in %s view, reusing previous token (len %d)", viewTag, len(g.sessionToken))
	}
	return nil
}

// withSessionRetry runs op, and if the session expired, logs in again and runs
// it once more.
func (g *Gateway) withSessionRetry(ctx context.Context, what string, op func() error) error {
	err := op()
	if errors.Is(err, domain.ErrSessionExpired) && g.creds.Username != "" {
		g.log.Warnf("f6600p: session expired during %s (%v), logging in again and retrying once", what, err)
		if loginErr := g.Login(ctx, g.creds); loginErr != nil {
			return fmt.Errorf("%w: session refresh failed: %w", domain.ErrSessionExpired, loginErr)
		}
		err = op()
	}
	return err
}

// postSigned POSTs a form body with the RSA integrity Check header, as the web
// UI's dataPost does, and maps the router's verdict to an error.
func (g *Gateway) postSigned(ctx context.Context, target, postBody string, failErr error) error {
	if g.rsaPubKey == nil {
		return fmt.Errorf("%w: router RSA public key not found on the main page, cannot sign the request", failErr)
	}
	encrypted, err := rsaEncrypt(g.rsaPubKey, []byte(sha256hex(postBody)))
	if err != nil {
		return fmt.Errorf("%w: RSA encrypt failed: %v", failErr, err)
	}
	headers := map[string]string{
		"Check":            base64.StdEncoding.EncodeToString(encrypted),
		"X-Requested-With": "XMLHttpRequest",
	}

	g.log.Infof("f6600p: POST %s %s", target, redactToken(postBody))
	body, status, err := g.sess.PostRawWithHeaders(ctx, target, postBody, headers)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	g.log.Infof("f6600p: write response HTTP %d: %s", status, truncate(string(body), 1000))
	if status != 200 {
		return fmt.Errorf("%w: POST returned %d", failErr, status)
	}
	root, parseErr := parseAjaxXML(string(body))
	if parseErr == nil && !isSuccess(root) {
		if isExpiredMsg(root.ErrorStr) {
			return fmt.Errorf("%w: %s", domain.ErrSessionExpired, root.ErrorStr)
		}
		if isFullAnswer(root.ErrorStr) {
			return fmt.Errorf("%w: %w: %s", failErr, errBoxFull, root.ErrorStr)
		}
		return fmt.Errorf("%w: %s", failErr, root.ErrorStr)
	}
	return nil
}

// MACFilterEnabled implements port.MACFilterSwitch.
func (g *Gateway) MACFilterEnabled(ctx context.Context) (bool, error) {
	enabled, _, err := g.MACFilterMode(ctx)
	return enabled, err
}

// MACFilterMode implements port.MACFilterModeReader.
func (g *Gateway) MACFilterMode(ctx context.Context) (bool, string, error) {
	body, err := g.menuData(ctx, "filterCriteria", filterGlobalTag)
	if err != nil {
		return false, "", err
	}
	enabled, target := parseFilterGlobal(string(body))
	g.log.Infof("f6600p: MAC filter enabled=%v mode=%s", enabled, target)
	if enabled && target == "Permit" {
		g.log.Warnf("f6600p: MAC filter is in allowlist mode (Permit): only listed devices can connect")
	}
	return enabled, target, nil
}

// SetMACFilterEnabled implements port.MACFilterSwitch. Enabling always selects
// blocklist mode ("Discard"): allowlist mode would cut every unlisted device,
// including the machine running kergui.
func (g *Gateway) SetMACFilterEnabled(ctx context.Context, enabled bool) error {
	err := g.setFilterGlobalOnce(ctx, enabled)
	if errors.Is(err, domain.ErrSessionExpired) && g.creds.Username != "" {
		g.log.Warnf("f6600p: session expired while switching MAC filter, logging in again and retrying once")
		if loginErr := g.Login(ctx, g.creds); loginErr != nil {
			return fmt.Errorf("%w: session refresh failed: %w", domain.ErrSessionExpired, loginErr)
		}
		err = g.setFilterGlobalOnce(ctx, enabled)
	}
	if err != nil || g.opts.SkipWriteVerify {
		return err
	}
	got, err := g.MACFilterEnabled(ctx)
	if err != nil {
		return err
	}
	if got != enabled {
		return fmt.Errorf("%w: router accepted the request but MAC filter is still enabled=%v", domain.ErrUnexpectedResponse, got)
	}
	g.log.Infof("f6600p: verified MAC filter enabled=%v on the router", enabled)
	return nil
}

func (g *Gateway) setFilterGlobalOnce(ctx context.Context, enabled bool) error {
	if err := g.loadWriteView(ctx); err != nil {
		return err
	}
	data, _, err := g.sess.Get(ctx, "/?_type=menuData&_tag="+filterGlobalTag)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	cur := parseFilterGlobalFields(string(data))
	if cur == nil {
		return fmt.Errorf("%w: cannot read current filter settings: %s", domain.ErrSessionExpired, truncate(string(data), 300))
	}
	g.log.Debugf("f6600p: filter settings before write: %v", cur)
	instID := cur["_InstID"]
	if instID == "" {
		instID = "IGD"
	}
	on := "0"
	if enabled {
		on = "1"
	}
	// Same field order as the SecurityGlobalCtl form; URL filter values are
	// re-posted unchanged.
	var b strings.Builder
	add := func(k, v string) { b.WriteString("&" + k + "=" + encodeURIComponent(v)) }
	b.WriteString("IF_ACTION=Apply")
	add("_InstID", instID)
	add("MacFilterEnable", on)
	add("MacFilterTarget", "Discard")
	add("UrlFilterEnable", orDefault(cur["UrlFilterEnable"], "0"))
	add("UrlFilterTarget", orDefault(cur["UrlFilterTarget"], "0"))
	b.WriteString("&_sessionTOKEN=" + g.sessionToken)
	return g.postSigned(ctx, "/?_type=menuData&_tag="+filterGlobalTag, b.String(), domain.ErrUnexpectedResponse)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// macFilterForm builds the body exactly as the web UI's InitialPostData does for
// the MAC filter template: inputs in DOM order, PostIgnore fields skipped
// (DstMacAddr in Route mode), URI-component encoding, token appended last.
func macFilterForm(action string, e macFilterEntry, token string) string {
	octets := strings.Split(strings.ToUpper(e.MAC.String()), ":")
	macStr := strings.Join(octets, ":")
	proto := e.Protocol
	if proto == "" {
		proto = "ALL"
	}
	typ := e.Type
	if typ == "" {
		typ = "Route"
	}
	var b strings.Builder
	add := func(k, v string) { b.WriteString("&" + k + "=" + encodeURIComponent(v)) }
	b.WriteString("IF_ACTION=" + action)
	add("_InstID", e.InstID)
	add("SrcMacAddr", macStr)
	add("Protocol", proto)
	add("Name", e.Name)
	add("Type", typ)
	add("select_protocol", proto)
	for i, o := range octets {
		add(fmt.Sprintf("sub_SrcMacAddr%d", i), o)
	}
	for i := 0; i < 6; i++ {
		add(fmt.Sprintf("sub_DstMacAddr%d", i), "")
	}
	b.WriteString("&_sessionTOKEN=" + token)
	return b.String()
}

func encodeURIComponent(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func isExpiredMsg(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "expir") || strings.Contains(l, "sessiontimeout") || strings.Contains(l, "timeout")
}

func redactToken(body string) string {
	if i := strings.Index(body, "_sessionTOKEN="); i >= 0 {
		return body[:i] + "_sessionTOKEN=<redacted>"
	}
	return body
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// verifyACL rereads the ACL and confirms the expected state.
func (g *Gateway) verifyACL(ctx context.Context, mac domain.MAC, want bool, failErr error) error {
	rules, err := g.AccessRules(ctx)
	if err != nil {
		if errors.Is(err, domain.ErrNotImplemented) {
			return nil
		}
		return err
	}
	present := false
	for _, r := range rules {
		if r.MAC.Equal(mac) && r.Mode == domain.AccessBlock {
			present = true
			break
		}
	}
	if present != want {
		g.log.Errorf("f6600p: verification failed for %s: want blocked=%v, router ACL has %d rule(s)", mac, want, len(rules))
		return fmt.Errorf("%w: router accepted the request but the ACL does not reflect it", failErr)
	}
	g.log.Infof("f6600p: verified %s blocked=%v on the router", mac, want)
	return nil
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// extractRSAPublicKey parses the PEM-encoded RSA public key from the main page JS.
func extractRSAPublicKey(html string) *rsa.PublicKey {
	const marker = "-----BEGIN PUBLIC KEY-----"
	start := strings.Index(html, marker)
	if start < 0 {
		return nil
	}
	end := strings.Index(html[start:], "-----END PUBLIC KEY-----")
	if end < 0 {
		return nil
	}
	pemStr := html[start : start+end+len("-----END PUBLIC KEY-----")]
	pemStr = strings.ReplaceAll(pemStr, `\n`, "\n")

	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil
	}
	return rsaPub
}

// rsaEncrypt encrypts data with PKCS#1 v1.5 (matching JSEncrypt behavior).
func rsaEncrypt(pub *rsa.PublicKey, data []byte) ([]byte, error) {
	return rsa.EncryptPKCS1v15(rand.Reader, pub, data)
}

// extractSessionTokenFromHTML extracts the _sessionTOKEN value from the main page's
// hidden input: <input type="hidden" name="_sessionTOKEN" id="_sessionTOKEN" value="..."/>
func extractSessionTokenFromHTML(html string) string {
	const idMarker = `id="_sessionTOKEN"`
	idx := strings.Index(html, idMarker)
	if idx < 0 {
		return ""
	}
	// Search for value="..." in the surrounding <input> tag
	start := strings.LastIndex(html[:idx], "<")
	if start < 0 {
		return ""
	}
	tagEnd := strings.Index(html[idx:], ">")
	if tagEnd < 0 {
		return ""
	}
	tag := html[start : idx+tagEnd+1]
	const valPrefix = `value="`
	vi := strings.Index(tag, valPrefix)
	if vi < 0 {
		return ""
	}
	rest := tag[vi+len(valPrefix):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

type nopLogger struct{}

func (nopLogger) Debugf(string, ...any) {}
func (nopLogger) Infof(string, ...any)  {}
func (nopLogger) Warnf(string, ...any)  {}
func (nopLogger) Errorf(string, ...any) {}
