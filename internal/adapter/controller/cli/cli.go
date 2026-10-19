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
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/captive"
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
	Router    string
	Adapter   string
	Username  string
	Password  string
	DBPath    string
	MasterKey string
	Insecure  bool
	// RouterPin is the box's certificate fingerprint. A self-signed box
	// cannot be verified by a chain, so the pin is what identifies it.
	RouterPin       string
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
	Meter    *usecase.Metering
	// Observe is the raw per-station view a hub relay snapshots (RFC-005 H2).
	Observe  *usecase.ObserveHosts
	Billing  *usecase.Billing
	Members  *usecase.Members
	Enforce  *usecase.Enforcement
	Captive  *usecase.Captive
	Identity *usecase.Identity
	Factory  port.RouterFactory
	Logger   port.Logger
	Logs     port.LogReader
	// Vault holds the box credentials and, for a relay, its hub token and
	// private identity (RFC-005 H1).
	Vault SecretVault
}

// SecretVault is the credential vault plus the two calls the relay needs to
// keep its own secrets encrypted at rest.
type SecretVault interface {
	port.CredentialVault
	StoreSecret(ctx context.Context, id string, secret []byte) error
	LoadSecret(ctx context.Context, id string) ([]byte, bool, error)
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
	case "hub":
		return a.cmdHub(ctx, args[1:])
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
  serve      Start the web dashboard + REST API (--hub also relays for a household)
  hub        Join a household on a Kergui hub (RFC-005): enroll, status
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
  --router-pin SHA256  Pin the router's certificate (hex SHA-256); use with --router-insecure
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
	fs.StringVar(&cfg.RouterPin, "router-pin", "", "pin the router certificate (hex SHA-256)")
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
		PinSHA256:       cfg.RouterPin,
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
	var addr, hubURL, agentToken, relayIP string
	var meterEvery time.Duration
	var cap captiveFlags
	cfg, ok := a.parse("serve", args, func(fs *flag.FlagSet) {
		fs.StringVar(&addr, "addr", "127.0.0.1:8080", "listen address for the REST API")
		fs.StringVar(&hubURL, "hub", "", "also relay for a household on this hub (see `kergui hub enroll`)")
		fs.StringVar(&agentToken, "agent-token", "", "relay token; prefer "+EnvAgentToken+", a flag is visible in ps")
		fs.StringVar(&relayIP, "relay-ip", "", "this machine's LAN address, so the relay never cuts itself")
		fs.DurationVar(&meterEvery, "meter-interval", 5*time.Minute, "traffic sampling interval (0 disables metering)")
		fs.BoolVar(&cap.enabled, "captive", false, "run the captive DNS (:53) and portal (:80) on the LAN address")
		fs.StringVar(&cap.lanIP, "lan-ip", "", "this machine's LAN address for the captive portal (default: auto-detect)")
		fs.StringVar(&cap.upstream, "dns-upstream", "", "DNS server queries are forwarded to (default: the router, port 53)")
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
	captiveKick := make(chan struct{}, 1)
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
		Meter:    svc.Meter,
		Observe:  svc.Observe,
		Billing:  svc.Billing,
		Members:  svc.Members,
		Enforce:  svc.Enforce,
		Identity: svc.Identity,
		Logger:   svc.Logger,
		Logs:     svc.Logs,
		OnChange: func() {
			select {
			case captiveKick <- struct{}{}:
			default: // a refresh is already pending
			}
		},
	}, httpapi.Config{
		BaseURL:   cfg.Router,
		AdapterID: cfg.Adapter,
		Opts:      opts,
		Version:   a.Version,
		Env:       serverEnv(*cfg),

		WebhookSecret: os.Getenv(config.EnvNabooPayWebhookSecret),
	})
	handler := api.Handler()
	if hubURL != "" || agentToken != "" || os.Getenv(EnvAgentToken) != "" {
		if agentToken == "" {
			agentToken = os.Getenv(EnvAgentToken)
		}
		if err := a.startRelay(ctx, svc, handler, *cfg, hubURL, agentToken, relayIP); err != nil {
			return a.fail(fmt.Errorf("relay: %w", err))
		}
		fmt.Fprintf(a.Err, "relaying for a household on %s\n", hubURL)
	}
	httpSrv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	fmt.Fprintf(a.Err, "kergui REST API listening on http://%s (router %s, logs %s)\n", addr, cfg.Router, cfg.LogFile)
	if svc.Logger != nil {
		svc.Logger.Infof("kergui %s started: listening on http://%s, router %s, adapter %q, log file %s",
			a.Version, addr, cfg.Router, cfg.Adapter, cfg.LogFile)
	}
	if svc.Meter != nil && meterEvery > 0 {
		go a.runMeter(ctx, svc, usecase.MACFilterInput{BaseURL: cfg.Router, AdapterID: cfg.Adapter, Opts: opts},
			usecase.ListDevicesInput{BaseURL: cfg.Router, AdapterID: cfg.Adapter, Opts: opts, Persist: true}, meterEvery)
	}
	if svc.Billing != nil {
		target := usecase.MACFilterInput{BaseURL: cfg.Router, AdapterID: cfg.Adapter, Opts: opts}
		if svc.Billing.PaymentsEnabled() {
			svc.Logger.Infof("payments: NabooPay enabled")
		} else {
			svc.Logger.Warnf("payments: NabooPay disabled (set %s to enable)", config.EnvNabooPayAPIKey)
		}
		go a.runBillingJobs(ctx, svc, target, captiveKick)
	}
	if cap.enabled {
		a.startCaptive(ctx, svc, cfg.Router, usecase.ListDevicesInput{
			BaseURL: cfg.Router, AdapterID: cfg.Adapter, Opts: opts, Persist: true,
		}, cap, captiveKick)
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(sctx)
	}()
	if err := httpSrv.ListenAndServe(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			if svc.Logger != nil {
				svc.Logger.Infof("kergui stopped")
			}
			return 0
		}
		if svc.Logger != nil {
			svc.Logger.Errorf("server stopped: %v", err)
		}
		return a.fail(err)
	}
	return 0
}

// runMeter refreshes the device inventory and samples traffic counters every
// interval until ctx ends. Refreshing the inventory here keeps the history page
// complete even when nobody opens the devices tab: a newly connected phone is
// recorded within one interval.
func (a *App) runMeter(ctx context.Context, svc *Services, in usecase.MACFilterInput, list usecase.ListDevicesInput, every time.Duration) {
	sample := func() {
		sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if _, err := svc.List.Execute(sctx, list); err != nil {
			svc.Logger.Warnf("metering: inventory refresh failed: %v", err)
		} else if svc.Identity != nil {
			if _, err := svc.Identity.Scan(sctx, in); err != nil {
				svc.Logger.Warnf("identity: scan failed: %v", err)
			}
		}
		res, err := svc.Meter.Sample(sctx, in)
		switch {
		case err != nil:
			svc.Logger.Warnf("metering: sample failed: %v", err)
		case res.Baseline:
			svc.Logger.Infof("metering: baseline recorded for %d device(s); consumption is counted from now on", res.Devices)
		default:
			svc.Logger.Infof("metering: %d device(s), +%.1f MB this round", res.Devices, float64(res.Bytes)/1e6)
		}
	}
	sample()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sample()
		}
	}
}

type captiveFlags struct {
	enabled         bool
	lanIP, upstream string
}

// startCaptive runs the captive DNS and portal, and keeps their view of who is
// cut fresh by re-reading the router every 90 s. Binding ports 53 and 80 needs
// root or `setcap cap_net_bind_service=+ep` on the binary.
func (a *App) startCaptive(ctx context.Context, svc *Services, routerURL string, list usecase.ListDevicesInput, f captiveFlags, kick <-chan struct{}) {
	if svc.Captive == nil {
		return
	}
	u, err := url.Parse(routerURL)
	if err != nil || u.Hostname() == "" {
		svc.Logger.Errorf("captive: cannot read router host from %q", routerURL)
		return
	}
	var lan netip.Addr
	if f.lanIP != "" {
		lan, err = netip.ParseAddr(f.lanIP)
	} else {
		lan, err = captive.LANAddr(u.Hostname())
	}
	if err != nil || !lan.Is4() {
		svc.Logger.Errorf("captive: no usable LAN IPv4 address (%v); pass --lan-ip", err)
		return
	}
	upstreams := []string{net.JoinHostPort(u.Hostname(), "53"), "1.1.1.1:53", "8.8.8.8:53"}
	if f.upstream != "" {
		upstreams = append([]string{f.upstream}, upstreams...)
	}
	portalURL := "http://" + lan.String() + "/portail"

	refresh := func() {
		rctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		if _, err := svc.List.Execute(rctx, list); err != nil {
			svc.Logger.Warnf("captive: device refresh failed: %v", err)
		} else if svc.Identity != nil {
			target := usecase.MACFilterInput{BaseURL: list.BaseURL, AdapterID: list.AdapterID, Opts: list.Opts}
			if _, err := svc.Identity.Scan(rctx, target); err != nil {
				svc.Logger.Warnf("identity: scan failed: %v", err)
			}
		}
		if err := svc.Captive.Refresh(rctx, routerURL); err != nil {
			svc.Logger.Warnf("captive: state refresh failed: %v", err)
		}
	}
	refresh()
	go func() {
		t := time.NewTicker(90 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				refresh()
			case <-kick:
				refresh()
			}
		}
	}()

	dns := &captive.DNSServer{Addr: net.JoinHostPort(lan.String(), "53"), PortalIP: lan, Upstreams: upstreams,
		Policy: svc.Captive, Log: svc.Logger}
	go func() {
		if err := dns.ListenAndServe(ctx); err != nil && ctx.Err() == nil {
			svc.Logger.Errorf("captive: DNS on %s failed: %v (run `make captive-caps` or start as root)", dns.Addr, err)
		}
	}()
	allow := append([]string{}, captive.DefaultPaymentHosts...)
	if pu, err := url.Parse(os.Getenv(config.EnvPublicURL)); err == nil && pu.Hostname() != "" {
		allow = append(allow, pu.Hostname()) // NabooPay's "thank you" redirect
	} else if pu, err := url.Parse(config.DefaultPublicURL); err == nil {
		allow = append(allow, pu.Hostname())
	}
	for _, h := range strings.Split(os.Getenv("KERGUI_CAPTIVE_ALLOW"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			allow = append(allow, h)
		}
	}
	garden := &captive.SNIProxy{Addr: net.JoinHostPort(lan.String(), "443"), Allow: allow, Self: lan, Log: svc.Logger}
	go func() {
		if err := garden.ListenAndServe(ctx); err != nil && ctx.Err() == nil {
			svc.Logger.Errorf("captive: payment relay on %s failed: %v (run `make captive-caps` or start as root)", garden.Addr, err)
		}
	}()
	svc.Logger.Infof("captive: cut devices may reach only %s", strings.Join(allow, ", "))
	portal := &http.Server{Addr: net.JoinHostPort(lan.String(), "80"), Handler: httpapi.PortalHandler(svc.Captive, portalURL, svc.Logger),
		ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := portal.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			svc.Logger.Errorf("captive: portal on %s failed: %v (run `make captive-caps` or start as root)", portal.Addr, err)
		}
	}()
	svc.Logger.Infof("captive: DNS %s (upstreams %s), portal %s — set the router's DHCP DNS to %s",
		dns.Addr, strings.Join(upstreams, ", "), portalURL, lan)
}

// serverEnv lists the environment variables kergui reads, with their state.
// Secrets report only whether they are set.
func serverEnv(cfg RuntimeConfig) []httpapi.EnvVar {
	v := func(name, desc, example string, required, secret bool, fallback string) httpapi.EnvVar {
		val := os.Getenv(name)
		e := httpapi.EnvVar{Name: name, Description: desc, Example: example, Required: required, Secret: secret, Set: val != ""}
		if !secret {
			e.Value = val
			if val == "" {
				e.Value = fallback
			}
		}
		return e
	}
	return []httpapi.EnvVar{
		v(config.EnvMasterKey, "Phrase secrète qui chiffre les identifiants du routeur stockés en base. Ne la perdez pas : sans elle, il faut ressaisir le mot de passe du routeur.",
			"une-longue-phrase-secrete", true, true, ""),
		v(config.EnvRouterPassword, "Mot de passe admin du routeur (facultatif si déjà enregistré via Réglages).",
			"mot-de-passe-du-routeur", false, true, ""),
		v("KERGUI_ROUTER_URL", "Adresse du routeur utilisée par make serve.", "https://192.168.1.1", false, false, cfg.Router),
		v("KERGUI_ADAPTER", "Modèle de routeur utilisé par make serve.", "zte_f6600p", false, false, cfg.Adapter),
		v(config.EnvNabooPayAPIKey, "Clé API NabooPay v2 (platform.naboopay.com). Sans elle, aucun lien de paiement n'est créé.",
			"naboo_live_xxxxxxxx", false, true, ""),
		v(config.EnvNabooPayBaseURL, "URL de l'API NabooPay (laisser vide pour la production).", "https://api.naboopay.com", false, false, ""),
		v(config.EnvNabooPayWebhookSecret, "Secret du webhook NabooPay (Paramètres → Intégration). Webhook à déclarer : KERGUI_PUBLIC_URL/paiement/webhook. Sans lui, les paiements sont vus en 30 s au plus.",
			"whsec_xxxxxxxx", false, true, ""),
		v(config.EnvPublicURL, "Adresse publique des pages de retour après paiement.", config.DefaultPublicURL, false, false, config.DefaultPublicURL),
		v("KERGUI_CAPTIVE", "1 = portail captif : les appareils coupés voient la page de paiement à la connexion (make serve).", "1", false, false, ""),
		v("KERGUI_CAPTIVE_ALLOW", "Sites supplémentaires accessibles aux appareils coupés, séparés par des virgules (le Journal liste ceux qui manquent).", "exemple-cdn.com", false, false, ""),
	}
}

// runBillingJobs keeps billing moving without the admin:
//   - every 30 s, ask NabooPay about pending payments (safety net for the webhook
//     POST /paiement/webhook, which checks a paid order within seconds,
//     and what restores a cut device within a minute of paying), open payment
//     links for devices over the monthly ceiling, and run the enforcement;
//   - every hour, close the previous month (idempotent: only new usage is
//     invoiced) and run the enforcement (cuts are simulated unless enabled).
//
// A confirmed payment triggers the enforcement at once, to restore internet.
func (a *App) runBillingJobs(ctx context.Context, svc *Services, target usecase.MACFilterInput, kick chan<- struct{}) {
	enforce := func() {
		if svc.Enforce == nil {
			return
		}
		ectx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		rep, err := svc.Enforce.Run(ectx, target)
		if err != nil {
			svc.Logger.Warnf("enforcement: run failed: %v", err)
			return
		}
		if len(rep.Restored) == 0 && (rep.Simulated || len(rep.Cut) == 0) {
			return // nothing changed on the router
		}
		select {
		case kick <- struct{}{}: // let the captive portal see cut / restored devices now
		default:
		}
	}
	hourly := func() {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		if _, err := svc.Billing.CloseMonth(cctx, target.BaseURL, ""); err != nil {
			svc.Logger.Warnf("billing: monthly close failed: %v", err)
		}
		cancel()
		enforce()
	}
	poll := func() {
		pctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		if n, err := svc.Billing.RefreshPending(pctx); err != nil {
			svc.Logger.Warnf("payments: status check failed: %v", err)
		} else if n > 0 {
			svc.Logger.Infof("payments: %d payment(s) confirmed", n)
		}
		// Ceiling: open links for devices that reached it; the enforcement below
		// then cuts them (and restores whatever was just paid).
		links, err := svc.Billing.EnsureCapLinks(pctx, target.BaseURL, nil)
		if err != nil {
			svc.Logger.Warnf("billing: ceiling check failed: %v", err)
		}
		if len(links) > 0 {
			select {
			case kick <- struct{}{}: // the new links show on the portal right away
			default:
			}
		}
		enforce()
	}
	hourly()
	pollT := time.NewTicker(30 * time.Second)
	hourT := time.NewTicker(time.Hour)
	defer pollT.Stop()
	defer hourT.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-pollT.C:
			poll()
		case <-hourT.C:
			hourly()
		}
	}
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
