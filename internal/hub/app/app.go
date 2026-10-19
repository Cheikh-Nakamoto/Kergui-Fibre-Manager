// Package app is the hub's composition root: it opens Postgres, applies the
// migrations, builds the kernel modules (auth, tenant, membership, audit,
// notification, events) and exposes one http.Handler. cmd/kergui-hub only
// reads the environment and runs the server; tests build an App directly.
//
// This is the only place, with internal/hub/..., allowed to import the
// kernel and pgx (enforced by tests/architecture_test.go).
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/agile-factory/kernel/kernel/events"
	"github.com/agile-factory/kernel/kernel/health"
	"github.com/agile-factory/kernel/kernel/migrator"
	"github.com/agile-factory/kernel/platform/audit"
	auditinfra "github.com/agile-factory/kernel/platform/audit/infrastructure"
	"github.com/agile-factory/kernel/platform/auth"
	authinfra "github.com/agile-factory/kernel/platform/auth/infrastructure"
	"github.com/agile-factory/kernel/platform/membership"
	memberdomain "github.com/agile-factory/kernel/platform/membership/domain"
	memberinfra "github.com/agile-factory/kernel/platform/membership/infrastructure"
	"github.com/agile-factory/kernel/platform/notification"
	notifinfra "github.com/agile-factory/kernel/platform/notification/infrastructure"
	"github.com/agile-factory/kernel/platform/tenant"
	tenantinfra "github.com/agile-factory/kernel/platform/tenant/infrastructure"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/adapters/console"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/adapters/evolution"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/config"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/inventory"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay"
	relaydomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
)

// App is a running hub minus its HTTP listener.
type App struct {
	pool      *pgxpool.Pool
	bus       *events.Bus
	health    *health.Checker
	handler   http.Handler
	log       *slog.Logger
	relay     *relay.Module
	inventory *inventory.Module
	alerts    *alerts.Module
	notifier  alertsdomain.Notifier
}

// Option tunes an App. The only one so far is the clock, which the
// integration tests move to expire a pairing code or a lease without
// waiting for wall-clock time.
type Option func(*options)

type options struct {
	now         func() time.Time
	callerWait  time.Duration
	leases      relaydomain.Timings
	alertWindow time.Duration
}

// WithClock replaces the module clock.
func WithClock(now func() time.Time) Option {
	return func(o *options) { o.now = now }
}

// WithCallerWait shortens how long an app request waits for a relay's
// answer before it is handed the command id instead.
func WithCallerWait(d time.Duration) Option {
	return func(o *options) { o.callerWait = d }
}

// WithLeaseTimings shortens the relay lease, so a failover can be tested in
// seconds. Keep the ratios of RFC-005: a lease of three heartbeats.
func WithLeaseTimings(t relaydomain.Timings) Option {
	return func(o *options) { o.leases = t }
}

// WithAlertWindow shortens the window that groups new devices into one
// alert, so a test does not wait two minutes for it to close.
func WithAlertWindow(d time.Duration) Option {
	return func(o *options) { o.alertWindow = d }
}

// New connects to the database, migrates it and wires every module.
func New(ctx context.Context, cfg config.Config, version string, log *slog.Logger, opts ...Option) (*App, error) {
	o := options{now: time.Now}
	for _, apply := range opts {
		apply(&o)
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("hub: database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("hub: database ping: %w", err)
	}
	a, err := build(cfg, version, pool, log, o)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return a, nil
}

func build(cfg config.Config, version string, pool *pgxpool.Pool, log *slog.Logger, o options) (*App, error) {
	// The outbox tables come first: every module publishes into them. The
	// rest follows the kernel's dependency order (tenant → auth → membership
	// → audit → notification).
	if err := migrator.Run(cfg.DatabaseURL, []migrator.Source{
		{Name: "events", FS: events.Migrations, Path: "migrations"},
		{Name: "tenant", FS: tenant.Migrations, Path: "migrations"},
		{Name: "auth", FS: auth.Migrations, Path: "migrations"},
		{Name: "membership", FS: membership.Migrations, Path: "migrations"},
		{Name: "audit", FS: audit.Migrations, Path: "migrations"},
		{Name: "notification", FS: notification.Migrations, Path: "migrations"},
		{Name: "hub_agents", FS: agents.Migrations, Path: "migrations"},
		{Name: "hub_relay", FS: relay.Migrations, Path: "migrations"},
		{Name: "hub_inventory", FS: inventory.Migrations, Path: "migrations"},
		{Name: "hub_alerts", FS: alerts.Migrations, Path: "migrations"},
	}); err != nil {
		return nil, fmt.Errorf("hub: migrations: %w", err)
	}

	bus, err := events.New(events.Config{Pool: pool, Logger: log})
	if err != nil {
		return nil, fmt.Errorf("hub: events bus: %w", err)
	}

	tenantRepo := tenantinfra.NewPostgresTenantRepository(pool)
	settingsRepo := tenantinfra.NewPostgresTenantSettingsRepository(pool)
	memberRepo := memberinfra.NewPostgresMembershipRepository(pool)
	inviteRepo := memberinfra.NewPostgresInviteTokenRepository(pool)
	userRepo := authinfra.NewPostgresUserRepository(pool)

	auditMod, err := audit.New(audit.Config{
		DB:             pool,
		Repo:           auditinfra.NewPostgresAuditRepository(pool),
		MembershipRepo: memberRepo,
		Publisher:      bus.Publisher(),
		Logger:         log,
	})
	if err != nil {
		return nil, fmt.Errorf("hub: audit: %w", err)
	}
	tenantMod := tenant.New(tenant.Config{
		Pool:           pool,
		Repo:           tenantRepo,
		SettingsRepo:   settingsRepo,
		MembershipRepo: memberRepo,
		AuditLogger:    auditMod.EventLogger(),
		Logger:         log,
	})
	memberMod := membership.New(membership.Config{
		Pool:            pool,
		Repo:            memberRepo,
		InviteTokenRepo: inviteRepo,
		TenantRepo:      tenantRepo,
		UserLookup:      userRepo,
		AuditLogger:     auditMod.EventLogger(),
		Publisher:       bus.Publisher(),
		Logger:          log,
	})
	authMod := auth.New(auth.Config{
		Pool:               pool,
		UserRepo:           userRepo,
		TenantRepo:         tenantRepo,
		TenantSettingsRepo: settingsRepo,
		MembershipRepo:     memberRepo,
		InviteTokenRepo:    inviteRepo,
		RefreshTokenRepo:   authinfra.NewPostgresRefreshTokenRepository(pool),
		EmailVerifRepo:     authinfra.NewPostgresEmailVerificationTokenRepository(pool),
		MembershipListUC:   memberMod.ListUC,
		AuditLogger:        auditMod.EventLogger(),
		Publisher:          bus.Publisher(),
		Logger:             log,
		JWTSecret:          cfg.JWTSecret,
		TokenLifespan:      cfg.TokenTTL,
		RefreshTokenTTL:    cfg.RefreshTTL,
	})
	notifMod, err := notification.New(notification.Config{
		LogRepo:       notifinfra.NewPostgresNotificationLogRepository(pool),
		EmailSender:   emailSender(cfg.SMTP, log),
		Logger:        log,
		From:          cfg.MailFrom,
		BaseURL:       cfg.PublicURL,
		DefaultLocale: "fr",
	})
	if err != nil {
		return nil, fmt.Errorf("hub: notification: %w", err)
	}
	// Handlers must be registered before the dispatcher starts (Run).
	auditMod.Subscribe(bus)
	notifMod.Subscribe(bus)

	// The hub's own modules (RFC-005 H1). agents owns the relays and the
	// household's trust material; relay moves commands to whichever relay
	// holds the box. relay reads the first through ports, never its tables.
	guard := tenantaccess.New(memberRepo)
	agentsMod := agents.New(agents.Config{
		Pool: pool, Members: memberRepo, AuditLogger: auditMod.EventLogger(), Now: o.now,
	})
	inventoryMod := inventory.New(inventory.Config{
		Pool: pool, Guard: guard, Publisher: bus.Publisher(),
		Logger: log, Now: o.now, Window: o.alertWindow,
	})
	relayMod := relay.New(relay.Config{
		Pool:        pool,
		Guard:       guard,
		Trust:       agentsMod.Trust,
		Agents:      agentsMod.Agents(),
		Tokens:      agentsMod.TokenResolver(),
		IsOwner:     ownerLookup(memberRepo),
		AuditLogger: auditMod.EventLogger(),
		Snapshots:   inventoryMod.Snapshots(),
		Logger:      log,
		Now:         o.now,
		CallerWait:  o.callerWait,
		Timings:     o.leases,
	})

	// The alerts module turns a closed inventory window into a WhatsApp
	// message. Its notifier is chosen by configuration, and a hub with none
	// configured still alerts — into its own log — rather than silently
	// dropping what a household asked to be told.
	notifier, err := notifier(cfg.WhatsApp, log)
	if err != nil {
		return nil, err
	}
	number, err := alertsNumber(cfg.WhatsApp.Number)
	if err != nil {
		return nil, err
	}
	alertsMod := alerts.New(alerts.Config{
		Pool: pool, Guard: guard, Notifier: notifier,
		Pusher: &console.Pusher{Log: log}, Targets: noPushTargets{},
		AuditLogger: auditMod.EventLogger(), Number: number,
		WebhookSecret: cfg.WhatsApp.WebhookSecret, Logger: log, Now: o.now,
	})
	alertsMod.Subscribe(bus)

	hc := health.New(health.Config{Version: version, DBPinger: pool})
	h := routes(routeDeps{
		cfg:    cfg,
		log:    log,
		health: hc,
		public: []registrar{authMod},
		member: []registrar{tenantMod, memberMod, auditMod, agentsMod, relayMod, inventoryMod, alertsMod},
		relay:  []relayRegistrar{agentsMod, relayMod},
		hooks:  []webhookRegistrar{alertsMod},
	})
	hc.MarkReady()
	return &App{
		pool: pool, bus: bus, health: hc, handler: h, log: log,
		relay: relayMod, inventory: inventoryMod, alerts: alertsMod, notifier: notifier,
	}, nil
}

// ownerLookup answers "does this user own the household?" for the relay's
// lease priority. It is a function so the relay module never touches the
// kernel's membership tables itself.
func ownerLookup(members memberdomain.MembershipRepository) func(context.Context, string, string) (bool, error) {
	return func(ctx context.Context, tenantID, userID string) (bool, error) {
		m, err := members.FindByUserAndTenant(ctx, userID, tenantID)
		if err != nil {
			if errors.Is(err, memberdomain.ErrMembershipNotFound) {
				return false, nil
			}
			return false, err
		}
		return m.IsOwner() && m.IsActive(), nil
	}
}

// notifier builds the messaging provider named by the configuration.
func notifier(cfg config.WhatsApp, log *slog.Logger) (alertsdomain.Notifier, error) {
	switch cfg.Provider {
	case config.NotifierEvolution:
		n, err := evolution.New(evolution.Config{
			BaseURL: cfg.Evolution.BaseURL, Instance: cfg.Evolution.Instance, APIKey: cfg.Evolution.APIKey,
		})
		if err != nil {
			return nil, fmt.Errorf("hub: whatsapp: %w", err)
		}
		// The licence of Evolution Go requires this notice to reach the
		// hub's administrators; the app's settings screen reads it from
		// the WhatsApp route, and the log says it once at start-up.
		log.Info("hub: whatsapp provider configured", "provider", n.Name(), "notice", n.Attribution())
		return n, nil
	default:
		// The console provider: messages go to the hub's log, so a pilot
		// without WhatsApp can still see what a household would receive.
		return &console.Notifier{Secret: []byte(cfg.WebhookSecret), Log: log}, nil
	}
}

// alertsNumber parses the household-facing WhatsApp number. An empty one is
// not an error: a hub can run without WhatsApp, and the opt-in route then
// says so instead of handing out a broken link.
func alertsNumber(raw string) (alertsdomain.Phone, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	n, err := alertsdomain.ParsePhone(raw)
	if err != nil {
		return "", fmt.Errorf("hub: KERGUI_HUB_WHATSAPP_NUMBER: %w", err)
	}
	return n, nil
}

// noPushTargets is the push side before H3: the hub holds no device tokens
// yet, so the alert path walks through the port and finds nobody.
type noPushTargets struct{}

func (noPushTargets) For(context.Context, string, string) ([]alertsdomain.PushTarget, error) {
	return nil, nil
}

// emailSender sends through SMTP when configured, else logs the rendered
// email (development, CI, and the pilot before a mail provider is chosen).
func emailSender(s config.SMTP, log *slog.Logger) notification.EmailSender {
	if s.Host == "" {
		return notification.NewConsoleSender(log)
	}
	return notification.NewSMTPSender(notification.SMTPConfig{
		Host: s.Host, Port: s.Port, Username: s.Username, Password: s.Password, UseTLS: s.TLS,
	})
}

// Handler serves the whole hub API.
func (a *App) Handler() http.Handler { return a.handler }

// Notifier is the messaging provider this hub was wired with. The
// integration tests read what the console provider "sent"; a deployment
// reads its name and its licence notice through the WhatsApp route.
func (a *App) Notifier() alertsdomain.Notifier { return a.notifier }

// Run drives the background work until ctx ends: the kernel's outbox
// dispatcher, the relay module's sweep of commands nobody picked up, and the
// inventory's announcement of the alert windows that have closed.
func (a *App) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sweeps := make(chan struct{})
	go func() {
		defer close(sweeps)
		var wg sync.WaitGroup
		for name, run := range map[string]func(context.Context) error{
			"relay":     a.relay.Run,
			"inventory": a.inventory.Run,
		} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := run(ctx); err != nil {
					a.log.Error("hub: background sweep", "module", name, "error", err)
				}
			}()
		}
		wg.Wait()
	}()

	err := a.bus.Run(ctx)
	cancel()
	<-sweeps // let the sweeps finish before the caller closes the pool
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("hub: events dispatcher: %w", err)
	}
	return nil
}

// Close releases the database pool. Call it after Run has returned.
func (a *App) Close() { a.pool.Close() }
