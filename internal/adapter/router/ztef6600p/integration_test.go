package ztef6600p

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/fixtures"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

func testServer(user, pass string) *httptest.Server {
	loggedIn := false
	viewLoaded := map[string]bool{}

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
				loggedIn = true
				http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "valid-session"})
				w.Write([]byte(`{"sess_token":"new-token","login_need_refresh":true}`))
			} else {
				w.Write([]byte(`{"loginErrMsg":"auth failed"}`))
			}

		case typ == "menuView":
			if !loggedIn {
				w.Write([]byte(`<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`))
				return
			}
			viewLoaded[tag] = true
			w.Write([]byte(`<script>$(document).ready(function(){});</script>`))

		case typ == "menuData":
			if !loggedIn {
				w.Write([]byte(`<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`))
				return
			}
			switch tag {
			case "devmgr_statusmgr_lua.lua":
				if !viewLoaded["statusMgr"] {
					w.Write([]byte(`<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`))
					return
				}
				w.Write([]byte(fixtures.ZTEF6600PStatus))
			case "wlan_client_stat_lua.lua":
				if !viewLoaded["localNetStatus"] {
					w.Write([]byte(`<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`))
					return
				}
				w.Write([]byte(fixtures.ZTEF6600PWLANClients))
			case "firewall_macfilterv3_lua.lua":
				if !viewLoaded["filterCriteria"] {
					w.Write([]byte(`<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`))
					return
				}
				w.Write([]byte(fixtures.ZTEF6600PMACFilter))
			default:
				http.NotFound(w, r)
			}

		default:
			w.Write([]byte(`<html>main</html>`))
		}
	}))
}

func sha256hex_test(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestGateway_LoginAndDevices(t *testing.T) {
	srv := testServer("user", "secret")
	defer srv.Close()

	gw, err := New(srv.URL, port.RouterOptions{})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := gw.Login(ctx, domain.Credentials{Username: "user", Password: "secret"}); err != nil {
		t.Fatalf("Login: %v", err)
	}

	devs, err := gw.Devices(ctx)
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if len(devs) != 3 {
		t.Errorf("got %d devices, want 3", len(devs))
	}

	// First device should be marked as blocked (it's in the MAC filter)
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

	gw, err := New(srv.URL, port.RouterOptions{})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := gw.Login(ctx, domain.Credentials{Username: "user", Password: "secret"}); err != nil {
		t.Fatal(err)
	}

	info, err := gw.RouterInfo(ctx)
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

	gw, err := New(srv.URL, port.RouterOptions{})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	gw.Login(ctx, domain.Credentials{Username: "user", Password: "secret"})

	rules, err := gw.AccessRules(ctx)
	if err != nil {
		t.Fatalf("AccessRules: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want 2", len(rules))
	}
	if rules[1].MAC.String() != "ac:bb:cc:00:00:99" {
		t.Errorf("orphan rule MAC = %s, want ac:bb:cc:00:00:99", rules[1].MAC)
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
	loggedIn := false
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
			loggedIn = true
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "valid-session"})
			w.Write([]byte(`{"sess_token":"new-token","login_need_refresh":true}`))

		case typ == "menuView":
			if !loggedIn {
				w.Write([]byte(`<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`))
				return
			}
			w.Write([]byte(`<script>ready</script>`))

		case typ == "menuData" && tag == "devmgr_statusmgr_lua.lua":
			requestCount++
			if requestCount == 1 {
				// Expire session on first data request
				loggedIn = false
				w.Write([]byte(`<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`))
				return
			}
			if !loggedIn {
				w.Write([]byte(`<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`))
				return
			}
			w.Write([]byte(fixtures.ZTEF6600PStatus))

		default:
			w.Write([]byte(`<html>main</html>`))
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

	// This should fail with SessionTimeout, then auto-refresh and succeed
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

func TestGateway_WriteNotImplemented(t *testing.T) {
	srv := testServer("user", "secret")
	defer srv.Close()

	gw, err := New(srv.URL, port.RouterOptions{})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	gw.Login(ctx, domain.Credentials{Username: "user", Password: "secret"})

	mac, _ := domain.ParseMAC("ac:bb:cc:00:00:11")
	err = gw.Block(ctx, mac)
	if err == nil {
		t.Fatal("expected Block to return ErrNotImplemented")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("error = %v, want not-implemented error", err)
	}
}
