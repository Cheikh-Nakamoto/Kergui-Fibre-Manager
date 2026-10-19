package mockrouter

// F6600P imitates the ZTE F6600P, the reference box of RFC-005. Unlike the
// F660 mock above, which follows the documented contract, this one replays a
// protocol captured on real hardware (firmware ZTEGF660006SN): the
// sha256(password+token) login, the RSA key published in the main page, the
// Check header on every write, and the menuView/menuData pair that guards each
// data page.
//
// It is stateful (MAC filter, global filter switch, Wi-Fi ACL, SSIDs) and
// enforces the two rules the relay design depends on:
//
//   - one admin session at a time (RFC-005 C2): a new login evicts the
//     previous one, whose next request gets SessionTimeout;
//   - a write is accepted only with the current view's _sessionTOKEN and a
//     Check header that decrypts to sha256hex(body).
//
// The protocol constants are spelled out here rather than imported from
// internal/adapter/router/ztef6600p: that package's own tests use this mock,
// and importing it back would be a cycle. A mock that restates the wire format
// also fails loudly when the adapter quietly changes it.
//
// Writing SSIDs is deliberately not implemented: RFC-005 keeps SSID writes for
// H4, after they are validated on hardware.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/fixtures"
)

// Protocol constants, as captured on the box.
const (
	f6600pCookie    = "SID_HTTPS_"
	f6600pLoginTok  = "12345678" // the value inside fixtures.ZTEF6600PLoginToken
	statusView      = "statusMgr"
	statusTag       = "devmgr_statusmgr_lua.lua"
	clientsView     = "localNetStatus"
	clientsTag      = "wlan_client_stat_lua.lua"
	filterView      = "filterCriteria"
	macFilterTag    = "firewall_macfilterv3_lua.lua"
	filterGlobalTag = "firewall_filterglobal_lua.lua"
	dhcpView        = "lanMgrIpv4"
	dhcpTag         = "Localnet_LanMgrIpv4_DHCPHostInfo_lua.lua"
	topoView        = "mmTopology"
	topoTag         = "topo_lua.lua"
	wlanAdvView     = "wlanAdvanced"
	wlanPolicyTag   = "wlan_macfilteraclpolicy_lua.lua"
	wlanRuleTag     = "wlan_macfilterrule_lua.lua"
	wlanStatusTag   = "wlan_wlanstatus_lua.lua"
	wlanBasicView   = "wlanBasic"
	wlanSSIDTag     = "wlan_wlansssidconf_lua.lua"
)

// PageToken is the per-view token the box injects in each menuView and
// demands back on every write. A fixed value is enough: the adapter re-reads
// it before each write, so a token that moved would only test the mock. It is
// exported so a test can forge a write and watch the mock refuse it.
const PageToken = "rttY3AfvuLqPpAuBdKvfyISm"

// f6600pKey is shared by every mock in the process. Generating an RSA key per
// instance would cost more than the tests it serves, and the key is public by
// construction: the box publishes it in its main page.
var f6600pKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("mockrouter: " + err.Error())
	}
	return k
})

type macRule struct{ instID, name, mac string }

type wifiRule struct{ mac, ap, name string }

type ssidEntry struct {
	id, alias, radio, name, enable, hide, psk string
}

// F6600P is a stateful mock ZTE F6600P.
type F6600P struct {
	user, pass string

	mu        sync.Mutex
	session   string // "" when nobody is logged in; only one at a time
	logins    int
	views     map[string]bool
	lastView  string
	rules     []macRule
	nextRule  int
	filterOn  bool
	wifiRules []wifiRule
	wifiACL   map[string]string // AP -> Disabled|Ban|Allow
	apEnabled map[string]bool
	ssids     []ssidEntry
}

// NewF6600P builds a mock accepting the given credentials (default
// admin/admin). Its MAC filter and global switch start from the captured
// fixtures, so a read before any write matches the real box.
func NewF6600P(user, pass string) *F6600P {
	if user == "" {
		user = "admin"
	}
	if pass == "" {
		pass = "admin"
	}
	s := &F6600P{
		user:      user,
		pass:      pass,
		views:     map[string]bool{},
		filterOn:  strings.Contains(fixtures.ZTEF6600PFilterGlobal, "<ParaName>MacFilterEnable</ParaName><ParaValue>1</ParaValue>"),
		wifiACL:   map[string]string{"DEV.WIFI.AP1": "Disabled", "DEV.WIFI.AP2": "Disabled", "DEV.WIFI.AP5": "Disabled"},
		apEnabled: map[string]bool{"DEV.WIFI.AP1": true, "DEV.WIFI.AP2": false, "DEV.WIFI.AP5": true},
		ssids: []ssidEntry{
			{id: "DEV.WIFI.AP1", alias: "SSID1", radio: "DEV.WIFI.RD1", name: "MonWiFi-2G", enable: "1", hide: "0", psk: "mock-passphrase-1"},
			{id: "DEV.WIFI.AP5", alias: "SSID5", radio: "DEV.WIFI.RD2", name: "MonWiFi-5G", enable: "1", hide: "0", psk: "mock-passphrase-5"},
		},
	}
	for _, m := range seededFilterMACs() {
		s.addRule(m)
	}
	return s
}

var reSrcMac = regexp.MustCompile(`<ParaName>SrcMacAddr</ParaName><ParaValue>([^<]+)</ParaValue>`)

// seededFilterMACs reads the blocked MACs out of the captured MAC-filter page,
// so the mock follows the fixture if it is ever re-captured.
func seededFilterMACs() []string {
	var out []string
	for _, m := range reSrcMac.FindAllStringSubmatch(fixtures.ZTEF6600PMACFilter, -1) {
		out = append(out, m[1])
	}
	return out
}

func (s *F6600P) addRule(mac string) {
	s.nextRule++
	s.rules = append(s.rules, macRule{
		instID: fmt.Sprintf("DEV.FW.CHAIN1.MACF%d", s.nextRule),
		name:   mac,
		mac:    mac,
	})
}

// Blocked reports whether the MAC filter currently holds a rule for mac
// (comparison is case-insensitive, as the box mixes cases).
func (s *F6600P) Blocked(mac string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rules {
		if strings.EqualFold(r.mac, mac) {
			return true
		}
	}
	return false
}

// WiFiBlocked reports whether mac has a Wi-Fi ACL rule on at least one AP
// whose policy is Ban.
func (s *F6600P) WiFiBlocked(mac string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.wifiRules {
		if strings.EqualFold(r.mac, mac) && s.wifiACL[r.ap] == "Ban" {
			return true
		}
	}
	return false
}

// MACFilterEnabled reports the global MAC-filter switch.
func (s *F6600P) MACFilterEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.filterOn
}

// Logins counts the successful logins since the mock started. The failover
// test asserts that taking over the lease costs exactly one more login.
func (s *F6600P) Logins() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins
}

// ActiveSession returns the id of the only live admin session, or "" when
// nobody is logged in. Two relays talking to the box at once would show up
// here as a changed id.
func (s *F6600P) ActiveSession() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session
}

// Handler returns the HTTP handler implementing the captured contract.
func (s *F6600P) Handler() http.Handler {
	return http.HandlerFunc(s.serve)
}

func (s *F6600P) serve(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	typ, tag := q.Get("_type"), q.Get("_tag")

	switch {
	case typ == "loginData" && tag == "login_entry" && r.Method == http.MethodGet:
		// The box hands out a provisional cookie before the credentials.
		http.SetCookie(w, &http.Cookie{Name: f6600pCookie, Value: "placeholder", Path: "/"})
		_, _ = w.Write([]byte(`{"sess_token":"mock-session-token","lockingTime":0}`))

	case typ == "loginData" && tag == "login_token":
		_, _ = w.Write([]byte(fixtures.ZTEF6600PLoginToken))

	case typ == "loginData" && tag == "login_entry" && r.Method == http.MethodPost:
		s.login(w, r)

	case typ == "menuView":
		s.menuView(w, r, tag)

	case typ == "menuData" && r.Method == http.MethodPost:
		s.menuDataWrite(w, r, tag)

	case typ == "menuData":
		s.menuDataRead(w, r, tag)

	default:
		_, _ = w.Write([]byte(s.mainPage()))
	}
}

// login mirrors the box: the password is sha256(password + login token), and a
// successful login evicts whoever held the single admin session (C2).
func (s *F6600P) login(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	want := sha256hex(s.pass + f6600pLoginTok)
	if r.PostForm.Get("Username") != s.user || r.PostForm.Get("Password") != want {
		_, _ = w.Write([]byte(`{"loginErrMsg":"auth failed"}`))
		return
	}
	sid := newSID()
	s.mu.Lock()
	s.session = sid
	s.logins++
	s.views = map[string]bool{} // the evicted session's views go with it
	s.lastView = ""
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: f6600pCookie, Value: sid, Path: "/"})
	_, _ = w.Write([]byte(`{"sess_token":"mock-session-token","login_need_refresh":true}`))
}

// authed reports whether the request carries the live session cookie. A relay
// evicted by a newer login lands here and is told its session timed out.
func (s *F6600P) authed(r *http.Request) bool {
	c, err := r.Cookie(f6600pCookie)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session != "" && c.Value == s.session
}

func (s *F6600P) menuView(w http.ResponseWriter, r *http.Request, tag string) {
	if !s.authed(r) {
		writeSessionTimeout(w)
		return
	}
	s.mu.Lock()
	s.views[tag] = true
	s.lastView = tag
	s.mu.Unlock()
	_, _ = w.Write([]byte(`<script>_sessionTmpToken = "` + hexEscape(PageToken) + `";</script>`))
}

func (s *F6600P) menuDataRead(w http.ResponseWriter, r *http.Request, tag string) {
	if !s.authed(r) {
		writeSessionTimeout(w)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Each data page is readable only after its own view, and the two pages
	// the topology screen owns only while that view is the current one.
	switch tag {
	case statusTag:
		s.guarded(w, s.views[statusView], fixtures.ZTEF6600PStatus)
	case clientsTag:
		s.guarded(w, s.views[clientsView], fixtures.ZTEF6600PWLANClients)
	case macFilterTag:
		s.guarded(w, s.views[filterView], renderMACFilter(s.rules))
	case filterGlobalTag:
		s.guarded(w, s.views[filterView], renderFilterGlobal(s.filterOn))
	case dhcpTag:
		s.guarded(w, s.lastView == dhcpView, fixtures.ZTEF6600PDHCPHostInfo)
	case topoTag:
		s.guarded(w, s.lastView == topoView, fixtures.ZTEF6600PTopology)
	case wlanStatusTag:
		s.guarded(w, s.views[clientsView], renderWLANStatus(s.apEnabled))
	case wlanPolicyTag:
		s.guarded(w, s.views[wlanAdvView], renderWiFiPolicy(s.wifiACL))
	case wlanRuleTag:
		s.guarded(w, s.views[wlanAdvView], renderWiFiRules(s.wifiRules))
	case wlanSSIDTag:
		s.guarded(w, s.views[wlanBasicView], renderSSIDs(s.ssids))
	default:
		http.NotFound(w, r)
	}
}

// guarded serves page when its view has been loaded, and otherwise answers
// SessionTimeout like the box does for a data page opened out of order.
func (s *F6600P) guarded(w http.ResponseWriter, viewLoaded bool, page string) {
	if !viewLoaded {
		writeSessionTimeout(w)
		return
	}
	_, _ = w.Write([]byte(page))
}

// menuDataWrite applies a write. The box checks three things before touching
// its configuration: a live session, the current view's token, and the Check
// header; the mock refuses in the same order.
func (s *F6600P) menuDataWrite(w http.ResponseWriter, r *http.Request, tag string) {
	if !s.authed(r) {
		xmlError(w, "SessionTimeout")
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		xmlError(w, "FAIL")
		return
	}
	body := string(raw)
	vals, err := url.ParseQuery(body)
	if err != nil {
		xmlError(w, "FAIL")
		return
	}
	if vals.Get("_sessionTOKEN") != PageToken {
		xmlError(w, "Cette page a expiré, actualisez et essayez à nouveau.")
		return
	}
	if !checkHeaderValid(r.Header.Get("Check"), body) {
		xmlError(w, "integrity check failed")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	switch tag {
	case macFilterTag:
		s.applyMACFilter(w, vals)
	case filterGlobalTag:
		s.applyFilterGlobal(w, vals)
	case wlanRuleTag:
		s.applyWiFiRule(w, vals)
	case wlanPolicyTag:
		s.applyWiFiPolicy(w, vals)
	case wlanSSIDTag:
		// H4 territory: refuse rather than pretend the write worked.
		xmlError(w, "SSID writes are not implemented by the mock")
	default:
		xmlError(w, "FAIL")
	}
}

func (s *F6600P) applyMACFilter(w http.ResponseWriter, vals url.Values) {
	switch vals.Get("IF_ACTION") {
	case "Apply":
		mac := vals.Get("SrcMacAddr")
		if mac == "" {
			xmlError(w, "FAIL")
			return
		}
		for _, r := range s.rules {
			if strings.EqualFold(r.mac, mac) {
				xmlError(w, "SUCC") // the box tolerates a duplicate apply
				return
			}
		}
		s.addRule(mac)
		xmlError(w, "SUCC")
	case "Delete":
		for i, r := range s.rules {
			if r.instID == vals.Get("_InstID") {
				s.rules = append(s.rules[:i], s.rules[i+1:]...)
				xmlError(w, "SUCC")
				return
			}
		}
		xmlError(w, "L'opération en cours est invalide")
	default:
		xmlError(w, "FAIL")
	}
}

func (s *F6600P) applyFilterGlobal(w http.ResponseWriter, vals url.Values) {
	if vals.Get("_InstID") != "IGD" || vals.Get("MacFilterTarget") != "Discard" {
		xmlError(w, "bad filter form")
		return
	}
	s.filterOn = vals.Get("MacFilterEnable") == "1"
	xmlError(w, "SUCC")
}

// applyWiFiRule mirrors the box's quirks: rule names are unique across the
// whole list, and ids are positions, renumbered after every delete.
func (s *F6600P) applyWiFiRule(w http.ResponseWriter, vals url.Values) {
	switch vals.Get("IF_ACTION") {
	case "Apply":
		for _, r := range s.wifiRules {
			if r.name == vals.Get("Name") {
				xmlError(w, "FAIL")
				return
			}
		}
		s.wifiRules = append(s.wifiRules, wifiRule{
			mac:  vals.Get("MACAddress"),
			ap:   vals.Get("Interface"),
			name: vals.Get("Name"),
		})
		xmlError(w, "SUCC")
	case "Delete":
		for i := range s.wifiRules {
			if fmt.Sprintf("DEV.ACL%d", i+1) == vals.Get("_InstID") {
				s.wifiRules = append(s.wifiRules[:i], s.wifiRules[i+1:]...)
				xmlError(w, "SUCC")
				return
			}
		}
		xmlError(w, "L'opération en cours est invalide")
	default:
		xmlError(w, "FAIL")
	}
}

func (s *F6600P) applyWiFiPolicy(w http.ResponseWriter, vals url.Values) {
	var n int
	if _, err := fmt.Sscanf(vals.Get("_InstNum"), "%d", &n); err != nil {
		xmlError(w, "FAIL")
		return
	}
	for i := 0; i < n; i++ {
		ap := vals.Get(fmt.Sprintf("_InstID_%d", i))
		if ap == "" {
			continue
		}
		s.wifiACL[ap] = vals.Get(fmt.Sprintf("ACLPolicy_%d", i))
	}
	xmlError(w, "SUCC")
}

// mainPage is the page the adapter reads right after logging in: it carries
// the RSA public key and the session token.
//
// The model is written with HTML entities, as the box does. That is not
// cosmetic: discovery matches plain substrings, "f660" is a prefix of
// "f6600p", and the F660 adapter is tried first, so a plain-text model name
// here would make the mock detect as a zte_f660.
func (s *F6600P) mainPage() string {
	der, err := x509.MarshalPKIXPublicKey(&f6600pKey().PublicKey)
	if err != nil {
		panic("mockrouter: " + err.Error())
	}
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	js := strings.ReplaceAll(strings.TrimSpace(pemStr), "\n", `\n`)
	return `<html><head><title>ZXHN &#70;&#54;&#54;&#48;&#48;&#80;</title></head><body>` +
		`<script>var pubKey = "` + js + `";</script>` +
		`<script>_sessionTmpToken = "` + hexEscape(PageToken) + `";</script>` +
		`<input type="hidden" name="_sessionTOKEN" id="_sessionTOKEN" value="` + PageToken + `"/>` +
		`</body></html>`
}

// checkHeaderValid verifies the integrity header the web UI sends with every
// write: base64(RSA_PKCS1v15(sha256hex(body))).
func checkHeaderValid(header, body string) bool {
	enc, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		return false
	}
	plain, err := rsa.DecryptPKCS1v15(nil, f6600pKey(), enc)
	return err == nil && string(plain) == sha256hex(body)
}

func renderMACFilter(rules []macRule) string {
	var b strings.Builder
	b.WriteString(`<ajax_response_xml_root><IF_ERRORSTR>SUCC</IF_ERRORSTR><OBJ_MACFILTER_ID>`)
	for _, r := range rules {
		fmt.Fprintf(&b, `<Instance>%s%s%s%s%s%s</Instance>`,
			para("_InstID", r.instID), para("Type", "Route"), para("Protocol", "ALL"),
			para("Name", r.name), para("SrcMacAddr", r.mac), para("DstMacAddr", "00:00:00:00:00:00"))
	}
	b.WriteString(`</OBJ_MACFILTER_ID></ajax_response_xml_root>`)
	return b.String()
}

func renderFilterGlobal(on bool) string {
	v := "0"
	if on {
		v = "1"
	}
	return `<ajax_response_xml_root><IF_ERRORSTR>SUCC</IF_ERRORSTR><OBJ_FWBASE_ID><Instance>` +
		para("_InstID", "IGD") + para("MacFilterEnable", v) + para("MacFilterTarget", "Discard") +
		para("UrlFilterEnable", "0") + para("UrlFilterTarget", "0") +
		`</Instance></OBJ_FWBASE_ID></ajax_response_xml_root>`
}

func renderWLANStatus(enabled map[string]bool) string {
	var b strings.Builder
	b.WriteString(`<ajax_response_xml_root><IF_ERRORSTR>SUCC</IF_ERRORSTR><OBJ_WLANAP_ID>`)
	for _, ap := range sortedKeys(enabled) {
		v := "0"
		if enabled[ap] {
			v = "1"
		}
		b.WriteString(`<Instance>` + para("_InstID", ap) + para("Enable", v) + `</Instance>`)
	}
	b.WriteString(`</OBJ_WLANAP_ID></ajax_response_xml_root>`)
	return b.String()
}

func renderWiFiPolicy(acl map[string]string) string {
	var b strings.Builder
	b.WriteString(`<ajax_response_xml_root><IF_ERRORSTR>SUCC</IF_ERRORSTR><OBJ_WLANAP_ID>`)
	for _, ap := range sortedKeys(acl) {
		b.WriteString(`<Instance>` + para("_InstID", ap) + para("ACLPolicy", acl[ap]) + `</Instance>`)
	}
	b.WriteString(`</OBJ_WLANAP_ID></ajax_response_xml_root>`)
	return b.String()
}

func renderWiFiRules(rules []wifiRule) string {
	var b strings.Builder
	b.WriteString(`<ajax_response_xml_root><IF_ERRORSTR>SUCC</IF_ERRORSTR><OBJ_ACLCFG_ID>`)
	for i, r := range rules {
		b.WriteString(`<Instance>` + para("_InstID", fmt.Sprintf("DEV.ACL%d", i+1)) +
			para("Name", r.name) + para("Interface", r.ap) + para("MACAddress", r.mac) + `</Instance>`)
	}
	b.WriteString(`</OBJ_ACLCFG_ID></ajax_response_xml_root>`)
	return b.String()
}

// renderSSIDs serves the SSID page the way the box does, with the secrets
// AES-encrypted under the page token.
func renderSSIDs(ssids []ssidEntry) string {
	var b strings.Builder
	b.WriteString(`<ajax_response_xml_root><IF_ERRORSTR>SUCC</IF_ERRORSTR><OBJ_WLANAP_ID>`)
	for _, s := range ssids {
		b.WriteString(`<Instance>` + para("_InstID", s.id) + para("Enable", s.enable) +
			para("BeaconType", "11iandWPA3") + para("WEPAuthMode", "None") + para("Alias", s.alias) +
			para("ESSIDHideEnable", s.hide) + para("ESSID", s.name) + para("WEPKeyIndex", "1") +
			para("WPA3EncryptType", "AESEncryption") + para("WPA3AuthMode", "SAEAuthentication") +
			para("WPAAuthMode", "PSKAuthentication") + para("WLANViewName", s.radio) +
			para("11iEncryptType", "AESEncryption") + para("WPAEncryptType", "AESEncryption") +
			para("11iAuthMode", "PSKAuthentication") + `</Instance>`)
	}
	b.WriteString(`</OBJ_WLANAP_ID><OBJ_WLANSETTING_ID><Instance>` +
		para("_InstID", "DEV.WIFI.RD1") + para("Band", "2.4GHz") + `</Instance><Instance>` +
		para("_InstID", "DEV.WIFI.RD2") + para("Band", "5GHz") + `</Instance></OBJ_WLANSETTING_ID>`)
	b.WriteString(`<OBJ_WLANPSK_ID>`)
	for _, s := range ssids {
		b.WriteString(`<Instance>` + para("_InstID", s.id+".PSK1") +
			para("KeyPassphrase", encryptParaValue(s.psk, PageToken)) + `</Instance>`)
	}
	b.WriteString(`</OBJ_WLANPSK_ID><OBJ_MAP_MASTER_ID><Instance>` +
		para("_InstID", "IGD") + para("EnBandSteer", "0") + `</Instance></OBJ_MAP_MASTER_ID>` +
		`<encode>WEPKey</encode><encode>KeyPassphrase</encode></ajax_response_xml_root>`)
	return b.String()
}

// encryptParaValue encrypts a secret the way the box does: AES-256-CBC, key
// sha256(token), IV the first 16 bytes of sha256(reversed token), zero
// padding, base64.
func encryptParaValue(plain, token string) string {
	key := sha256.Sum256([]byte(token))
	ivSum := sha256.Sum256([]byte(reverseString(token)))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		panic("mockrouter: " + err.Error())
	}
	padded := []byte(plain)
	if r := len(padded) % aes.BlockSize; r != 0 {
		padded = append(padded, make([]byte, aes.BlockSize-r)...)
	}
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, ivSum[:aes.BlockSize]).CryptBlocks(out, padded)
	return base64.StdEncoding.EncodeToString(out)
}

func reverseString(s string) string {
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

// sortedKeys keeps the rendered pages stable, so a test can compare them.
func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }

func para(name, value string) string {
	return `<ParaName>` + name + `</ParaName><ParaValue>` + value + `</ParaValue>`
}

func writeSessionTimeout(w http.ResponseWriter) {
	_, _ = w.Write([]byte(`<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`))
}

func xmlError(w http.ResponseWriter, msg string) {
	_, _ = w.Write([]byte(`<ajax_response_xml_root><IF_ERRORSTR>` + msg + `</IF_ERRORSTR></ajax_response_xml_root>`))
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// hexEscape writes a string the way the box's JS does, byte by byte as \xNN.
func hexEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		fmt.Fprintf(&b, `\x%02x`, s[i])
	}
	return b.String()
}
