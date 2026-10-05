// Package tests holds cross-layer integration tests and the architecture tests.
// They wire the real adapters against the in-process mock router, exercising the
// whole stack the way the composition root does.
package tests

import (
	"context"
	"io"
	"net/http/httptest"
	"testing"

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

func TestEndToEnd(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(mockrouter.New("admin", "admin").Handler())
	defer srv.Close()

	factory := catalog.New()
	clk := clock.Real{}
	disco := discovery.New(factory, clk)
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	vlt := vault.New(sqlite.NewCredentialStore(db), "test-master-key")
	routers := sqlite.NewRouterRepo(db)
	devices := sqlite.NewDeviceRepo(db)
	vendors := vendor.New(nil)
	logger := logging.New(io.Discard, false)

	// 1. Discovery auto-detects the F660 from the login-page title.
	info, err := usecase.NewDiscoverRouter(disco).Execute(ctx, usecase.DiscoverInput{BaseURL: srv.URL})
	if err != nil || info.AdapterID != "zte_f660" {
		t.Fatalf("discover = (%+v, %v)", info, err)
	}

	// 2. Login persists encrypted credentials + records the router.
	auth := usecase.NewAuthenticate(factory, vlt, routers, disco, clk, logger)
	res, err := auth.Execute(ctx, usecase.AuthenticateInput{
		BaseURL: srv.URL,
		Creds:   domain.Credentials{Username: "admin", Password: "admin"},
		Persist: true,
	})
	if err != nil || !res.Stored {
		t.Fatalf("auth = (%+v, %v)", res, err)
	}
	if _, ok, _ := vlt.Load(ctx, usecase.RouterID(srv.URL)); !ok {
		t.Fatal("credentials not stored")
	}

	// 3. ListDevices uses the STORED credentials (no password passed) and flags new.
	list := usecase.NewListDevices(factory, vlt, devices, vendors, disco, clk, logger)
	in := usecase.ListDevicesInput{BaseURL: srv.URL, Persist: true}
	views, err := list.Execute(ctx, in)
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(views) != 4 {
		t.Fatalf("want 4 devices, got %d", len(views))
	}
	for _, v := range views {
		if !v.NewlySeen {
			t.Errorf("%s should be new on first run", v.MAC)
		}
	}
	views2, err := list.Execute(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views2 {
		if v.NewlySeen {
			t.Errorf("%s should not be new on second run", v.MAC)
		}
	}

	// 4. Inspect reports authenticated counts.
	rep, err := usecase.NewInspectRouter(factory, disco, vlt, clk, logger).Execute(ctx, usecase.InspectInput{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if !rep.AuthOK || rep.DeviceCount != 4 || rep.AccessRuleCount != 2 {
		t.Fatalf("inspect report = %+v", rep)
	}
}
