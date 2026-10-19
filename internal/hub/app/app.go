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
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/config"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
)

// App is a running hub minus its HTTP listener.
type App struct {
	pool    *pgxpool.Pool
	bus     *events.Bus
	health  *health.Checker
	handler http.Handler
	log     *slog.Logger
	relay   *relay.Module
}

// Option tunes an App. The only one so far is the clock, which the
// integration tests move to expire a pairing code or a lease without
// waiting for wall-clock time.
type Option func(*options)

type options struct {
	now        func() time.Time
	callerWait time.Duration
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
	relayMod := relay.New(relay.Config{
		Pool:        pool,
		Guard:       guard,
		Trust:       agentsMod.Trust,
		Agents:      agentsMod.Agents(),
		Tokens:      agentsMod.TokenResolver(),
		IsOwner:     ownerLookup(memberRepo),
		AuditLogger: auditMod.EventLogger(),
		Logger:      log,
		Now:         o.now,
		CallerWait:  o.callerWait,
	})

	hc := health.New(health.Config{Version: version, DBPinger: pool})
	h := routes(routeDeps{
		cfg:    cfg,
		log:    log,
		health: hc,
		public: []registrar{authMod},
		member: []registrar{tenantMod, memberMod, auditMod, agentsMod, relayMod},
		relay:  []relayRegistrar{agentsMod, relayMod},
	})
	hc.MarkReady()
	return &App{pool: pool, bus: bus, health: hc, handler: h, log: log, relay: relayMod}, nil
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

// Run drives the background work until ctx ends: the kernel's outbox
// dispatcher, and the relay module's sweep of commands nobody picked up.
func (a *App) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sweep := make(chan struct{})
	go func() {
		defer close(sweep)
		if err := a.relay.Run(ctx); err != nil {
			a.log.Error("hub: relay sweep", "error", err)
		}
	}()

	err := a.bus.Run(ctx)
	cancel()
	<-sweep // let the sweep finish before the caller closes the pool
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("hub: events dispatcher: %w", err)
	}
	return nil
}

// Close releases the database pool. Call it after Run has returned.
func (a *App) Close() { a.pool.Close() }
