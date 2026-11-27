package ztef6600p

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/fixtures"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// RFC-007 D6: which rules kergui wrote, and « liste pleine ».

func mockBox(t *testing.T) (*mockrouter.F6600P, *Gateway) {
	t.Helper()
	box := mockrouter.NewF6600P("admin", "admin")
	srv := httptest.NewServer(box.Handler())
	t.Cleanup(srv.Close)
	gw, err := New(srv.URL, port.RouterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Login(context.Background(), domain.Credentials{Username: "admin", Password: "admin"}); err != nil {
		t.Fatal(err)
	}
	return box, gw
}

// TestParseMACFilter_ByKergui: the captured rules were named by the
// household (« Phone-Salon »), so kergui did not write them; a rule named
// after its own MAC is kergui's, whatever the case.
func TestParseMACFilter_ByKergui(t *testing.T) {
	for _, r := range parseMACFilter(fixtures.ZTEF6600PMACFilter) {
		if r.ByKergui {
			t.Errorf("%s: a household rule is reported as kergui's", r.MAC)
		}
	}
	page := `<ajax_response_xml_root><IF_ERRORSTR>SUCC</IF_ERRORSTR><OBJ_MACFILTER_ID><Instance>` +
		`<ParaName>_InstID</ParaName><ParaValue>DEV.FW.CHAIN1.MACF1</ParaValue>` +
		`<ParaName>Name</ParaName><ParaValue>02:AA:BB:00:00:01</ParaValue>` +
		`<ParaName>SrcMacAddr</ParaName><ParaValue>02:aa:bb:00:00:01</ParaValue></Instance></OBJ_MACFILTER_ID></ajax_response_xml_root>`
	rules := parseMACFilter(page)
	if len(rules) != 1 || !rules[0].ByKergui {
		t.Fatalf("rules = %+v, want one kergui rule", rules)
	}
}

// TestGateway_WiFiRules_Captured: the 63 rules read on a real box are
// 21 MACs x 3 access points, each named "<mac> APn": all kergui's.
func TestGateway_WiFiRules_Captured(t *testing.T) {
	box, gw := mockBox(t)
	box.LoadCapturedWiFiACL()
	rules, err := gw.WiFiRules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 63 {
		t.Fatalf("%d rules, want 63", len(rules))
	}
	macs := map[string]bool{}
	for _, r := range rules {
		if !r.ByKergui {
			t.Errorf("%s on %s: not recognised as kergui's", r.MAC, r.SSID)
		}
		if r.Mode != domain.AccessBlock {
			t.Errorf("%s on %s: mode %s, want block", r.MAC, r.SSID, r.Mode)
		}
		macs[r.MAC.String()] = true
	}
	if len(macs) != 21 {
		t.Errorf("%d MACs, want 21", len(macs))
	}
}

// TestGateway_WiFiRules_OnlyRefusals: a rule on an access point that is not
// in Ban refuses nobody, and a rule with another name is the household's.
func TestGateway_WiFiRules_OnlyRefusals(t *testing.T) {
	box, gw := mockBox(t)
	box.SetWiFiPolicy("DEV.WIFI.AP1", "Ban")
	box.AddWiFiRule("02:aa:bb:00:00:01", "DEV.WIFI.AP1", "02:aa:bb:00:00:01 AP1")
	box.AddWiFiRule("02:aa:bb:00:00:02", "DEV.WIFI.AP1", "Tablette voisin")
	box.AddWiFiRule("02:aa:bb:00:00:03", "DEV.WIFI.AP5", "02:aa:bb:00:00:03 AP5") // AP5 is Disabled
	rules, err := gw.WiFiRules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("rules = %+v, want the two on AP1", rules)
	}
	for _, r := range rules {
		want := r.MAC.String() == "02:aa:bb:00:00:01"
		if r.ByKergui != want {
			t.Errorf("%s: ByKergui = %v, want %v", r.MAC, r.ByKergui, want)
		}
	}
}

// TestGateway_BlockWhenFirewallFull: the box refuses an add with its « plein »
// answer; the adapter says the list is full, with the count it holds.
func TestGateway_BlockWhenFirewallFull(t *testing.T) {
	box, gw := mockBox(t)
	used := len(box.FirewallRules())
	box.SetRuleLimits(used, 0)
	err := gw.Block(context.Background(), mustMAC(t, "02:aa:bb:00:00:42"))
	var full *domain.AccessListFullError
	if !errors.As(err, &full) {
		t.Fatalf("Block = %v, want an AccessListFullError", err)
	}
	if full.Kind != domain.AccessListFirewall || full.Used != used || full.Limit != used {
		t.Errorf("full = %+v, want firewall %d/%d", full, used, used)
	}
	if box.Blocked("02:aa:bb:00:00:42") {
		t.Error("the mock took a rule beyond its limit")
	}
}

// TestGateway_WiFiBlockWhenFull: the shared Wi-Fi table counts one rule per
// access point; full, it is reported in devices.
func TestGateway_WiFiBlockWhenFull(t *testing.T) {
	box, gw := mockBox(t)
	for i := 1; i <= 2; i++ { // two devices on the mock's two enabled APs
		for _, ap := range []string{"AP1", "AP5"} {
			mac := fmt.Sprintf("02:aa:bb:00:00:%02x", i)
			box.AddWiFiRule(mac, "DEV.WIFI."+ap, mac+" "+ap)
		}
	}
	box.SetWiFiPolicy("DEV.WIFI.AP1", "Ban")
	box.SetWiFiPolicy("DEV.WIFI.AP5", "Ban")
	box.SetRuleLimits(0, 4)
	err := gw.WiFiBlock(context.Background(), mustMAC(t, "02:aa:bb:00:00:43"))
	var full *domain.AccessListFullError
	if !errors.As(err, &full) {
		t.Fatalf("WiFiBlock = %v, want an AccessListFullError", err)
	}
	if full.Kind != domain.AccessListWiFi || full.Used != 2 || full.Limit != 2 {
		t.Errorf("full = %+v, want wifi 2/2", full)
	}
}

// TestIsFullAnswer: the answers recognised as « plein », and an ordinary
// refusal that is not one.
func TestIsFullAnswer(t *testing.T) {
	for _, s := range []string{mockrouter.FullAnswer, "The number of entries has reached the maximum", "Liste pleine"} {
		if !isFullAnswer(s) {
			t.Errorf("%q not recognised as full", s)
		}
	}
	for _, s := range []string{"FAIL", "L'opération en cours est invalide", "SessionTimeout", ""} {
		if isFullAnswer(s) {
			t.Errorf("%q taken for full", s)
		}
	}
}

func mustMAC(t *testing.T, s string) domain.MAC {
	t.Helper()
	m, err := domain.ParseMAC(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestFullAnswer_LiveCapture: the answer a live F6600P gave on 2026-11-27 when
// its Wi-Fi rule table and its firewall MAC filter were full (RFC-007 W1, W7)
// is recognised as "list full".
func TestFullAnswer_LiveCapture(t *testing.T) {
	data, err := os.ReadFile("../../../fixtures/zte_f6600p/list_full.xml")
	if err != nil {
		t.Fatal(err)
	}
	root, err := parseAjaxXML(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if isSuccess(root) {
		t.Fatal("the full answer parsed as a success")
	}
	if !isFullAnswer(root.ErrorStr) {
		t.Fatalf("%q is not recognised as a full answer", root.ErrorStr)
	}
}
