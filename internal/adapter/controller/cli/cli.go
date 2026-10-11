// Package cli is the command-line controller: the delivery adapter. It parses
// flags, calls the use-case interactors, and hands results to a presenter. It
// receives its data-layer services from the composition root via a Builder, so it
// never opens a database or constructs a gateway itself.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/controller/httpapi"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/vault"
	present "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/presenter/cli"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/config"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"

	"golang.org/x/term"
)

// RuntimeConfig is the resolved per-invocation configuration.
type RuntimeConfig struct {
	Router          string
	Adapter         string
	Username        string
	Password        string
	DBPath          string
	MasterKey       string
	Insecure        bool
	JSON            bool
	Verbose         bool
	LogFile         string
	Timeout         time.Duration
	SkipWriteVerify bool
}

// Services bundles the wired interactors the controller drives.
type Services struct {
	Discover *usecase.DiscoverRouter
	Auth     *usecase.Authenticate
	List     *usecase.ListDevices
	Inspect  *usecase.InspectRouter
	Block    *usecase.BlockDevice
	Unblock  *usecase.UnblockDevice
	Rename   *usecase.RenameDevice
	Filter   *usecase.MACFilter
	WiFi     *usecase.WiFiAccess
	Factory  port.RouterFactory
	Logger   port.Logger
	Logs     port.LogReader
}

// Builder constructs the services (opening the DB, building the vault, etc.) from
// the runtime config, returning a close function to release resources.
type Builder func(cfg RuntimeConfig) (svc *Services, closeFn func() error, err error)

// App is the CLI application.
type App struct {
	Build   Builder
	Out     io.Writer
	Err     io.Writer
	Version string
}

// New builds the app with stdout/stderr and the given builder.
func New(build Builder) *App {
	return &App{Build: build, Out: os.Stdout, Err: os.Stderr, Version: "dev"}
}

// Run dispatches a command and returns a process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		a.usage()
		return 2
	}
	switch args[0] {
	case "discover":
		return a.cmdDiscover(ctx, args[1:])
	case "login":
		return a.cmdLogin(ctx, args[1:])
	case "devices":
		return a.cmdDevices(ctx, args[1:])
	case "inspect":
		return a.cmdInspect(ctx, args[1:])
	case "serve":
		return a.cmdServe(ctx, args[1:])
	case "block":
		return a.cmdChangeAccess(ctx, args[1:], true)
	case "unblock":
		return a.cmdChangeAccess(ctx, args[1:], false)
	case "rename":
		return a.cmdRename(ctx, args[1:])
	case "version", "-v", "--version":
		fmt.Fprintln(a.Out, a.Version)
		return 0
	case "help", "-h", "--help":
		a.usage()
		return 0
	default:
		fmt.Fprintf(a.Err, "unknown command %q\n\n", args[0])
		a.usage()
		return 2
	}
}

func (a *App) usage() {
	fmt.Fprint(a.Err, `kergui — local admin overlay for Orange Sénégal routers

Usage:
  kergui <command> [flags]

Commands:
  discover   Non-destructively fingerprint the router (no login)
  login      Test credentials and store them encrypted (--test to only test)
  devices    List connected devices
  inspect    Diagnostic report of the router protocol mapping
  serve      Start the web dashboard + REST API
  block      Block a device by MAC (requires --mac and --yes)
  unblock    Unblock a device by MAC (requires --mac and --yes)
  rename     Set a device's local custom name (--mac and --name)
  version    Print the version

Common flags:
  --router URL         Router base URL (default `+config.DefaultRouter+`)
  --adapter ID         Force an adapter (zte_f660|zte_f680|zte_funbox); default auto-detect
  --username NAME      Router username (default `+config.DefaultUsername+`)
  --password PASS      Router password (prefer `+config.EnvRouterPassword+` env, or the prompt)
  --router-insecure    Skip TLS verification for the router's self-signed cert
  --json               JSON output
  --db PATH            Local database path (default `+config.DefaultDBPath+`)
  --timeout DURATION   Per-request timeout (default `+config.DefaultTimeout.String()+`)
  --verbose            Verbose logging
  --log-file PATH      Also write logs to a file (serve defaults to `+config.DefaultLogFile+`)

Credentials are encrypted with the passphrase in `+config.EnvMasterKey+`.
All endpoints are UNVERIFIED until confirmed on a real router.
`)
}

func (a *App) commonFlags(fs *flag.FlagSet) *RuntimeConfig {
	cfg := &RuntimeConfig{}
	fs.StringVar(&cfg.Router, "router", config.DefaultRouter, "router base URL")
	fs.StringVar(&cfg.Adapter, "adapter", "", "force adapter id; default auto-detect")
	fs.StringVar(&cfg.Username, "username", config.DefaultUsername, "router username")
	fs.StringVar(&cfg.Password, "password", "", "router password")
	fs.BoolVar(&cfg.JSON, "json", false, "JSON output")
	fs.BoolVar(&cfg.Insecure, "router-insecure", false, "skip TLS verification for the router cert")
	fs.StringVar(&cfg.DBPath, "db", config.DefaultDBPath, "local database path")
	fs.BoolVar(&cfg.Verbose, "verbose", false, "verbose logging")
	fs.StringVar(&cfg.LogFile, "log-file", "", "also write logs to a file")
	fs.DurationVar(&cfg.Timeout, "timeout", config.DefaultTimeout, "per-request timeout")
	return cfg
}

// finalize fills values that come from the environment.
func (a *App) finalize(cfg *RuntimeConfig) {
	cfg.MasterKey = os.Getenv(config.EnvMasterKey)
	if cfg.Password == "" {
		cfg.Password = os.Getenv(config.EnvRouterPassword)
	}
}

func (a *App) options(cfg RuntimeConfig) port.RouterOptions {
	return port.RouterOptions{
		InsecureTLS:     cfg.Insecure,
		Timeout:         cfg.Timeout,
		SkipWriteVerify: cfg.SkipWriteVerify,
	}
}

func (a *App) presenter(jsonOut bool) port.Presenter {
	if jsonOut {
		return present.NewJSONPresenter(a.Out)
	}
	return present.NewTablePresenter(a.Out)
}

func (a *App) withServices(cfg RuntimeConfig, fn func(*Services, port.Presenter, port.RouterOptions) error) int {
	svc, closeFn, err := a.Build(cfg)
	if err != nil {
		return a.fail(err)
	}
	if closeFn != nil {
		defer func() { _ = closeFn() }()
	}
	opts := a.options(cfg)
	opts.Logger = svc.Logger
	if err := fn(svc, a.presenter(cfg.JSON), opts); err != nil {
		return a.fail(err)
	}
	return 0
}

func (a *App) parse(name string, args []string, extra func(*flag.FlagSet)) (*RuntimeConfig, bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.Err)
	cfg := a.commonFlags(fs)
	if extra != nil {
		extra(fs)
	}
	if err := fs.Parse(args); err != nil {
		return nil, false
	}
	a.finalize(cfg)
	return cfg, true
}

func (a *App) cmdDiscover(ctx context.Context, args []string) int {
	cfg, ok := a.parse("discover", args, nil)
	if !ok {
		return 2
	}
	return a.withServices(*cfg, func(svc *Services, pres port.Presenter, opts port.RouterOptions) error {
		info, err := svc.Discover.Execute(ctx, usecase.DiscoverInput{BaseURL: cfg.Router, Opts: opts})
		if err != nil {
			return err
		}
		return pres.Discovery(info)
	})
}

func (a *App) cmdLogin(ctx context.Context, args []string) int {
	var test bool
	cfg, ok := a.parse("login", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&test, "test", false, "only test the connection; do not store credentials")
	})
	if !ok {
		return 2
	}
	if cfg.Password == "" {
		p, err := a.readPassword("Router password: ")
		if err != nil {
			return a.fail(err)
		}
		cfg.Password = p
	}
	return a.withServices(*cfg, func(svc *Services, pres port.Presenter, opts port.RouterOptions) error {
		res, err := svc.Auth.Execute(ctx, usecase.AuthenticateInput{
			BaseURL:   cfg.Router,
			AdapterID: cfg.Adapter,
			Creds:     domain.Credentials{Username: cfg.Username, Password: cfg.Password},
			Opts:      opts,
			Persist:   !test,
		})
		if err != nil {
			return err
		}
		if test {
			return pres.Message("Connection OK via adapter " + res.AdapterID)
		}
		return pres.Message("Login OK — credentials stored (encrypted) for router " + res.RouterID)
	})
}

func (a *App) cmdDevices(ctx context.Context, args []string) int {
	cfg, ok := a.parse("devices", args, nil)
	if !ok {
		return 2
	}
	return a.withServices(*cfg, func(svc *Services, pres port.Presenter, opts port.RouterOptions) error {
		views, err := svc.List.Execute(ctx, usecase.ListDevicesInput{
			BaseURL:   cfg.Router,
			AdapterID: cfg.Adapter,
			Opts:      opts,
			Creds:     domain.Credentials{Username: cfg.Username, Password: cfg.Password},
			Persist:   true,
		})
		if err != nil {
			return err
		}
		return pres.Devices(views)
	})
}

func (a *App) cmdInspect(ctx context.Context, args []string) int {
	cfg, ok := a.parse("inspect", args, nil)
	if !ok {
		return 2
	}
	return a.withServices(*cfg, func(svc *Services, pres port.Presenter, opts port.RouterOptions) error {
		rep, err := svc.Inspect.Execute(ctx, usecase.InspectInput{
			BaseURL:   cfg.Router,
			AdapterID: cfg.Adapter,
			Creds:     domain.Credentials{Username: cfg.Username, Password: cfg.Password},
			Opts:      opts,
		})
		if err != nil {
			return err
		}
		return pres.Report(rep)
	})
}

func (a *App) cmdServe(ctx context.Context, args []string) int {
	_ = ctx
	var addr string
	cfg, ok := a.parse("serve", args, func(fs *flag.FlagSet) {
		fs.StringVar(&addr, "addr", "127.0.0.1:8080", "listen address for the REST API")
	})
	if !ok {
		return 2
	}
	if cfg.LogFile == "" {
		cfg.LogFile = config.DefaultLogFile
	}
	svc, closeFn, err := a.Build(*cfg)
	if err != nil {
		return a.fail(err)
	}
	if closeFn != nil {
		defer func() { _ = closeFn() }()
	}
	opts := a.options(*cfg)
	opts.Logger = svc.Logger
	api := httpapi.New(httpapi.Services{
		Discover: svc.Discover,
		List:     svc.List,
		Inspect:  svc.Inspect,
		Block:    svc.Block,
		Unblock:  svc.Unblock,
		Auth:     svc.Auth,
		Rename:   svc.Rename,
		Filter:   svc.Filter,
		WiFi:     svc.WiFi,
		Logger:   svc.Logger,
		Logs:     svc.Logs,
	}, httpapi.Config{
		BaseURL:   cfg.Router,
		AdapterID: cfg.Adapter,
		Opts:      opts,
		Version:   a.Version,
	})
	httpSrv := &http.Server{Addr: addr, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second}
	fmt.Fprintf(a.Err, "kergui REST API listening on http://%s (router %s, logs %s)\n", addr, cfg.Router, cfg.LogFile)
	if svc.Logger != nil {
		svc.Logger.Infof("kergui %s started: listening on http://%s, router %s, adapter %q, log file %s",
			a.Version, addr, cfg.Router, cfg.Adapter, cfg.LogFile)
	}
	if err := httpSrv.ListenAndServe(); err != nil {
		if svc.Logger != nil {
			svc.Logger.Errorf("server stopped: %v", err)
		}
		return a.fail(err)
	}
	return 0
}

// cmdChangeAccess implements `block` and `unblock`. A router write is an
// explicit, outward action, so it is refused without --yes. Until a model's write
// path is verified on real hardware the gateway returns ErrNotImplemented, which
// surfaces here as a clear, non-fatal explanation.
func (a *App) cmdChangeAccess(ctx context.Context, args []string, block bool) int {
	name := "unblock"
	if block {
		name = "block"
	}
	var mac string
	var yes, noVerify bool
	cfg, ok := a.parse(name, args, func(fs *flag.FlagSet) {
		fs.StringVar(&mac, "mac", "", "target device MAC address (required)")
		fs.BoolVar(&yes, "yes", false, "confirm the change (required: writes modify the router)")
		fs.BoolVar(&noVerify, "no-verify-write", false, "skip read-after-write ACL verification")
	})
	if !ok {
		return 2
	}
	cfg.SkipWriteVerify = noVerify
	m, err := domain.ParseMAC(mac)
	if err != nil {
		return a.fail(err)
	}
	if !yes {
		fmt.Fprintf(a.Err, "refusing to %s %s without confirmation — re-run with --yes\n", name, m)
		return 1
	}
	return a.withServices(*cfg, func(svc *Services, pres port.Presenter, opts port.RouterOptions) error {
		in := usecase.ChangeAccessInput{
			BaseURL:   cfg.Router,
			AdapterID: cfg.Adapter,
			MAC:       m,
			Opts:      opts,
			Creds:     domain.Credentials{Username: cfg.Username, Password: cfg.Password},
			Confirm:   true,
		}
		if block {
			err = svc.Block.Execute(ctx, in)
		} else {
			err = svc.Unblock.Execute(ctx, in)
		}
		if err != nil {
			return err
		}
		return pres.Message(name + " OK for " + m.String())
	})
}

// cmdRename implements `rename`, setting a device's local custom name. This is a
// local inventory change and never touches the router; the device must already
// have been seen (run `devices` first).
func (a *App) cmdRename(ctx context.Context, args []string) int {
	var mac, newName string
	cfg, ok := a.parse("rename", args, func(fs *flag.FlagSet) {
		fs.StringVar(&mac, "mac", "", "target device MAC address (required)")
		fs.StringVar(&newName, "name", "", "new custom name (required)")
	})
	if !ok {
		return 2
	}
	m, err := domain.ParseMAC(mac)
	if err != nil {
		return a.fail(err)
	}
	return a.withServices(*cfg, func(svc *Services, pres port.Presenter, _ port.RouterOptions) error {
		if err := svc.Rename.Execute(ctx, usecase.RenameInput{BaseURL: cfg.Router, MAC: m, Name: newName}); err != nil {
			return err
		}
		return pres.Message("renamed " + m.String() + " to " + newName)
	})
}

func (a *App) readPassword(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("no password supplied (set --password, %s, or run in a terminal)", config.EnvRouterPassword)
	}
	fmt.Fprint(a.Err, prompt)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(a.Err)
	return string(b), err
}

func (a *App) fail(err error) int {
	fmt.Fprintln(a.Err, "error: "+friendly(err))
	return 1
}

func friendly(err error) string {
	switch {
	case errors.Is(err, domain.ErrRouterUnreachable):
		return "router unreachable — check --router and your LAN connection (" + err.Error() + ")"
	case errors.Is(err, domain.ErrAuthFailed):
		return "authentication failed — check the username/password (" + err.Error() + ")"
	case errors.Is(err, vault.ErrNoMasterKey):
		return "master key not set — export " + config.EnvMasterKey + " to store or read credentials"
	case errors.Is(err, domain.ErrUnsupportedModel):
		return err.Error()
	case errors.Is(err, domain.ErrDeviceNotFound):
		return err.Error() + " — run `kergui devices` first so the device is in the local inventory"
	case errors.Is(err, domain.ErrNotImplemented):
		return err.Error() + " — not available until this router's write path is verified (see docs/reverse-engineering)"
	default:
		return err.Error()
	}
}
