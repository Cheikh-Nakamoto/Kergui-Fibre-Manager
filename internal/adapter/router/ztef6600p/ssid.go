package ztef6600p

// Wi-Fi networks ("Wi-Fi > Basique > Configuration SSID"): the router has eight
// access points (SSID1-4 on 2.4 GHz, SSID5-8 on 5 GHz, some reserved and not
// listed), each one switched on or off on its own.
//
// The web UI posts one SSID's whole form at a time: every field it shows,
// including the WPA passphrase and the four WEP keys. Those secrets come back
// AES-encrypted with the page's _sessionTmpToken and go out AES-encrypted with
// a random key, itself RSA-encrypted in an extra "encode" field. kergui does
// the same, re-sending the secrets it did not change.

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

const (
	wlanBasicView = "wlanBasic"
	wlanSSIDTag   = "wlan_wlansssidconf_lua.lua"
	// With band steering on, the 5 GHz main SSID follows SSID1 and the web UI
	// greys it out (getBSCtrl in the wlanBasic view).
	bandSteerFollower = "DEV.WIFI.AP5"
)

var _ port.WiFiSSIDManager = (*Gateway)(nil)

// securityModes mirrors the web UI's EncryptionParameter table, in its order:
// the UI shows the first mode whose parameters all match the SSID's.
var securityModes = []struct {
	name   string
	params map[string]string
}{
	{"No Security", map[string]string{"BeaconType": "None"}},
	{"WEP-OpenSystem", map[string]string{"BeaconType": "Basic", "WEPAuthMode": "None"}},
	{"WEP-ShareKey", map[string]string{"BeaconType": "Basic", "WEPAuthMode": "SharedAuthentication"}},
	{"WPA-PSK-TKIP", map[string]string{"BeaconType": "WPA", "WPAAuthMode": "PSKAuthentication", "WPAEncryptType": "TKIPEncryption"}},
	{"WPA-PSK-AES", map[string]string{"BeaconType": "WPA", "WPAAuthMode": "PSKAuthentication", "WPAEncryptType": "AESEncryption"}},
	{"WPA-PSK-TKIP/AES", map[string]string{"BeaconType": "WPA", "WPAAuthMode": "PSKAuthentication", "WPAEncryptType": "TKIPandAESEncryption"}},
	{"WPA2-PSK-AES", map[string]string{"BeaconType": "11i", "11iAuthMode": "PSKAuthentication", "11iEncryptType": "AESEncryption"}},
	{"WPA2-PSK-TKIP", map[string]string{"BeaconType": "11i", "11iAuthMode": "PSKAuthentication", "11iEncryptType": "TKIPEncryption"}},
	{"WPA2-PSK-TKIP/AES", map[string]string{"BeaconType": "11i", "11iAuthMode": "PSKAuthentication", "11iEncryptType": "TKIPandAESEncryption"}},
	{"WPA/WPA2-PSK-TKIP", map[string]string{"BeaconType": "WPAand11i", "WPAAuthMode": "PSKAuthentication", "11iAuthMode": "PSKAuthentication", "WPAEncryptType": "TKIPEncryption", "11iEncryptType": "TKIPEncryption"}},
	{"WPA/WPA2-PSK-AES", map[string]string{"BeaconType": "WPAand11i", "WPAAuthMode": "PSKAuthentication", "11iAuthMode": "PSKAuthentication", "WPAEncryptType": "AESEncryption", "11iEncryptType": "AESEncryption"}},
	{"WPA/WPA2-PSK-TKIP/AES", map[string]string{"BeaconType": "WPAand11i", "WPAAuthMode": "PSKAuthentication", "11iAuthMode": "PSKAuthentication", "WPAEncryptType": "TKIPandAESEncryption", "11iEncryptType": "TKIPandAESEncryption"}},
	{"WPA2-EAP-AES", map[string]string{"BeaconType": "11i", "11iAuthMode": "EAPAuthentication", "11iEncryptType": "AESEncryption"}},
	{"WPA/WPA2-EAP-TKIP/AES", map[string]string{"BeaconType": "WPAand11i", "WPAAuthMode": "EAPAuthentication", "11iAuthMode": "EAPAuthentication", "WPAEncryptType": "TKIPandAESEncryption", "11iEncryptType": "TKIPandAESEncryption"}},
	{"WPA/WPA2-EAP-AES", map[string]string{"BeaconType": "WPAand11i", "WPAAuthMode": "EAPAuthentication", "11iAuthMode": "EAPAuthentication", "WPAEncryptType": "AESEncryption", "11iEncryptType": "AESEncryption"}},
	{"WPA3-OWE", map[string]string{"BeaconType": "WPA3", "WPA3AuthMode": "OWEAuthentication", "WPA3EncryptType": "AESEncryption"}},
	{"WPA3-SAE", map[string]string{"BeaconType": "WPA3", "WPA3AuthMode": "SAEAuthentication", "WPA3EncryptType": "AESEncryption"}},
	{"WPA/WPA2/WPA3-PSK/SAE", map[string]string{"BeaconType": "WPAand11iandWPA3", "WPAAuthMode": "PSKAuthentication", "11iAuthMode": "PSKAuthentication", "WPA3AuthMode": "SAEAuthentication", "WPAEncryptType": "TKIPandAESEncryption", "11iEncryptType": "TKIPandAESEncryption", "WPA3EncryptType": "AESEncryption"}},
	{"WPA2/WPA3-SAE", map[string]string{"BeaconType": "11iandWPA3", "11iAuthMode": "PSKAuthentication", "WPA3AuthMode": "SAEAuthentication", "11iEncryptType": "TKIPandAESEncryption", "WPA3EncryptType": "AESEncryption"}},
	{"WPA2-PSK-AES/WPA3-SAE-AES", map[string]string{"BeaconType": "11iandWPA3", "11iAuthMode": "PSKAuthentication", "WPA3AuthMode": "SAEAuthentication", "11iEncryptType": "AESEncryption", "WPA3EncryptType": "AESEncryption"}},
	{"WPA-EAP-TKIP", map[string]string{"BeaconType": "WPA", "WPAAuthMode": "EAPAuthentication", "WPAEncryptType": "TKIPEncryption"}},
}

func securityMode(ap map[string]string) string {
	for _, m := range securityModes {
		match := true
		for k, v := range m.params {
			if ap[k] != v {
				match = false
				break
			}
		}
		if match {
			return m.name
		}
	}
	return ""
}

// usesPassphrase reports whether a security mode takes the WPA passphrase
// (the PSK branch of g_EncryptionTypeAuxCtrlsDisp).
func usesPassphrase(mode string) bool {
	return strings.Contains(mode, "PSK") || strings.Contains(mode, "SAE")
}

// ssidPage is one read of the SSID page, secrets already decrypted.
type ssidPage struct {
	aps       []map[string]string
	bandOf    map[string]string // radio (WLANViewName) -> "2.4GHz" / "5GHz"
	guests    []map[string]string
	wep       map[string]string // DEV.WIFI.AP1.WEP1 -> key
	psk       map[string]string // DEV.WIFI.AP1.PSK1 -> passphrase
	bandSteer bool
}

func (p *ssidPage) ssids() []domain.WiFiSSID {
	out := make([]domain.WiFiSSID, 0, len(p.aps))
	for _, ap := range p.aps {
		s := domain.WiFiSSID{
			ID:       ap["_InstID"],
			Alias:    ap["Alias"],
			Band:     p.bandOf[ap["WLANViewName"]],
			Name:     ap["ESSID"],
			Enabled:  ap["Enable"] == "1",
			Hidden:   ap["ESSIDHideEnable"] == "1",
			Security: securityMode(ap),
		}
		if p.bandSteer && s.ID == bandSteerFollower {
			s.Locked = "suit SSID1 tant que le band steering est actif"
		}
		out = append(out, s)
	}
	return out
}

func parseSSIDPage(data, token string) (*ssidPage, error) {
	root, err := parseAjaxXML(data)
	if err != nil || !isSuccess(root) {
		return nil, fmt.Errorf("%w: Wi-Fi SSID page: %s", domain.ErrSessionExpired, truncate(data, 300))
	}
	insts := func(substr string) []map[string]string {
		obj := findObject(root, substr)
		if obj == nil {
			return nil
		}
		var out []map[string]string
		for _, inst := range obj.Instances {
			out = append(out, inst.toMap())
		}
		return out
	}
	p := &ssidPage{
		aps:    insts("WLANAP"),
		bandOf: map[string]string{},
		guests: insts("GUESTWIFI"),
		wep:    map[string]string{},
		psk:    map[string]string{},
	}
	if len(p.aps) == 0 {
		return nil, fmt.Errorf("%w: no Wi-Fi access point in the SSID page", domain.ErrUnexpectedResponse)
	}
	for _, r := range insts("WLANSETTING") {
		p.bandOf[r["_InstID"]] = r["Band"]
	}
	for _, m := range insts("MAP_MASTER") {
		p.bandSteer = m["EnBandSteer"] == "1"
	}
	for _, k := range insts("WLANWEPKEY") {
		if p.wep[k["_InstID"]], err = decodeParaValue(k["WEPKey"], token, reverse(token)); err != nil {
			return nil, err
		}
	}
	for _, k := range insts("WLANPSK") {
		if p.psk[k["_InstID"]], err = decodeParaValue(k["KeyPassphrase"], token, reverse(token)); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// ssidPage loads the wlanBasic view (for its token, which also decrypts the
// page's secrets) and reads the SSID page.
func (g *Gateway) ssidPage(ctx context.Context) (*ssidPage, error) {
	if err := g.loadView(ctx, wlanBasicView); err != nil {
		return nil, err
	}
	data, err := g.wlanData(ctx, wlanSSIDTag)
	if err != nil {
		return nil, err
	}
	return parseSSIDPage(data, g.sessionToken)
}

// WiFiSSIDs implements port.WiFiSSIDManager.
func (g *Gateway) WiFiSSIDs(ctx context.Context) ([]domain.WiFiSSID, error) {
	var out []domain.WiFiSSID
	err := g.withSessionRetry(ctx, "Wi-Fi SSID read", func() error {
		p, err := g.ssidPage(ctx)
		if err != nil {
			return err
		}
		out = p.ssids()
		return nil
	})
	return out, err
}

// SetWiFiSSID implements port.WiFiSSIDManager.
func (g *Gateway) SetWiFiSSID(ctx context.Context, id string, change domain.WiFiSSIDChange) error {
	if err := g.withSessionRetry(ctx, "Wi-Fi SSID change", func() error { return g.setSSIDOnce(ctx, id, change) }); err != nil {
		return err
	}
	if g.opts.SkipWriteVerify {
		return nil
	}
	return g.verifySSID(ctx, id, change)
}

func (g *Gateway) setSSIDOnce(ctx context.Context, id string, change domain.WiFiSSIDChange) error {
	p, err := g.ssidPage(ctx)
	if err != nil {
		return err
	}
	idx := -1
	for i, ap := range p.aps {
		if ap["_InstID"] == id {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("%w: no Wi-Fi access point %s", domain.ErrInvalidInput, id)
	}
	if p.bandSteer && id == bandSteerFollower {
		return fmt.Errorf("%w: %s follows SSID1 while band steering is on", domain.ErrInvalidInput, id)
	}
	ap := p.aps[idx]
	mode := securityMode(ap)
	if mode == "" {
		return fmt.Errorf("%w: unknown Wi-Fi security on %s (BeaconType %q)", domain.ErrUnexpectedResponse, id, ap["BeaconType"])
	}
	if change.Password != nil && !usesPassphrase(mode) {
		return fmt.Errorf("%w: %s uses %s, which has no WPA password", domain.ErrInvalidInput, ap["Alias"], mode)
	}
	var guest map[string]string
	if idx < len(p.guests) {
		guest = p.guests[idx]
	}
	body, err := g.ssidForm(p, ap, guest, mode, change)
	if err != nil {
		return err
	}
	return g.postSigned(ctx, "/?_type=menuData&_tag="+wlanSSIDTag, body, domain.ErrUnexpectedResponse)
}

// ssidForm mirrors InitialPostData on a filled template_WLANSSIDConf clone:
// every non-PostIgnore field in document order, secrets AES-encrypted.
func (g *Gateway) ssidForm(p *ssidPage, ap, guest map[string]string, mode string, ch domain.WiFiSSIDChange) (string, error) {
	if g.rsaPubKey == nil {
		return "", fmt.Errorf("%w: router RSA public key not found, cannot encrypt the Wi-Fi password", domain.ErrUnexpectedResponse)
	}
	id := ap["_InstID"]
	enable, name, hide := ap["Enable"], ap["ESSID"], ap["ESSIDHideEnable"]
	passphrase := p.psk[id+".PSK1"]
	if ch.Enabled != nil {
		enable = boolFlag(*ch.Enabled)
	}
	if ch.Name != nil {
		name = *ch.Name
	}
	if ch.Hidden != nil {
		hide = boolFlag(*ch.Hidden)
	}
	if ch.Password != nil {
		passphrase = *ch.Password
	}
	pskConf, wepConf := "N", "N"
	if usesPassphrase(mode) {
		pskConf = "Y"
	} else if strings.HasPrefix(mode, "WEP-") {
		wepConf = "Y"
	}
	key, iv := randomDigits(16), randomDigits(16)

	var b strings.Builder
	add := func(k, v string) { b.WriteString("&" + k + "=" + encodeURIComponent(v)) }
	secret := func(k, v string) error {
		enc, err := encodeParaValue(v, key, iv)
		if err != nil {
			return err
		}
		add(k, enc)
		return nil
	}
	b.WriteString("IF_ACTION=Apply")
	add("Enable", enable)
	add("_InstID", id)
	add("_WEPCONIG", wepConf)
	add("_PSKCONIG", pskConf)
	for _, k := range []string{"BeaconType", "WEPAuthMode", "WPAAuthMode", "11iAuthMode", "WPAEncryptType", "11iEncryptType", "WPA3AuthMode", "WPA3EncryptType"} {
		add(k, ap[k])
	}
	for i := 0; i < 4; i++ {
		add(fmt.Sprintf("_InstID_WEP%d", i), instIfKnown(p.wep, fmt.Sprintf("%s.WEP%d", id, i+1)))
	}
	add("_InstID_PSK", instIfKnown(p.psk, id+".PSK1"))
	// RADIUS addresses are rebuilt from four sub-fields this page does not have.
	for _, k := range []string{"MasterAuthServerIp", "BackupAuthServerIp", "MasterAcctServerIp", "BackupAcctServerIp"} {
		add(k, "...")
	}
	add("_InstID_GUEST", guest["_InstID"])
	add("_GUEST", orDefault(guest["_GUEST"], "N"))
	add("GuestWifi", guest["GuestWifi"])
	add("ESSID", name)
	add("ESSIDHideEnable", hide)
	add("EncryptionType", mode)
	if err := secret("KeyPassphrase", passphrase); err != nil {
		return "", err
	}
	add("WEPKeyIndex", ap["WEPKeyIndex"])
	add("ShowWEPKey", "0")
	for i := 0; i < 4; i++ {
		if err := secret(fmt.Sprintf("WEPKey0%d", i), p.wep[fmt.Sprintf("%s.WEP%d", id, i+1)]); err != nil {
			return "", err
		}
	}
	encKey, err := rsaEncrypt(g.rsaPubKey, []byte(key+"+"+iv))
	if err != nil {
		return "", fmt.Errorf("%w: RSA encrypt failed: %v", domain.ErrUnexpectedResponse, err)
	}
	add("encode", base64.StdEncoding.EncodeToString(encKey))
	b.WriteString("&_sessionTOKEN=" + g.sessionToken)
	return b.String(), nil
}

func (g *Gateway) verifySSID(ctx context.Context, id string, ch domain.WiFiSSIDChange) error {
	var p *ssidPage
	err := g.withSessionRetry(ctx, "Wi-Fi SSID verify", func() (e error) { p, e = g.ssidPage(ctx); return })
	if err != nil {
		return err
	}
	for _, s := range p.ssids() {
		if s.ID != id {
			continue
		}
		ok := (ch.Enabled == nil || s.Enabled == *ch.Enabled) &&
			(ch.Name == nil || s.Name == *ch.Name) &&
			(ch.Hidden == nil || s.Hidden == *ch.Hidden) &&
			(ch.Password == nil || p.psk[id+".PSK1"] == *ch.Password)
		if !ok {
			g.log.Errorf("f6600p: Wi-Fi SSID %s verification failed: got enabled=%v name=%q hidden=%v", id, s.Enabled, s.Name, s.Hidden)
			return fmt.Errorf("%w: router accepted the request but %s does not reflect it", domain.ErrUnexpectedResponse, s.Alias)
		}
		g.log.Infof("f6600p: verified Wi-Fi %s (%s) enabled=%v name=%q hidden=%v", s.Alias, id, s.Enabled, s.Name, s.Hidden)
		return nil
	}
	return fmt.Errorf("%w: %s vanished after the change", domain.ErrUnexpectedResponse, id)
}

// instIfKnown returns inst when the page listed it, "" otherwise (an
// unfilled template field).
func instIfKnown(m map[string]string, inst string) string {
	if _, ok := m[inst]; ok {
		return inst
	}
	return ""
}

func boolFlag(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// randomDigits mirrors the UI's randomNum(n): n decimal digits.
func randomDigits(n int) string {
	b := make([]byte, n)
	for i := range b {
		d, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			panic(err) // crypto/rand never fails on supported platforms
		}
		b[i] = byte('0' + d.Int64())
	}
	return string(b)
}

// paraCipher is the UI's CryptoJS AES: AES-256-CBC, key sha256(key), IV the
// first 16 bytes of sha256(iv), zero padding, base64 ciphertext.
func paraCipher(key, iv string) (cipher.Block, []byte) {
	k := sha256.Sum256([]byte(key))
	v := sha256.Sum256([]byte(iv))
	block, _ := aes.NewCipher(k[:]) // a 32-byte key never fails
	return block, v[:aes.BlockSize]
}

// encodeParaValue mirrors the UI's encodeParaValue (empty stays empty).
func encodeParaValue(src, key, iv string) (string, error) {
	if src == "" {
		return "", nil
	}
	block, ivb := paraCipher(key, iv)
	data := []byte(src)
	if r := len(data) % aes.BlockSize; r != 0 {
		data = append(data, make([]byte, aes.BlockSize-r)...)
	}
	cipher.NewCBCEncrypter(block, ivb).CryptBlocks(data, data)
	return base64.StdEncoding.EncodeToString(data), nil
}

// decodeParaValue mirrors the UI's decodeParaValue. Values read from the router
// use the page's _sessionTmpToken as key and that token reversed as IV.
func decodeParaValue(src, key, iv string) (string, error) {
	if src == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(src)
	if err != nil || len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
		return "", fmt.Errorf("%w: undecodable Wi-Fi secret", domain.ErrUnexpectedResponse)
	}
	block, ivb := paraCipher(key, iv)
	cipher.NewCBCDecrypter(block, ivb).CryptBlocks(raw, raw)
	out := bytes.TrimRight(raw, "\x00")
	// A wrong token decrypts to noise; posting that back would replace the
	// real password, so refuse instead.
	for _, c := range out {
		if c < 0x20 || c > 0x7e {
			return "", fmt.Errorf("%w: Wi-Fi secret did not decrypt with the session token", domain.ErrUnexpectedResponse)
		}
	}
	return string(out), nil
}

func reverse(s string) string {
	r := []byte(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}
