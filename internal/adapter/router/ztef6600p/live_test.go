package ztef6600p

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// TestLive_ObserveFieldAvailability runs Observe against a real F6600P and
// logs, per field, how many stations the firmware filled. It only reads, but
// it logs in: the router keeps one session, so any other client (browser,
// kergui serve) is logged out. Nothing identifying is printed.
//
//	KERGUI_LIVE_ROUTER=https://192.168.1.1 KERGUI_ROUTER_USER=user \
//	KERGUI_ROUTER_PASSWORD=... go test -run Live -v ./internal/adapter/router/ztef6600p/
func TestLive_ObserveFieldAvailability(t *testing.T) {
	base := os.Getenv("KERGUI_LIVE_ROUTER")
	if base == "" {
		t.Skip("set KERGUI_LIVE_ROUTER, KERGUI_ROUTER_USER and KERGUI_ROUTER_PASSWORD to run against a real router")
	}
	user := os.Getenv("KERGUI_ROUTER_USER")
	if user == "" {
		user = "user"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	g, err := New(base, port.RouterOptions{InsecureTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Login(ctx, domain.Credentials{Username: user, Password: os.Getenv("KERGUI_ROUTER_PASSWORD")}); err != nil {
		t.Fatal(err)
	}
	info, err := g.RouterInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	obs, err := g.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	for _, o := range obs {
		inc := func(field string, ok bool) {
			if ok {
				count[field]++
			}
		}
		inc("associated", o.Associated)
		inc("random_mac", o.MAC.IsRandomized())
		inc("ipv4", !o.IPv4.IsZero())
		inc("hostname", o.Hostname != "")
		inc("ssid", o.SSID != "")
		inc("band", o.Band != "")
		inc("assoc_since", !o.AssocSince.IsZero())
		inc("dhcp_lease", o.Lease != nil)
		inc("radio_caps", o.Radio.Known())
	}
	t.Logf("model=%s firmware=%s hardware=%s stations=%d", info.Model, info.Firmware, info.HardwareVer, len(obs))
	for _, f := range []string{"associated", "random_mac", "ipv4", "hostname", "ssid", "band", "assoc_since",
		"dhcp_lease", "radio_caps"} {
		t.Logf("  %-18s %d/%d", f, count[f], len(obs))
	}
	if count["associated"] == 0 || count["dhcp_lease"] == 0 {
		t.Errorf("expected associated stations and DHCP leases, got %v", count)
	}
}
