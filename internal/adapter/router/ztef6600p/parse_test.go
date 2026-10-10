package ztef6600p

import (
	"regexp"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/fixtures"
)

func TestParseRouterInfo(t *testing.T) {
	model, fw, hw := parseRouterInfo(fixtures.ZTEF6600PStatus)
	if model != "F6600P" {
		t.Errorf("model = %q, want F6600P", model)
	}
	if fw != "ZTEGF660006SN" {
		t.Errorf("firmware = %q, want ZTEGF660006SN", fw)
	}
	if hw != "ZTEGF6600PV90" {
		t.Errorf("hardware = %q, want ZTEGF6600PV90", hw)
	}
}

func TestParseWLANClients(t *testing.T) {
	devs := parseWLANClients(fixtures.ZTEF6600PWLANClients)
	if len(devs) != 3 {
		t.Fatalf("got %d devices, want 3", len(devs))
	}

	// Check first device
	d := devs[0]
	if d.MAC.String() != "ac:bb:cc:00:00:11" {
		t.Errorf("dev[0].MAC = %s, want ac:bb:cc:00:00:11", d.MAC)
	}
	if d.IP.String() != "192.168.1.10" {
		t.Errorf("dev[0].IP = %s, want 192.168.1.10", d.IP)
	}
	if d.Hostname != "Phone-Salon" {
		t.Errorf("dev[0].Hostname = %q, want Phone-Salon", d.Hostname)
	}
	if !d.Connected {
		t.Error("dev[0].Connected = false, want true")
	}
	if d.SSID != "MonWiFi-2G" {
		t.Errorf("dev[0].SSID = %q, want MonWiFi-2G", d.SSID)
	}

	// Third device is on 5GHz
	d2 := devs[2]
	if d2.SSID != "MonWiFi-5G" {
		t.Errorf("dev[2].SSID = %q, want MonWiFi-5G", d2.SSID)
	}
	if d2.MAC.String() != "ac:bb:cc:00:00:33" {
		t.Errorf("dev[2].MAC = %s, want ac:bb:cc:00:00:33", d2.MAC)
	}
}

func TestParseMACFilter(t *testing.T) {
	rules := parseMACFilter(fixtures.ZTEF6600PMACFilter)
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want 2", len(rules))
	}
	if rules[0].MAC.String() != "ac:bb:cc:00:00:11" {
		t.Errorf("rule[0].MAC = %s, want ac:bb:cc:00:00:11", rules[0].MAC)
	}
	if rules[0].Mode != domain.AccessBlock {
		t.Errorf("rule[0].Mode = %s, want block", rules[0].Mode)
	}
	// Second rule is an orphan MAC (not in devices list)
	if rules[1].MAC.String() != "ac:bb:cc:00:00:99" {
		t.Errorf("rule[1].MAC = %s, want ac:bb:cc:00:00:99", rules[1].MAC)
	}
}

func TestParseFilterGlobal(t *testing.T) {
	enabled, target := parseFilterGlobal(fixtures.ZTEF6600PFilterGlobal)
	if !enabled {
		t.Error("MacFilterEnable = false, want true")
	}
	if target != "Discard" {
		t.Errorf("MacFilterTarget = %q, want Discard", target)
	}
}

func TestParseLoginToken(t *testing.T) {
	token := parseLoginToken(fixtures.ZTEF6600PLoginToken)
	if token != "12345678" {
		t.Errorf("token = %q, want 12345678", token)
	}
}

func TestParseEmptyMACFilter(t *testing.T) {
	empty := `<ajax_response_xml_root><IF_ERRORPARAM>SUCC</IF_ERRORPARAM><IF_ERRORTYPE>SUCC</IF_ERRORTYPE><IF_ERRORSTR>SUCC</IF_ERRORSTR><IF_ERRORID>0</IF_ERRORID><OBJ_MACFILTER_ID></OBJ_MACFILTER_ID></ajax_response_xml_root>`
	rules := parseMACFilter(empty)
	if len(rules) != 0 {
		t.Errorf("got %d rules from empty filter, want 0", len(rules))
	}
}

func TestFixturesAreSanitised(t *testing.T) {
	realMAC := regexp.MustCompile(`(?i)([0-9a-f]{2}:){5}[0-9a-f]{2}`)
	placeholder := regexp.MustCompile(`(?i)ac:bb:cc:00:00:[0-9a-f]{2}`)

	fixtures := map[string]string{
		"status":        fixtures.ZTEF6600PStatus,
		"wlan_clients":  fixtures.ZTEF6600PWLANClients,
		"macfilter":     fixtures.ZTEF6600PMACFilter,
		"filter_global": fixtures.ZTEF6600PFilterGlobal,
	}
	for name, content := range fixtures {
		for _, m := range realMAC.FindAllString(content, -1) {
			if !placeholder.MatchString(m) {
				t.Errorf("%s: non-placeholder MAC found: %s", name, m)
			}
		}
	}
}
