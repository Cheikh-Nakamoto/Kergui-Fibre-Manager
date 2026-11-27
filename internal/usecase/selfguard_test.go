package usecase_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/discovery"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/catalog"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// UX audit 2026-11-26, C1 (#113): the core never cuts the device running
// Kergui off the box. On the F6600P mock: this phone is the station at the
// address the self locator gives, whatever its MAC (Android's is private).

const (
	guardPhone   = "02:4b:47:00:01:13" // this phone, on MonWiFi-2G (AP1)
	guardPhoneIP = "192.168.1.113"
	guardOther   = "02:4b:47:00:01:14" // another device, on MonWiFi-5G (AP5)
)

// selfAt is a port.SelfLocator the test moves: "" is a phone off the box's
// Wi-Fi.
type selfAt struct {
	mu sync.Mutex
	ip string
}

func (s *selfAt) SelfIP(context.Context, string) domain.IP {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ip == "" {
		return domain.IP{}
	}
	return domain.MustIP(s.ip)
}

func (s *selfAt) set(ip string) {
	s.mu.Lock()
	s.ip = ip
	s.mu.Unlock()
}

type guardRig struct {
	box   *mockrouter.F6600P
	in    usecase.MACFilterInput
	self  *selfAt
	block *usecase.BlockDevice
	wifi  *usecase.WiFiAccess
	purge *usecase.PurgeStaleRules
	r     *purgeRig // the inventory helpers
}

func newGuardRig(t *testing.T) *guardRig {
	t.Helper()
	box := mockrouter.NewF6600P("admin", "admin")
	box.Join(mockrouter.Station{MAC: guardPhone, Hostname: "Mon-Telephone", IP: guardPhoneIP, AP: "DEV.WIFI.AP1"})
	box.Join(mockrouter.Station{MAC: guardOther, Hostname: "Tablette", IP: "192.168.1.114", AP: "DEV.WIFI.AP5"})
	srv := httptest.NewServer(box.Handler())
	t.Cleanup(srv.Close)
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	factory := catalog.New()
	clk := &fakeClock{t: purgeNow}
	disco := discovery.New(factory, clk)
	vlt := newVault()
	log := &journal{}
	self := &selfAt{ip: guardPhoneIP}
	devices := sqlite.NewDeviceRepo(db)
	purge := usecase.NewPurgeStaleRules(factory, vlt, disco, devices, sqlite.NewRulePurgeRepo(db), clk, log).WithSelf(self)
	g := &guardRig{
		box: box, self: self, purge: purge,
		in: usecase.MACFilterInput{BaseURL: srv.URL, AdapterID: "zte_f6600p",
			Creds: domain.Credentials{Username: "admin", Password: "admin"}},
		block: usecase.NewBlockDevice(factory, vlt, disco, clk, log).WithPurge(purge).WithSelf(self),
		wifi:  usecase.NewWiFiAccess(factory, vlt, disco).WithSelf(self),
	}
	g.r = &purgeRig{box: box, in: g.in, devices: devices, clock: clk, log: log, purge: purge, block: g.block}
	return g
}

func (g *guardRig) blockMAC(t *testing.T, mac string) error {
	t.Helper()
	return g.block.Execute(context.Background(), usecase.ChangeAccessInput{
		BaseURL: g.in.BaseURL, AdapterID: g.in.AdapterID, Creds: g.in.Creds, MAC: mustMAC(t, mac), Confirm: true,
	})
}

func (g *guardRig) turnOff(id string) error {
	off := false
	return g.wifi.SetSSID(context.Background(), g.in, id, domain.WiFiSSIDChange{Enabled: &off})
}

func wantLockout(t *testing.T, what string, err error, reason string) {
	t.Helper()
	var lock *domain.SelfLockoutError
	if !errors.As(err, &lock) || !errors.Is(err, domain.ErrSelfLockout) || lock.Reason != reason {
		t.Fatalf("%s = %v, want a self lockout (%s)", what, err, reason)
	}
}

// TestSelfGuard_NeverBlocksThisPhone: neither « Bloquer » (the firewall, which
// on the F6600P may also cut the box's own pages: not checked) nor « Couper
// l'accès » reaches the box for this phone. Another device is blocked as
// usual, and once the phone has left the box's Wi-Fi the guard has nothing
// to spare.
func TestSelfGuard_NeverBlocksThisPhone(t *testing.T) {
	g := newGuardRig(t)
	ctx := context.Background()

	wantLockout(t, "Block(this phone)", g.blockMAC(t, guardPhone), domain.SelfLockoutDevice)
	if g.box.Blocked(guardPhone) {
		t.Fatal("this phone got a firewall rule")
	}
	wifiWrites := g.box.WiFiWrites()
	wantLockout(t, "WiFiBlock(this phone)", g.wifi.Set(ctx, g.in, mustMAC(t, guardPhone), true), domain.SelfLockoutDevice)
	if g.box.WiFiWrites() != wifiWrites || g.r.wifiMACs()[guardPhone] {
		t.Fatal("this phone was refused at the Wi-Fi")
	}
	// Giving access back is never refused.
	if err := g.wifi.Set(ctx, g.in, mustMAC(t, guardPhone), false); errors.Is(err, domain.ErrSelfLockout) {
		t.Fatalf("WiFiUnblock(this phone) = %v", err)
	}

	if err := g.blockMAC(t, guardOther); err != nil || !g.box.Blocked(guardOther) {
		t.Fatalf("Block(another device) = %v, blocked %v", err, g.box.Blocked(guardOther))
	}

	// The phone is known by its address, not by its MAC: a new private MAC
	// at the same address is still this phone.
	const rotated = "02:4b:47:00:01:15"
	g.box.Forget(guardPhone)
	g.box.Join(mockrouter.Station{MAC: rotated, Hostname: "Mon-Telephone", IP: guardPhoneIP, AP: "DEV.WIFI.AP1"})
	wantLockout(t, "Block(this phone, new MAC)", g.blockMAC(t, rotated), domain.SelfLockoutDevice)

	// Off the box's Wi-Fi, the device at that address is not this phone.
	g.self.set("")
	if err := g.blockMAC(t, rotated); err != nil {
		t.Fatalf("Block with the phone away = %v", err)
	}
}

// TestSelfGuard_NetworkOfThisPhoneStaysOn: the network this phone is on, by
// the SSID the device list gives it, is never turned off; another is. The
// last network still on is never turned off either, phone or not.
func TestSelfGuard_NetworkOfThisPhoneStaysOn(t *testing.T) {
	g := newGuardRig(t)

	wantLockout(t, "turning off MonWiFi-2G", g.turnOff("DEV.WIFI.AP1"), domain.SelfLockoutNetwork)
	if s, _ := g.box.SSID("DEV.WIFI.AP1"); !s.Enabled {
		t.Fatal("the network this phone is on was turned off")
	}
	if err := g.turnOff("DEV.WIFI.AP5"); err != nil {
		t.Fatalf("turning off MonWiFi-5G = %v", err)
	}
	if s, _ := g.box.SSID("DEV.WIFI.AP5"); s.Enabled {
		t.Fatal("MonWiFi-5G is still on")
	}

	// The phone is elsewhere (mobile data): MonWiFi-2G is now the last one.
	g.self.set("")
	wantLockout(t, "turning off the last network", g.turnOff("DEV.WIFI.AP1"), domain.SelfLockoutLastNetwork)
	if s, _ := g.box.SSID("DEV.WIFI.AP1"); !s.Enabled {
		t.Fatal("the last network was turned off")
	}

	// Turning a network on, renaming it or changing its password is not
	// turning it off.
	on, name := true, "Maison"
	if err := g.wifi.SetSSID(context.Background(), g.in, "DEV.WIFI.AP5", domain.WiFiSSIDChange{Enabled: &on}); err != nil {
		t.Fatalf("turning MonWiFi-5G back on = %v", err)
	}
	g.self.set(guardPhoneIP)
	if err := g.wifi.SetSSID(context.Background(), g.in, "DEV.WIFI.AP1", domain.WiFiSSIDChange{Name: &name}); err != nil {
		t.Fatalf("renaming this phone's network = %v", err)
	}
}

// TestSelfGuard_PurgeSparesThisPhone: the daily purge adds a firewall rule to
// a device the Wi-Fi refuses and that was seen lately (RFC-007 D6). This
// phone, refused on the 5 GHz network while it is on the 2.4 GHz one, gets
// none: it would lose the box. Another device in the same case does.
func TestSelfGuard_PurgeSparesThisPhone(t *testing.T) {
	g := newGuardRig(t)
	const refused = "02:4b:47:00:01:16"
	g.box.SetWiFiPolicy("DEV.WIFI.AP5", "Ban")
	for _, m := range []string{guardPhone, refused} {
		g.box.AddWiFiRule(m, "DEV.WIFI.AP5", m+" AP5")
		g.r.seen(t, m, day)
	}

	rep, err := g.purge.Run(context.Background(), g.in)
	if err != nil {
		t.Fatal(err)
	}
	if g.box.Blocked(guardPhone) {
		t.Fatalf("the purge blocked this phone: %+v", rep)
	}
	if !g.box.Blocked(refused) {
		t.Fatalf("the purge did not complete the other refusal: %+v", rep)
	}
}
