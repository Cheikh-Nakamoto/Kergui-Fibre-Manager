package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/controller/httpapi"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/discovery"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/vault"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/catalog"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/clock"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/logging"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// UX audit 2026-11-26, C1 (#113) through the API: a write that would cut the
// device running Kergui off the box is a 409 self_lockout, with its reason
// for the app's sentence, and nothing reaches the box.

const (
	lockPhone   = "02:4b:47:00:01:13"
	lockPhoneIP = "192.168.1.113"
)

type fixedSelfIP string

func (s fixedSelfIP) SelfIP(context.Context, string) domain.IP { return domain.MustIP(string(s)) }

func guardedAPI(t *testing.T, box *mockrouter.F6600P) *httptest.Server {
	t.Helper()
	router := httptest.NewServer(box.Handler())
	t.Cleanup(router.Close)
	factory := catalog.New()
	clk := clock.Real{}
	disco := discovery.New(factory, clk)
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	vlt := vault.New(sqlite.NewCredentialStore(db), "test-key")
	if err := vlt.Store(context.Background(), usecase.RouterID(router.URL), domain.Credentials{Username: "admin", Password: "admin"}); err != nil {
		t.Fatal(err)
	}
	logger := logging.New(io.Discard, false)
	self := fixedSelfIP(lockPhoneIP)
	api := httpapi.New(httpapi.Services{
		Block: usecase.NewBlockDevice(factory, vlt, disco, clk, logger).WithSelf(self),
		WiFi:  usecase.NewWiFiAccess(factory, vlt, disco).WithSelf(self),
	}, httpapi.Config{BaseURL: router.URL, AdapterID: "zte_f6600p", Version: "test"})
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func patchFor(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func wantSelfLockout(t *testing.T, what string, code int, body map[string]any, reason string) {
	t.Helper()
	if code != http.StatusConflict || body["code"] != httpapi.SelfLockoutCode || body["reason"] != reason {
		t.Fatalf("%s = %d %v, want 409 %s %s", what, code, body, httpapi.SelfLockoutCode, reason)
	}
}

func TestAPI_SelfLockout(t *testing.T) {
	box := mockrouter.NewF6600P("admin", "admin")
	box.Join(mockrouter.Station{MAC: lockPhone, Hostname: "Mon-Telephone", IP: lockPhoneIP, AP: "DEV.WIFI.AP1"})
	api := guardedAPI(t, box)

	code, body := postFor(t, api.URL+"/api/devices/"+lockPhone+"/block")
	wantSelfLockout(t, "block this phone", code, body, domain.SelfLockoutDevice)
	code, body = postFor(t, api.URL+"/api/devices/"+lockPhone+"/wifi-block")
	wantSelfLockout(t, "wifi-block this phone", code, body, domain.SelfLockoutDevice)
	if box.Blocked(lockPhone) || box.WiFiWrites() != 0 {
		t.Fatalf("this phone reached the box: blocked %v, %d Wi-Fi writes", box.Blocked(lockPhone), box.WiFiWrites())
	}

	code, body = patchFor(t, api.URL+"/api/wifi/ssids/DEV.WIFI.AP1", `{"enabled":false}`)
	wantSelfLockout(t, "turning off this phone's network", code, body, domain.SelfLockoutNetwork)
	if code, body := patchFor(t, api.URL+"/api/wifi/ssids/DEV.WIFI.AP5", `{"enabled":false}`); code != http.StatusOK {
		t.Fatalf("turning off the other network = %d %v", code, body)
	}
	if s, _ := box.SSID("DEV.WIFI.AP1"); !s.Enabled {
		t.Fatal("this phone's network was turned off")
	}
}
