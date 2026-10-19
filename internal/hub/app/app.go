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

	"github.com/agile-factory/kernel/kernel/events"
	"github.com/agile-factory/kernel/kernel/health"
	"github.com/agile-factory/kernel/kernel/migrator"
	"github.com/agile-factory/kernel/platform/audit"
	auditinfra "github.com/agile-factory/kernel/platform/audit/infrastructure"
	"github.com/agile-factory/kernel/platform/auth"
	authinfra "github.com/agile-factory/kernel/platform/auth/infrastructure"
	"github.com/agile-factory/kernel/platform/membership"
	memberinfra "github.com/agile-factory/kernel/platform/membership/infrastructure"
	"github.com/agile-factory/kernel/platform/notification"
	notifinfra "github.com/agile-factory/kernel/platform/notification/infrastructure"
	"github.com/agile-factory/kernel/platform/tenant"
	tenantinfra "github.com/agile-factory/kernel/platform/tenant/infrastructure"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/config"
)

// App is a running hub minus its HTTP listener.
type App struct {
	pool    *pgxpool.Pool
	bus     *events.Bus
	health  *health.Checker
	handler http.Handler
	log     *slog.Logger
}

// New connects to the database, migrates it and wires every module.
func New(ctx context.Context, cfg config.Config, version string, log *slog.Logger) (*App, error) {
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("hub: database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("hub: database ping: %w", err)
	}
	a, err := build(cfg, version, pool, log)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return a, nil
}

func build(cfg config.Config, version string, pool *pgxpool.Pool, log *slog.Logger) (*App, error) {
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

	hc := health.New(health.Config{Version: version, DBPinger: pool})
	h := routes(routeDeps{
		cfg:    cfg,
		log:    log,
		health: hc,
		public: []registrar{authMod},
		member: []registrar{tenantMod, memberMod, auditMod},
	})
	hc.MarkReady()
	return &App{pool: pool, bus: bus, health: hc, handler: h, log: log}, nil
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

// Run drives the background work (the outbox dispatcher) until ctx ends.
func (a *App) Run(ctx context.Context) error {
	if err := a.bus.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("hub: events dispatcher: %w", err)
	}
	return nil
}

// Close releases the database pool. Call it after Run has returned.
func (a *App) Close() { a.pool.Close() }
