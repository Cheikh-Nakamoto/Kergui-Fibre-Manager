package zte

import (
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/fixtures"
)

func TestExtractLoginToken(t *testing.T) {
	if got := extractLoginToken(fixtures.ZTEF660Login, "Frm_Logintoken"); got != "3" {
		t.Errorf("token = %q, want 3", got)
	}
	if got := extractLoginToken(fixtures.ZTEF660Login, "Absent"); got != "" {
		t.Errorf("absent token = %q, want empty", got)
	}
}

func TestParseRouterInfo(t *testing.T) {
	model, fw, hw := parseRouterInfo(fixtures.ZTEF660Status)
	if model != "ZXHN F660" || fw != "V6.0.10P2T2" || hw != "V6.0" {
		t.Errorf("router info = (%q,%q,%q)", model, fw, hw)
	}
}

func TestParseDevices(t *testing.T) {
	devs := parseDevices(fixtures.ZTEF660Devices)
	if len(devs) != 4 {
		t.Fatalf("want 4 devices, got %d: %+v", len(devs), devs)
	}
	byMAC := map[string]domain.Device{}
	for _, d := range devs {
		byMAC[d.MAC.String()] = d
	}

	phone := byMAC["ac:bb:cc:00:00:11"]
	if phone.Hostname != "Samsung-Galaxy" || phone.IP.String() != "192.168.1.10" || !phone.Connected || phone.SSID != "SSID-Maison" {
		t.Errorf("phone merged wrong: %+v", phone)
	}
	tv := byMAC["ac:bb:cc:00:00:33"]
	if tv.Hostname != "TV-Salon" || tv.Connected {
		t.Errorf("tv (inactive LAN host) wrong: %+v", tv)
	}
	wifiOnly := byMAC["ac:bb:cc:00:00:44"]
	if !wifiOnly.Connected || wifiOnly.IP.String() != "" {
		t.Errorf("wifi-only device wrong: %+v", wifiOnly)
	}
}

func TestParseAccessRules(t *testing.T) {
	rules := parseAccessRules(fixtures.ZTEF660AccessControl)
	if len(rules) != 2 {
		t.Fatalf("want 2 rules, got %d", len(rules))
	}
	for _, r := range rules {
		if r.Mode != domain.AccessBlock {
			t.Errorf("rule %s mode = %s, want block", r.MAC, r.Mode)
		}
	}
	if rules[0].MAC.String() != "ac:bb:cc:00:00:33" {
		t.Errorf("first rule MAC = %s", rules[0].MAC)
	}
}
