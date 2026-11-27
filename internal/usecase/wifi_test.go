package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

func wifiSetup() (*fakeGateway, *usecase.WiFiAccess, usecase.MACFilterInput) {
	gw := &fakeGateway{meta: f660Meta(), ssids: []domain.WiFiSSID{
		{ID: "AP1", Alias: "SSID1", Enabled: true},
		{ID: "AP3", Alias: "SSID3"},
		{ID: "AP5", Alias: "SSID5", Enabled: false, Locked: "suit SSID1"},
	}}
	in := usecase.MACFilterInput{BaseURL: "http://192.168.1.1", AdapterID: "zte_f660",
		Creds: domain.Credentials{Username: "admin", Password: "x"}}
	return gw, usecase.NewWiFiAccess(fakeFactory{gw: gw}, newVault(), fakeDisco{}), in
}

func TestWiFiAccess_SetSSID(t *testing.T) {
	ctx := context.Background()
	on, off, short := true, false, "1234567"
	cases := []struct {
		name   string
		id     string
		change domain.WiFiSSIDChange
		ok     bool
		// lockout: refused as cutting the house off the Wi-Fi (#113),
		// rather than as an invalid input.
		lockout bool
	}{
		{"enable a spare SSID", "AP3", domain.WiFiSSIDChange{Enabled: &on}, true, false},
		{"switch off the last enabled SSID", "AP1", domain.WiFiSSIDChange{Enabled: &off}, false, true},
		{"locked SSID", "AP5", domain.WiFiSSIDChange{Enabled: &on}, false, false},
		{"unknown SSID", "AP9", domain.WiFiSSIDChange{Enabled: &on}, false, false},
		{"empty change", "AP3", domain.WiFiSSIDChange{}, false, false},
		{"password too short", "AP3", domain.WiFiSSIDChange{Password: &short}, false, false},
	}
	for _, c := range cases {
		gw, uc, in := wifiSetup()
		err := uc.SetSSID(ctx, in, c.id, c.change)
		if c.ok && err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if !c.ok && !c.lockout && !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", c.name, err)
		}
		var lock *domain.SelfLockoutError
		if c.lockout && (!errors.As(err, &lock) || lock.Reason != domain.SelfLockoutLastNetwork) {
			t.Errorf("%s: err = %v, want a last-network lockout", c.name, err)
		}
		if wrote := len(gw.ssidSets) > 0; wrote != c.ok {
			t.Errorf("%s: router written = %v, want %v", c.name, wrote, c.ok)
		}
	}
}

func TestWiFiSSIDChange_ValidateTrimsName(t *testing.T) {
	name := "  Clients  "
	c := domain.WiFiSSIDChange{Name: &name}
	if err := c.Validate(); err != nil || *c.Name != "Clients" {
		t.Fatalf("Validate: %v, name %q", err, *c.Name)
	}
	bad := "Café"
	if err := (&domain.WiFiSSIDChange{Name: &bad}).Validate(); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("non-ASCII name accepted: %v", err)
	}
}
