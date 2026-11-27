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
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/vendor"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/clock"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/logging"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// RFC-007 D6 (#102, #103) through the API: « liste pleine » as a stable code with
// its numbers, and the daily purge where the box is already read.

func purgingAPI(t *testing.T, box *mockrouter.F6600P) *httptest.Server {
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
	devRepo := sqlite.NewDeviceRepo(db)
	purge := usecase.NewPurgeStaleRules(factory, vlt, disco, devRepo, sqlite.NewRulePurgeRepo(db), clk, logger)
	api := httpapi.New(httpapi.Services{
		List:    usecase.NewListDevices(factory, vlt, devRepo, vendor.New(nil), disco, clk, logger),
		Block:   usecase.NewBlockDevice(factory, vlt, disco, clk, logger).WithPurge(purge),
		Unblock: usecase.NewUnblockDevice(factory, vlt, disco, clk, logger),
		WiFi:    usecase.NewWiFiAccess(factory, vlt, disco),
		Purge:   purge,
	}, httpapi.Config{BaseURL: router.URL, AdapterID: "zte_f6600p", Version: "test"})
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func postFor(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

// TestAPI_BlockListFull: the box refuses the add, nothing stale to purge: 409
// access_list_full, with the list and its numbers for the app's sentence.
func TestAPI_BlockListFull(t *testing.T) {
	box := mockrouter.NewF6600P("admin", "admin")
	used := len(box.FirewallRules())
	box.SetRuleLimits(used, 0)
	api := purgingAPI(t, box)
	code, body := postFor(t, api.URL+"/api/devices/02:aa:bb:00:00:51/block")
	if code != http.StatusConflict {
		t.Fatalf("block = %d %v, want 409", code, body)
	}
	if body["code"] != httpapi.AccessListFullCode || body["kind"] != "firewall" ||
		body["limit"] != float64(used) || body["used"] != float64(used) {
		t.Fatalf("body = %v, want access_list_full firewall %d/%d", body, used, used)
	}
}

// TestAPI_WiFiBlockListFull: the same answer for the Wi-Fi list.
func TestAPI_WiFiBlockListFull(t *testing.T) {
	box := mockrouter.NewF6600P("admin", "admin")
	box.SetRuleLimits(0, 1)
	box.SetWiFiPolicy("DEV.WIFI.AP1", "Ban")
	box.AddWiFiRule("02:aa:bb:00:00:52", "DEV.WIFI.AP1", "02:aa:bb:00:00:52 AP1")
	api := purgingAPI(t, box)
	code, body := postFor(t, api.URL+"/api/devices/02:aa:bb:00:00:53/wifi-block")
	if code != http.StatusConflict || body["code"] != httpapi.AccessListFullCode || body["kind"] != "wifi" || body["limit"] != float64(1) {
		t.Fatalf("wifi-block = %d %v, want 409 access_list_full wifi 1", code, body)
	}
}

// TestAPI_DevicesRunsTheDailyPurge: listing the devices is where the box is
// read anyway; once a day it also purges. A device on the box that the Wi-Fi
// refuses gets its missing firewall rule there.
func TestAPI_DevicesRunsTheDailyPurge(t *testing.T) {
	box := mockrouter.NewF6600P("admin", "admin")
	here := strings.ToLower(box.Stations()[0].MAC)
	box.SetWiFiPolicy("DEV.WIFI.AP1", "Ban")
	box.AddWiFiRule(here, "DEV.WIFI.AP1", here+" AP1")
	api := purgingAPI(t, box)
	if code, _ := getJSON(t, api.URL+"/api/devices"); code != http.StatusOK {
		t.Fatalf("devices = %d", code)
	}
	if !box.Blocked(here) {
		t.Fatal("the daily purge did not run with the device list")
	}
	if box.WiFiWrites() != 0 {
		t.Errorf("%d Wi-Fi writes, want none", box.WiFiWrites())
	}
}

// blockedList reads GET /api/blocked.
func blockedList(t *testing.T, api string) map[string]struct{ Firewall, WiFi bool } {
	t.Helper()
	resp, err := http.Get(api + "/api/blocked")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Devices []struct {
			MAC      string `json:"mac"`
			Firewall bool   `json:"firewall"`
			WiFi     bool   `json:"wifi"`
		} `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/blocked = %d, %v", resp.StatusCode, err)
	}
	out := map[string]struct{ Firewall, WiFi bool }{}
	for _, d := range body.Devices {
		out[d.MAC] = struct{ Firewall, WiFi bool }{d.Firewall, d.WiFi}
	}
	return out
}

// TestAPI_UnknownRemovedByHand: a MAC the inventory does not know keeps its
// rules through the daily purge (RFC-007 D6, Q4), and the « liste pleine »
// screen frees it by hand, from both lists.
func TestAPI_UnknownRemovedByHand(t *testing.T) {
	const unknown = "02:aa:bb:00:00:56"
	box := mockrouter.NewF6600P("admin", "admin")
	box.AddFirewallRule(unknown, unknown)
	for _, ap := range []string{"AP1", "AP5"} {
		box.SetWiFiPolicy("DEV.WIFI."+ap, "Ban")
		box.AddWiFiRule(unknown, "DEV.WIFI."+ap, unknown+" "+ap)
	}
	api := purgingAPI(t, box)
	if code, _ := getJSON(t, api.URL+"/api/devices"); code != http.StatusOK {
		t.Fatalf("devices = %d", code)
	}
	if got := blockedList(t, api.URL)[unknown]; !got.Firewall || !got.WiFi {
		t.Fatalf("%s after the purge = %+v, want both rules kept", unknown, got)
	}
	if code, body := postFor(t, api.URL+"/api/devices/"+unknown+"/unblock"); code != http.StatusOK {
		t.Fatalf("unblock = %d %v", code, body)
	}
	if code, body := postFor(t, api.URL+"/api/devices/"+unknown+"/wifi-unblock"); code != http.StatusOK {
		t.Fatalf("wifi-unblock = %d %v", code, body)
	}
	if box.Blocked(unknown) || box.WiFiRuleCount() != 0 {
		t.Fatalf("firewall blocked=%v, %d Wi-Fi rules left", box.Blocked(unknown), box.WiFiRuleCount())
	}
	if _, listed := blockedList(t, api.URL)[unknown]; listed {
		t.Error("still listed after the removal by hand")
	}
}

// TestAPI_BlockedDevices: the box's lists, absent devices included, for the
// « liste pleine » screen.
func TestAPI_BlockedDevices(t *testing.T) {
	box := mockrouter.NewF6600P("admin", "admin")
	box.AddFirewallRule("02:aa:bb:00:00:54", "02:aa:bb:00:00:54")
	api := purgingAPI(t, box)
	resp, err := http.Get(api.URL + "/api/blocked")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Devices []struct {
			MAC      string `json:"mac"`
			Present  bool   `json:"present"`
			Firewall bool   `json:"firewall"`
			ByKergui bool   `json:"by_kergui"`
			LastSeen string `json:"last_seen"`
		} `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/blocked = %d, %v", resp.StatusCode, err)
	}
	found := false
	for _, d := range body.Devices {
		if d.MAC == "02:aa:bb:00:00:54" {
			found = d.Firewall && d.ByKergui && !d.Present && d.LastSeen == ""
		}
	}
	if !found || len(body.Devices) != len(box.FirewallRules()) {
		t.Fatalf("devices = %+v", body.Devices)
	}
}
