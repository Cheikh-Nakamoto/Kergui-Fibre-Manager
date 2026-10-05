package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// --- fakes -----------------------------------------------------------------

type fakeClock struct{ t time.Time }

func (c fakeClock) Now() time.Time { return c.t }

type nopLogger struct{}

func (nopLogger) Debugf(string, ...any) {}
func (nopLogger) Infof(string, ...any)  {}
func (nopLogger) Warnf(string, ...any)  {}
func (nopLogger) Errorf(string, ...any) {}

type fakeDisco struct {
	info domain.RouterInfo
	err  error
}

func (d fakeDisco) Discover(context.Context, string, port.RouterOptions) (domain.RouterInfo, error) {
	return d.info, d.err
}

type fakeGateway struct {
	loginErr error
	devices  []domain.Device
	rules    []domain.AccessRule
	meta     domain.AdapterMeta
	loggedIn bool
}

func (g *fakeGateway) Login(context.Context, domain.Credentials) error {
	if g.loginErr != nil {
		return g.loginErr
	}
	g.loggedIn = true
	return nil
}
func (g *fakeGateway) RouterInfo(context.Context) (*domain.RouterInfo, error) { return nil, nil }
func (g *fakeGateway) Devices(context.Context) ([]domain.Device, error)       { return g.devices, nil }
func (g *fakeGateway) AccessRules(context.Context) ([]domain.AccessRule, error) {
	return g.rules, nil
}
func (g *fakeGateway) Block(context.Context, domain.MAC) error   { return domain.ErrNotImplemented }
func (g *fakeGateway) Unblock(context.Context, domain.MAC) error { return domain.ErrNotImplemented }
func (g *fakeGateway) Meta() domain.AdapterMeta                  { return g.meta }

type fakeFactory struct{ gw *fakeGateway }

func (f fakeFactory) New(string, string, port.RouterOptions) (port.RouterPort, error) {
	return f.gw, nil
}
func (f fakeFactory) Available() []domain.AdapterMeta { return []domain.AdapterMeta{f.gw.meta} }
func (f fakeFactory) Lookup(string) (domain.AdapterMeta, bool) {
	return f.gw.meta, true
}

type fakeVault struct {
	stored map[string]domain.Credentials
}

func newVault() *fakeVault { return &fakeVault{stored: map[string]domain.Credentials{}} }
func (v *fakeVault) Store(_ context.Context, id string, c domain.Credentials) error {
	v.stored[id] = c
	return nil
}
func (v *fakeVault) Load(_ context.Context, id string) (domain.Credentials, bool, error) {
	c, ok := v.stored[id]
	return c, ok, nil
}
func (v *fakeVault) Delete(_ context.Context, id string) error { delete(v.stored, id); return nil }

type fakeRouterRepo struct{ saved map[string]domain.Router }

func newRouterRepo() *fakeRouterRepo { return &fakeRouterRepo{saved: map[string]domain.Router{}} }
func (r *fakeRouterRepo) Upsert(_ context.Context, rt domain.Router) error {
	r.saved[rt.ID] = rt
	return nil
}
func (r *fakeRouterRepo) Get(_ context.Context, id string) (domain.Router, bool, error) {
	rt, ok := r.saved[id]
	return rt, ok, nil
}
func (r *fakeRouterRepo) List(context.Context) ([]domain.Router, error) { return nil, nil }

type fakeDeviceRepo struct{ byMAC map[string]domain.Device }

func newDeviceRepo() *fakeDeviceRepo { return &fakeDeviceRepo{byMAC: map[string]domain.Device{}} }
func (r *fakeDeviceRepo) Upsert(_ context.Context, d domain.Device) (bool, error) {
	key := d.RouterID + "|" + d.MAC.String()
	_, existed := r.byMAC[key]
	r.byMAC[key] = d
	return !existed, nil
}
func (r *fakeDeviceRepo) ListByRouter(context.Context, string) ([]domain.Device, error) {
	return nil, nil
}
func (r *fakeDeviceRepo) FindByMAC(_ context.Context, routerID string, mac domain.MAC) (domain.Device, bool, error) {
	d, ok := r.byMAC[routerID+"|"+mac.String()]
	return d, ok, nil
}
func (r *fakeDeviceRepo) SetCustomName(_ context.Context, routerID string, mac domain.MAC, name string) error {
	key := routerID + "|" + mac.String()
	d, ok := r.byMAC[key]
	if !ok {
		return domain.ErrDeviceNotFound
	}
	d.CustomName = name
	r.byMAC[key] = d
	return nil
}

type fakeVendors struct{}

func (fakeVendors) Vendor(domain.MAC) string { return "" }

func f660Meta() domain.AdapterMeta {
	return domain.AdapterMeta{ID: "zte_f660", Vendor: "ZTE", Models: []string{"F660"}, Verified: false}
}

// --- tests -----------------------------------------------------------------

func TestDiscoverRouter(t *testing.T) {
	info := domain.RouterInfo{BaseURL: "http://192.168.1.1", Vendor: "ZTE", Model: "F660", AdapterID: "zte_f660", Reachable: true}
	uc := usecase.NewDiscoverRouter(fakeDisco{info: info})
	got, err := uc.Execute(context.Background(), usecase.DiscoverInput{BaseURL: info.BaseURL})
	if err != nil || got.AdapterID != "zte_f660" {
		t.Fatalf("Execute = (%+v, %v)", got, err)
	}
}

func TestAuthenticate_PersistAndTestMode(t *testing.T) {
	gw := &fakeGateway{meta: f660Meta()}
	factory := fakeFactory{gw: gw}
	vault := newVault()
	routers := newRouterRepo()
	clock := fakeClock{t: time.Unix(1000, 0)}
	disco := fakeDisco{info: domain.RouterInfo{AdapterID: "zte_f660", Vendor: "ZTE"}}
	uc := usecase.NewAuthenticate(factory, vault, routers, disco, clock, nopLogger{})

	// --test mode: must NOT persist.
	if _, err := uc.Execute(context.Background(), usecase.AuthenticateInput{
		BaseURL: "http://192.168.1.1", Creds: domain.Credentials{Username: "admin", Password: "x"}, Persist: false,
	}); err != nil {
		t.Fatalf("test-mode login: %v", err)
	}
	if len(vault.stored) != 0 || len(routers.saved) != 0 {
		t.Fatal("test mode must not persist credentials or router")
	}

	// persist mode: stores creds + router.
	res, err := uc.Execute(context.Background(), usecase.AuthenticateInput{
		BaseURL: "http://192.168.1.1", Creds: domain.Credentials{Username: "admin", Password: "x"}, Persist: true,
	})
	if err != nil {
		t.Fatalf("persist login: %v", err)
	}
	if !res.Stored || res.AdapterID != "zte_f660" {
		t.Fatalf("result = %+v", res)
	}
	if _, ok := vault.stored[res.RouterID]; !ok {
		t.Error("credentials not stored")
	}
	if _, ok := routers.saved[res.RouterID]; !ok {
		t.Error("router not recorded")
	}
}

func TestAuthenticate_LoginError(t *testing.T) {
	gw := &fakeGateway{meta: f660Meta(), loginErr: domain.ErrAuthFailed}
	uc := usecase.NewAuthenticate(fakeFactory{gw: gw}, newVault(), newRouterRepo(),
		fakeDisco{info: domain.RouterInfo{AdapterID: "zte_f660"}}, fakeClock{}, nopLogger{})
	_, err := uc.Execute(context.Background(), usecase.AuthenticateInput{
		BaseURL: "http://192.168.1.1", Creds: domain.Credentials{Username: "admin", Password: "bad"}, Persist: true,
	})
	if err == nil {
		t.Fatal("expected login error")
	}
}

func TestListDevices_StoredCredsAndNewFlag(t *testing.T) {
	gw := &fakeGateway{meta: f660Meta(), devices: []domain.Device{
		{MAC: domain.MustMAC("aa:bb:cc:00:00:11"), Hostname: "phone", Connected: true},
		{MAC: domain.MustMAC("aa:bb:cc:00:00:22"), Hostname: "pc", Connected: true},
	}}
	vault := newVault()
	rid := usecase.RouterID("http://192.168.1.1")
	_ = vault.Store(context.Background(), rid, domain.Credentials{Username: "admin", Password: "x"})
	devRepo := newDeviceRepo()
	uc := usecase.NewListDevices(fakeFactory{gw: gw}, vault, devRepo, fakeVendors{},
		fakeDisco{info: domain.RouterInfo{AdapterID: "zte_f660"}}, fakeClock{t: time.Unix(2000, 0)}, nopLogger{})

	in := usecase.ListDevicesInput{BaseURL: "http://192.168.1.1", Persist: true}
	views, err := uc.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("want 2 devices, got %d", len(views))
	}
	for _, v := range views {
		if !v.NewlySeen {
			t.Errorf("device %s should be NewlySeen on first run", v.MAC)
		}
		if v.ID == "" || v.RouterID != rid {
			t.Errorf("device identity not filled: %+v", v.Device)
		}
	}

	// Second run: same MACs are no longer new.
	views2, err := uc.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	for _, v := range views2 {
		if v.NewlySeen {
			t.Errorf("device %s should not be new on second run", v.MAC)
		}
	}
}

func TestListDevices_NoCredentials(t *testing.T) {
	gw := &fakeGateway{meta: f660Meta()}
	uc := usecase.NewListDevices(fakeFactory{gw: gw}, newVault(), newDeviceRepo(), fakeVendors{},
		fakeDisco{info: domain.RouterInfo{AdapterID: "zte_f660"}}, fakeClock{}, nopLogger{})
	_, err := uc.Execute(context.Background(), usecase.ListDevicesInput{BaseURL: "http://192.168.1.1", Persist: true})
	if err == nil {
		t.Fatal("expected error when no credentials stored")
	}
}

func TestInspectRouter(t *testing.T) {
	gw := &fakeGateway{
		meta:    f660Meta(),
		devices: []domain.Device{{MAC: domain.MustMAC("aa:bb:cc:00:00:11")}},
		rules:   []domain.AccessRule{{MAC: domain.MustMAC("aa:bb:cc:00:00:99"), Mode: domain.AccessBlock}},
	}
	vault := newVault()
	_ = vault.Store(context.Background(), usecase.RouterID("http://192.168.1.1"), domain.Credentials{Username: "admin", Password: "x"})
	uc := usecase.NewInspectRouter(fakeFactory{gw: gw},
		fakeDisco{info: domain.RouterInfo{AdapterID: "zte_f660", Model: "F660", Reachable: true}}, vault, fakeClock{}, nopLogger{})

	rep, err := uc.Execute(context.Background(), usecase.InspectInput{BaseURL: "http://192.168.1.1"})
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if !rep.AuthOK || rep.DeviceCount != 1 || rep.AccessRuleCount != 1 {
		t.Fatalf("report = %+v", rep)
	}
	// Unverified adapter must raise a warning.
	found := false
	for _, w := range rep.Warnings {
		if len(w) > 0 && (contains(w, "UNVERIFIED")) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an UNVERIFIED warning, got %v", rep.Warnings)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
