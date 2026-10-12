package ztef6600p

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

func mockPair(id, alias, radio, name, enable, psk string) mockSSID {
	return mockSSID{id: id, alias: alias, radio: radio, name: name, enable: enable, hide: "0", beacon: "11iandWPA3", psk: psk}
}

func ssidState() *mockState {
	return &mockState{viewLoaded: map[string]bool{}, bandSteer: true, ssids: []mockSSID{
		mockPair("DEV.WIFI.AP1", "SSID1", "DEV.WIFI.RD1", "Maison", "1", "main-pass-1"),
		mockPair("DEV.WIFI.AP3", "SSID3", "DEV.WIFI.RD1", "SSID3", "0", "default-3x"),
		mockPair("DEV.WIFI.AP5", "SSID5", "DEV.WIFI.RD2", "Maison", "1", "main-pass-1"),
	}}
}

// renderMockSSIDs serves the SSID page like the router: secrets AES-encrypted
// with the page token (reversed for the IV).
func renderMockSSIDs(st *mockState) string {
	enc := func(v string) string {
		out, _ := encodeParaValue(v, mockToken, reverse(mockToken))
		return out
	}
	p := func(k, v string) string { return `<ParaName>` + k + `</ParaName><ParaValue>` + v + `</ParaValue>` }
	x := `<ajax_response_xml_root><IF_ERRORSTR>SUCC</IF_ERRORSTR><OBJ_WLANAP_ID>`
	for _, s := range st.ssids {
		x += `<Instance>` + p("_InstID", s.id) + p("Enable", s.enable) + p("BeaconType", s.beacon) +
			p("WEPAuthMode", "None") + p("Alias", s.alias) + p("ESSIDHideEnable", s.hide) + p("ESSID", s.name) +
			p("WEPKeyIndex", "1") + p("WPA3EncryptType", "AESEncryption") + p("WPA3AuthMode", "SAEAuthentication") +
			p("WPAAuthMode", "PSKAuthentication") + p("WLANViewName", s.radio) + p("11iEncryptType", "TKIPandAESEncryption") +
			p("WPAEncryptType", "TKIPandAESEncryption") + p("11iAuthMode", "PSKAuthentication") + `</Instance>`
	}
	x += `</OBJ_WLANAP_ID><OBJ_WLANSETTING_ID><Instance>` + p("_InstID", "DEV.WIFI.RD1") + p("Band", "2.4GHz") +
		`</Instance><Instance>` + p("_InstID", "DEV.WIFI.RD2") + p("Band", "5GHz") + `</Instance></OBJ_WLANSETTING_ID><OBJ_WLANWEPKEY_ID>`
	for _, s := range st.ssids {
		for _, n := range []string{"1", "2", "3", "4"} {
			x += `<Instance>` + p("_InstID", s.id+".WEP"+n) + p("WEPKey", enc("wep"+n+"key")) + `</Instance>`
		}
	}
	x += `</OBJ_WLANWEPKEY_ID><OBJ_WLANPSK_ID>`
	for _, s := range st.ssids {
		x += `<Instance>` + p("_InstID", s.id+".PSK1") + p("KeyPassphrase", enc(s.psk)) + `</Instance>`
	}
	steer := "0"
	if st.bandSteer {
		steer = "1"
	}
	return x + `</OBJ_WLANPSK_ID><OBJ_MAP_MASTER_ID><Instance>` + p("_InstID", "IGD") + p("EnBandSteer", steer) +
		`</Instance></OBJ_MAP_MASTER_ID><encode>WEPKey</encode><encode>KeyPassphrase</encode></ajax_response_xml_root>`
}

// applyMockSSID applies a posted SSID form, decrypting the secrets with the
// key/IV pair carried RSA-encrypted in "encode". It returns an error string.
func applyMockSSID(st *mockState, vals url.Values) string {
	raw, err := base64.StdEncoding.DecodeString(vals.Get("encode"))
	if err != nil {
		return "bad encode"
	}
	plain, err := rsa.DecryptPKCS1v15(nil, mockKey, raw)
	key, iv, ok := strings.Cut(string(plain), "+")
	if err != nil || !ok {
		return "bad encode key"
	}
	psk, err := decodeParaValue(vals.Get("KeyPassphrase"), key, iv)
	if err != nil {
		return "bad passphrase"
	}
	for i := 0; i < 4; i++ {
		if k, err := decodeParaValue(vals.Get("WEPKey0"+string(rune('0'+i))), key, iv); err != nil || k != "wep"+string(rune('1'+i))+"key" {
			return "WEP key lost"
		}
	}
	for i := range st.ssids {
		s := &st.ssids[i]
		if s.id != vals.Get("_InstID") {
			continue
		}
		if vals.Get("_InstID_PSK") != s.id+".PSK1" || vals.Get("EncryptionType") != "WPA2/WPA3-SAE" || vals.Get("_PSKCONIG") != "Y" {
			return "bad form"
		}
		s.enable, s.name, s.hide, s.psk = vals.Get("Enable"), vals.Get("ESSID"), vals.Get("ESSIDHideEnable"), psk
		return ""
	}
	return "no such AP"
}

func TestGateway_WiFiSSIDs(t *testing.T) {
	srv := testServerWithState("user", "secret", ssidState())
	defer srv.Close()
	gw := loginGateway(t, srv.URL)

	ssids, err := gw.WiFiSSIDs(context.Background())
	if err != nil {
		t.Fatalf("WiFiSSIDs: %v", err)
	}
	if len(ssids) != 3 {
		t.Fatalf("got %d SSIDs, want 3: %+v", len(ssids), ssids)
	}
	want := domain.WiFiSSID{ID: "DEV.WIFI.AP3", Alias: "SSID3", Band: "2.4GHz", Name: "SSID3", Security: "WPA2/WPA3-SAE"}
	if ssids[1] != want {
		t.Errorf("SSID3 = %+v, want %+v", ssids[1], want)
	}
	if ssids[0].Locked != "" || ssids[2].Locked == "" {
		t.Errorf("only SSID5 must be locked under band steering: %+v", ssids)
	}
}

func TestGateway_SetWiFiSSID(t *testing.T) {
	state := ssidState()
	srv := testServerWithState("user", "secret", state)
	defer srv.Close()
	gw := loginGateway(t, srv.URL)
	ctx := context.Background()

	on, name, pass := true, "Clients", "client-pass-42"
	if err := gw.SetWiFiSSID(ctx, "DEV.WIFI.AP3", domain.WiFiSSIDChange{Enabled: &on, Name: &name, Password: &pass}); err != nil {
		t.Fatalf("SetWiFiSSID: %v", err)
	}
	if got := state.ssids[1]; got.enable != "1" || got.name != "Clients" || got.psk != pass || got.hide != "0" {
		t.Errorf("SSID3 after change = %+v", got)
	}

	// Switching off alone must re-send the password unchanged.
	off := false
	if err := gw.SetWiFiSSID(ctx, "DEV.WIFI.AP3", domain.WiFiSSIDChange{Enabled: &off}); err != nil {
		t.Fatalf("SetWiFiSSID off: %v", err)
	}
	if got := state.ssids[1]; got.enable != "0" || got.psk != pass || got.name != "Clients" {
		t.Errorf("SSID3 after switch-off = %+v", got)
	}
	if state.ssids[0].enable != "1" || state.ssids[0].psk != "main-pass-1" {
		t.Errorf("SSID1 touched: %+v", state.ssids[0])
	}
}

func TestGateway_SetWiFiSSIDRefusesBandSteerFollower(t *testing.T) {
	state := ssidState()
	srv := testServerWithState("user", "secret", state)
	defer srv.Close()
	gw := loginGateway(t, srv.URL)

	off := false
	err := gw.SetWiFiSSID(context.Background(), "DEV.WIFI.AP5", domain.WiFiSSIDChange{Enabled: &off})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if len(state.posts) != 0 {
		t.Errorf("posted despite the lock: %v", state.posts)
	}
}

func TestSSIDForm_FieldOrder(t *testing.T) {
	state := ssidState()
	srv := testServerWithState("user", "secret", state)
	defer srv.Close()
	gw := loginGateway(t, srv.URL)

	on := true
	if err := gw.SetWiFiSSID(context.Background(), "DEV.WIFI.AP3", domain.WiFiSSIDChange{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, kv := range strings.Split(state.posts[0], "&") {
		k, _, _ := strings.Cut(kv, "=")
		names = append(names, k)
	}
	// The web UI's InitialPostData order for template_WLANSSIDConf.
	want := "IF_ACTION Enable _InstID _WEPCONIG _PSKCONIG BeaconType WEPAuthMode WPAAuthMode 11iAuthMode " +
		"WPAEncryptType 11iEncryptType WPA3AuthMode WPA3EncryptType _InstID_WEP0 _InstID_WEP1 _InstID_WEP2 " +
		"_InstID_WEP3 _InstID_PSK MasterAuthServerIp BackupAuthServerIp MasterAcctServerIp BackupAcctServerIp " +
		"_InstID_GUEST _GUEST GuestWifi ESSID ESSIDHideEnable EncryptionType KeyPassphrase WEPKeyIndex ShowWEPKey " +
		"WEPKey00 WEPKey01 WEPKey02 WEPKey03 encode _sessionTOKEN"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("fields:\n got %s\nwant %s", got, want)
	}
}

func TestParaValueRoundTrip(t *testing.T) {
	for _, v := range []string{"", "a", "exactly16bytes!!", "a much longer Wi-Fi passphrase, 63 chars at most..."} {
		enc, err := encodeParaValue(v, "1234567890123456", "6543210987654321")
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeParaValue(enc, "1234567890123456", "6543210987654321")
		if err != nil || got != v {
			t.Errorf("round trip %q: got %q, %v", v, got, err)
		}
	}
	enc, _ := encodeParaValue("secret-pass", "right-key", "right-iv")
	if _, err := decodeParaValue(enc, "wrong-key", "right-iv"); err == nil {
		t.Error("decoding with the wrong key must fail, not return noise")
	}
}

func TestSecurityMode(t *testing.T) {
	cases := map[string]map[string]string{
		"No Security":   {"BeaconType": "None"},
		"WPA2-PSK-AES":  {"BeaconType": "11i", "11iAuthMode": "PSKAuthentication", "11iEncryptType": "AESEncryption"},
		"WPA2/WPA3-SAE": {"BeaconType": "11iandWPA3", "11iAuthMode": "PSKAuthentication", "WPA3AuthMode": "SAEAuthentication", "11iEncryptType": "TKIPandAESEncryption", "WPA3EncryptType": "AESEncryption"},
		"":              {"BeaconType": "Mystery"},
	}
	for want, ap := range cases {
		if got := securityMode(ap); got != want {
			t.Errorf("securityMode(%v) = %q, want %q", ap, got, want)
		}
	}
	if usesPassphrase("No Security") || usesPassphrase("WEP-OpenSystem") || !usesPassphrase("WPA3-SAE") {
		t.Error("usesPassphrase misclassifies modes")
	}
}
