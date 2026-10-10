package ztef6600p

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/fixtures"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

const mockToken = "rttY3AfvuLqPpAuBdKvfyISm"

var mockKey, mockMainPage = func() (*rsa.PrivateKey, string) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	js := strings.ReplaceAll(strings.TrimSpace(pemStr), "\n", `\n`)
	return k, `<html><script>var pubKey = "` + js + `";</script>` +
		`<input type="hidden" name="_sessionTOKEN" id="_sessionTOKEN" value=""/></html>`
}()

func hexEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		fmt.Fprintf(&b, `\x%02x`, s[i])
	}
	return b.String()
}

type mockRule struct{ instID, mac string }

type mockState struct {
	loggedIn   bool
	viewLoaded map[string]bool
	rules      []mockRule
	nextID     int
	posts      []string
}

func (st *mockState) add(mac string) {
	st.nextID++
	st.rules = append(st.rules, mockRule{instID: fmt.Sprintf("DEV.FW.MACFILTER%d", st.nextID), mac: mac})
}

func testServer(user, pass string) *httptest.Server {
	state := &mockState{viewLoaded: map[string]bool{}}
	state.add("AC:BB:CC:00:00:11")
	state.add("AC:BB:CC:00:00:99")
	return testServerWithState(user, pass, state)
}

func xmlErr(w http.ResponseWriter, msg string) {
	w.Write([]byte(`<ajax_response_xml_root><IF_ERRORSTR>` + msg + `</IF_ERRORSTR></ajax_response_xml_root>`))
}

// checkIntegrity mirrors the router: the Check header must be the RSA
// encryption of sha256hex(body).
func checkIntegrity(r *http.Request, body string) bool {
	enc, err := base64.StdEncoding.DecodeString(r.Header.Get("Check"))
	if err != nil {
		return false
	}
	plain, err := rsa.DecryptPKCS1v15(nil, mockKey, enc)
	return err == nil && string(plain) == sha256hex(body)
}

func testServerWithState(user, pass string, state *mockState) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		typ := q.Get("_type")
		tag := q.Get("_tag")

		switch {
		case typ == "loginData" && tag == "login_entry" && r.Method == "GET":
			w.Header().Set("Set-Cookie", sessionCookie+"=placeholder; Path=/")
			w.Write([]byte(`{"sess_token":"test-session-token","lockingTime":0}`))

		case typ == "loginData" && tag == "login_token":
			w.Write([]byte(`<ajax_response_xml_root>12345678</ajax_response_xml_root>`))

		case typ == "loginData" && tag == "login_entry" && r.Method == "POST":
			r.ParseForm()
			wantHash := sha256hex(pass + "12345678")
			if r.FormValue("Username") == user && r.FormValue("Password") == wantHash {
				state.loggedIn = true
				http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "valid-session"})
				w.Write([]byte(`{"sess_token":"new-token","login_need_refresh":true}`))
			} else {
				w.Write([]byte(`{"loginErrMsg":"auth failed"}`))
			}

		case typ == "menuView":
			if !state.loggedIn {
				w.Write([]byte(`<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`))
				return
			}
			state.viewLoaded[tag] = true
			w.Write([]byte(`<script>_sessionTmpToken = "` + hexEscape(mockToken) + `";</script>`))

		case typ == "menuData" && r.Method == "POST":
			if !state.loggedIn {
				xmlErr(w, "SessionTimeout")
				return
			}
			rawBody, _ := io.ReadAll(r.Body)
			body := string(rawBody)
			state.posts = append(state.posts, body)
			vals, _ := url.ParseQuery(body)
			if vals.Get("_sessionTOKEN") != mockToken {
				xmlErr(w, "Cette page a expiré, actualisez et essayez à nouveau.")
				return
			}
			if !checkIntegrity(r, body) {
				xmlErr(w, "integrity check failed")
				return
			}
			switch vals.Get("IF_ACTION") {
			case "Apply":
				state.add(vals.Get("SrcMacAddr"))
				xmlErr(w, "SUCC")
			case "Delete":
				for i, rule := range state.rules {
					if rule.instID == vals.Get("_InstID") {
						state.rules = append(state.rules[:i], state.rules[i+1:]...)
						break
					}
				}
				xmlErr(w, "SUCC")
			default:
				xmlErr(w, "FAIL")
			}

		case typ == "menuData" && r.Method == "GET":
			if !state.loggedIn {
				w.Write([]byte(`<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`))
				return
			}
			switch tag {
			case "devmgr_statusmgr_lua.lua":
				if !state.viewLoaded["statusMgr"] {
					w.Write([]byte(`<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`))
					return
				}
				w.Write([]byte(fixtures.ZTEF6600PStatus))
			case "wlan_client_stat_lua.lua":
				if !state.viewLoaded["localNetStatus"] {
					w.Write([]byte(`<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`))
					return
				}
				w.Write([]byte(fixtures.ZTEF6600PWLANClients))
			case "firewall_macfilterv3_lua.lua":
				if !state.viewLoaded["filterCriteria"] {
					w.Write([]byte(`<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`))
					return
				}
				w.Write([]byte(renderMACFilter(state.rules)))
			default:
				http.NotFound(w, r)
			}

		default:
			w.Write([]byte(mockMainPage))
		}
	}))
}

func renderMACFilter(rules []mockRule) string {
	xml := `<ajax_response_xml_root><IF_ERRORPARAM>SUCC</IF_ERRORPARAM><IF_ERRORTYPE>SUCC</IF_ERRORTYPE><IF_ERRORSTR>SUCC</IF_ERRORSTR><IF_ERRORID>0</IF_ERRORID><OBJ_MACFILTER_ID>`
	for _, r := range rules {
		xml += fmt.Sprintf(`<Instance><ParaName>_InstID</ParaName><ParaValue>%s</ParaValue><ParaName>Name</ParaName><ParaValue>%s</ParaValue><ParaName>Type</ParaName><ParaValue>Route</ParaValue><ParaName>Protocol</ParaName><ParaValue>ALL</ParaValue><ParaName>SrcMacAddr</ParaName><ParaValue>%s</ParaValue></Instance>`, r.instID, r.mac, r.mac)
	}
	xml += `</OBJ_MACFILTER_ID></ajax_response_xml_root>`
	return xml
}

func sha256hex_test(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func loginGateway(t *testing.T, url string) *Gateway {
	t.Helper()
	gw, err := New(url, port.RouterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Login(context.Background(), domain.Credentials{Username: "user", Password: "secret"}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	return gw
}

func TestGateway_LoginAndDevices(t *testing.T) {
	srv := testServer("user", "secret")
	defer srv.Close()

	gw := loginGateway(t, srv.URL)
	ctx := context.Background()

	devs, err := gw.Devices(ctx)
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if len(devs) != 3 {
		t.Errorf("got %d devices, want 3", len(devs))
	}

	found := false
	for _, d := range devs {
		if d.MAC.String() == "ac:bb:cc:00:00:11" {
			found = true
			if !d.Blocked {
				t.Error("device ac:bb:cc:00:00:11 should be blocked (in MAC filter)")
			}
		}
	}
	if !found {
		t.Error("device ac:bb:cc:00:00:11 not found")
	}
}

func TestGateway_RouterInfo(t *testing.T) {
	srv := testServer("user", "secret")
	defer srv.Close()

	gw := loginGateway(t, srv.URL)
	info, err := gw.RouterInfo(context.Background())
	if err != nil {
		t.Fatalf("RouterInfo: %v", err)
	}
	if info.Model != "F6600P" {
		t.Errorf("Model = %q, want F6600P", info.Model)
	}
	if info.Firmware != "ZTEGF660006SN" {
		t.Errorf("Firmware = %q, want ZTEGF660006SN", info.Firmware)
	}
	if info.Vendor != "ZTE" {
		t.Errorf("Vendor = %q, want ZTE", info.Vendor)
	}
}

func TestGateway_AccessRules(t *testing.T) {
	srv := testServer("user", "secret")
	defer srv.Close()

	gw := loginGateway(t, srv.URL)
	rules, err := gw.AccessRules(context.Background())
	if err != nil {
		t.Fatalf("AccessRules: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want 2", len(rules))
	}
}

func TestGateway_LoginFailsWithBadPassword(t *testing.T) {
	srv := testServer("user", "secret")
	defer srv.Close()

	gw, err := New(srv.URL, port.RouterOptions{})
	if err != nil {
		t.Fatal(err)
	}

	err = gw.Login(context.Background(), domain.Credentials{Username: "user", Password: "wrong"})
	if err == nil {
		t.Fatal("expected login to fail with wrong password")
	}
	if !strings.Contains(err.Error(), "auth") {
		t.Errorf("error = %v, want auth-related error", err)
	}
}

func TestGateway_AutoSessionRefresh(t *testing.T) {
	state := &mockState{viewLoaded: map[string]bool{}}
	requestCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		typ := q.Get("_type")
		tag := q.Get("_tag")

		switch {
		case typ == "loginData" && tag == "login_entry" && r.Method == "GET":
			w.Header().Set("Set-Cookie", sessionCookie+"=placeholder; Path=/")
			w.Write([]byte(`{"sess_token":"test-session-token","lockingTime":0}`))

		case typ == "loginData" && tag == "login_token":
			w.Write([]byte(`<ajax_response_xml_root>12345678</ajax_response_xml_root>`))

		case typ == "loginData" && tag == "login_entry" && r.Method == "POST":
			state.loggedIn = true
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "valid-session"})
			w.Write([]byte(`{"sess_token":"new-token","login_need_refresh":true}`))

		case typ == "menuView":
			if !state.loggedIn {
				w.Write([]byte(`<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`))
				return
			}
			state.viewLoaded[tag] = true
			w.Write([]byte(`<script>ready</script>`))

		case typ == "menuData" && tag == "devmgr_statusmgr_lua.lua":
			requestCount++
			if requestCount == 1 {
				state.loggedIn = false
				w.Write([]byte(`<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`))
				return
			}
			if !state.loggedIn {
				w.Write([]byte(`<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`))
				return
			}
			w.Write([]byte(fixtures.ZTEF6600PStatus))

		default:
			w.Write([]byte(mockMainPage))
		}
	}))
	defer srv.Close()

	gw, err := New(srv.URL, port.RouterOptions{})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := gw.Login(ctx, domain.Credentials{Username: "user", Password: "secret"}); err != nil {
		t.Fatalf("Login: %v", err)
	}

	info, err := gw.RouterInfo(ctx)
	if err != nil {
		t.Fatalf("RouterInfo after session refresh: %v", err)
	}
	if info.Model != "F6600P" {
		t.Errorf("Model = %q, want F6600P", info.Model)
	}
	if requestCount < 2 {
		t.Errorf("expected at least 2 data requests (1 failed + 1 retry), got %d", requestCount)
	}
}

func TestGateway_BlockAndUnblock(t *testing.T) {
	state := &mockState{viewLoaded: map[string]bool{}}
	srv := testServerWithState("user", "secret", state)
	defer srv.Close()

	gw := loginGateway(t, srv.URL)
	ctx := context.Background()

	mac, _ := domain.ParseMAC("aa:bb:cc:dd:ee:ff")

	// Block
	if err := gw.Block(ctx, mac); err != nil {
		t.Fatalf("Block: %v", err)
	}

	rules, _ := gw.AccessRules(ctx)
	found := false
	for _, r := range rules {
		if r.MAC.String() == "aa:bb:cc:dd:ee:ff" {
			found = true
		}
	}
	if !found {
		t.Error("MAC aa:bb:cc:dd:ee:ff not in ACL after Block")
	}

	// Unblock
	if err := gw.Unblock(ctx, mac); err != nil {
		t.Fatalf("Unblock: %v", err)
	}

	rules, _ = gw.AccessRules(ctx)
	for _, r := range rules {
		if r.MAC.String() == "aa:bb:cc:dd:ee:ff" {
			t.Error("MAC aa:bb:cc:dd:ee:ff still in ACL after Unblock")
		}
	}

	wantApply := "IF_ACTION=Apply&_InstID=-1&SrcMacAddr=AA%3ABB%3ACC%3ADD%3AEE%3AFF&Protocol=ALL&Name=aa%3Abb%3Acc%3Add%3Aee%3Aff&Type=Route&select_protocol=ALL"
	if len(state.posts) != 2 || !strings.HasPrefix(state.posts[0], wantApply) {
		t.Fatalf("posts = %q, want first to start with %q", state.posts, wantApply)
	}
	if !strings.HasPrefix(state.posts[1], "IF_ACTION=Delete&_InstID=DEV.FW.MACFILTER1&") {
		t.Errorf("delete post = %q", state.posts[1])
	}
}

func TestGateway_BlockIsIdempotent(t *testing.T) {
	state := &mockState{viewLoaded: map[string]bool{}}
	state.add("AA:BB:CC:DD:EE:FF")
	srv := testServerWithState("user", "secret", state)
	defer srv.Close()

	gw := loginGateway(t, srv.URL)
	mac, _ := domain.ParseMAC("aa:bb:cc:dd:ee:ff")
	if err := gw.Block(context.Background(), mac); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if len(state.posts) != 0 || len(state.rules) != 1 {
		t.Errorf("already-blocked MAC was posted again: posts=%d rules=%d", len(state.posts), len(state.rules))
	}
}

func TestParseJSString(t *testing.T) {
	html := `x; _sessionTmpToken = "` + hexEscape(mockToken) + `"; y`
	if got := parseJSString(html, "_sessionTmpToken"); got != mockToken {
		t.Errorf("parseJSString = %q, want %q", got, mockToken)
	}
}
