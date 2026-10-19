// Command kergui is the Kergui Fibre Manager CLI: a local, read-only admin
// overlay for Orange Sénégal routers (Milestone 1). This file is the composition
// root (Frameworks & Drivers ring): the one place that imports concrete adapters
// and wires them into the use cases. Everything else depends only inward.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/controller/cli"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/discovery"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/payment/naboopay"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/vault"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/catalog"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/vendor"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/clock"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/config"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/logging"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// version is overridable via -ldflags "-X main.version=...".
var version = "0.2.0-m2"

func main() {
	app := cli.New(build)
	app.Version = version
	// SIGTERM (docker stop, systemd) cancels the context so `serve` shuts down
	// cleanly instead of dying in the middle of a database write.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := app.Run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}

// build is the dependency-injection seam: given the runtime config it opens the
// database and assembles the interactors. It returns a close function.
func build(cfg cli.RuntimeConfig) (*cli.Services, func() error, error) {
	db, err := sqlite.Open(cfg.DBPath)
	if err != nil {
		return nil, nil, err
	}

	// One serialised session per router: the box allows a single admin login.
	factory := router.NewSharedFactory(catalog.New())
	clk := clock.Real{}

	logger := logging.New(os.Stderr, cfg.Verbose)
	var logCloser func() error
	if cfg.LogFile != "" {
		logger, logCloser, err = logging.NewFile(cfg.LogFile, os.Stderr, cfg.Verbose)
		if err != nil {
			_ = db.Close()
			return nil, nil, err
		}
	}

	disco := discovery.New(factory, clk)
	vlt := vault.New(sqlite.NewCredentialStore(db), cfg.MasterKey)
	routers := sqlite.NewRouterRepo(db)
	devices := sqlite.NewDeviceRepo(db)
	usage := sqlite.NewUsageRepo(db)
	members := sqlite.NewMemberRepo(db)
	payments := sqlite.NewPaymentRepo(db)
	settings := sqlite.NewSettingsRepo(db)
	identities := sqlite.NewIdentityRepo(db)

	var payGateway port.PaymentGateway
	if key := os.Getenv(config.EnvNabooPayAPIKey); key != "" {
		payGateway = naboopay.New(key, os.Getenv(config.EnvNabooPayBaseURL))
	}
	publicURL := os.Getenv(config.EnvPublicURL)
	if publicURL == "" {
		publicURL = config.DefaultPublicURL
	}
	vendors := vendor.New(nil)

	svc := &cli.Services{
		Discover: usecase.NewDiscoverRouter(disco),
		Auth:     usecase.NewAuthenticate(factory, vlt, routers, disco, clk, logger),
		List:     usecase.NewListDevices(factory, vlt, devices, vendors, disco, clk, logger),
		Inspect:  usecase.NewInspectRouter(factory, disco, vlt, clk, logger),
		Block:    usecase.NewBlockDevice(factory, vlt, disco, clk, logger),
		Unblock:  usecase.NewUnblockDevice(factory, vlt, disco, clk, logger),
		Rename:   usecase.NewRenameDevice(devices),
		Filter:   usecase.NewMACFilter(factory, vlt, disco, logger),
		WiFi:     usecase.NewWiFiAccess(factory, vlt, disco),
		Meter:    usecase.NewMetering(factory, vlt, disco, usage, clk, logger),
		Billing: usecase.NewBilling(sqlite.NewHistoryRepo(db), usage, payments, settings, members,
			payGateway, clk, logger, publicURL),
		Members:  usecase.NewMembers(members, clk, logger),
		Enforce:  usecase.NewEnforcement(factory, vlt, disco, payments, members, settings, sqlite.NewCutRepo(db), devices, usage, clk, logger),
		Captive:  usecase.NewCaptive(devices, payments, sqlite.NewCutRepo(db), identities, sqlite.NewReminderRepo(db), clk, logger),
		Identity: usecase.NewIdentity(factory, vlt, disco, devices, members, identities, sqlite.NewCutRepo(db), clk, logger),
		Factory:  factory,
		Logger:   logger,
		Logs:     logger,
		Vault:    vlt,
	}
	closeFn := func() error {
		err := db.Close()
		if logCloser != nil {
			_ = logCloser()
		}
		return err
	}
	return svc, closeFn, nil
}
