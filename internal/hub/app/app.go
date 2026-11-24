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

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/payment/naboopay"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents"
	agentsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
	agentsusecase "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/adapters/console"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/adapters/expo"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/adapters/whatsmeow"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/backups"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/complaints"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/config"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/hubkey"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/identity"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/inventory"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/googleplay"
	licensinghandler "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/handler"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/signingkey"
	licensingusecase "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/messages"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay"
	relaydomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/releases"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/replyblock"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// App is a running hub minus its HTTP listener.
type App struct {
	pool       *pgxpool.Pool
	bus        *events.Bus
	health     *health.Checker
	handler    http.Handler
	log        *slog.Logger
	relay      *relay.Module
	inventory  *inventory.Module
	alerts     *alerts.Module
	licensing  *licensing.Module // nil when no signing key is configured
	notifier   alertsdomain.Notifier
	whatsapp   *whatsmeow.Notifier // nil unless KERGUI_HUB_NOTIFIER=whatsmeow
	pusher     alertsdomain.Pusher
	messages   *messages.Module
	complaints *complaints.Module
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
	gateway     port.PaymentGateway
	play        licensingusecase.PlayVerifier
	playPush    licensingusecase.PushVerifier
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

// WithPaymentGateway replaces NabooPay, so the integration tests can pay
// an order without a NabooPay account.
func WithPaymentGateway(g port.PaymentGateway) Option {
	return func(o *options) { o.gateway = g }
}

// WithPlay replaces the Google Play Developer API and Pub/Sub's push
// authentication, so the integration tests need no Google account.
func WithPlay(v licensingusecase.PlayVerifier, push licensingusecase.PushVerifier) Option {
	return func(o *options) { o.play, o.playPush = v, push }
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
		{Name: "hub_replyblock", FS: replyblock.Migrations, Path: "migrations"},
		{Name: "hub_licensing", FS: licensing.Migrations, Path: "migrations"},
		{Name: "hub_identity", FS: identity.Migrations, Path: "migrations"},
		{Name: "hub_admin", FS: admin.Migrations, Path: "migrations"},
		{Name: "hub_messages", FS: messages.Migrations, Path: "migrations"},
		{Name: "hub_releases", FS: releases.Migrations, Path: "migrations"},
		{Name: "hub_complaints", FS: complaints.Migrations, Path: "migrations"},
		{Name: "hub_backups", FS: backups.Migrations, Path: "migrations"},
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
	refreshRepo := authinfra.NewPostgresRefreshTokenRepository(pool)

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
		RefreshTokenRepo:   refreshRepo,
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
		OwnerKey: bundleOwnerKey(agentsMod.Trust),
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
	notifier, wa, err := notifier(context.Background(), cfg.WhatsApp, pool, log)
	if err != nil {
		return nil, err
	}
	number, err := alertsNumber(cfg.WhatsApp.Number)
	if err != nil {
		return nil, err
	}
	// App push (issue #49): the console channel logs until an Expo project
	// is configured, like the WhatsApp provider; the tokens the app
	// registers are the module's own.
	pusher := pushChannel(cfg.Push, log)
	alertsMod := alerts.New(alerts.Config{
		Pool: pool, Guard: guard, Notifier: notifier, Pusher: pusher,
		AuditLogger: auditMod.EventLogger(), Number: number,
		WebhookSecret: cfg.WhatsApp.WebhookSecret, Logger: log, Now: o.now,
	})
	alertsMod.Subscribe(bus)
	member := []registrar{}
	if wa != nil {
		// whatsmeow receives the replies itself (no webhook), and its
		// session is linked and unlinked by a platform admin (issue #59).
		wa.OnInbound(alertsMod.Deliver)
		member = append(member, &whatsmeow.Admin{Session: wa, Number: number, Audit: auditMod.EventLogger()})
	}

	// "1 = block": the one place the hub signs a command of its own. Its
	// key is derived per household and acts only because the owner
	// delegated it; without a seed the path is off and the household is
	// told so instead of being promised a block that never comes.
	keys, err := hubKeys(cfg.WhatsApp.KeySeed, log)
	if err != nil {
		return nil, err
	}
	replies := replyblock.New(replyblock.Config{
		Pool: pool, Guard: guard, Alerts: alertsMod.Repo, Trust: agentsMod.Trust,
		Commands: relayMod.Commands(), Keys: keys, IsAdmin: adminLookup(memberRepo),
		Approve: inventoryMod.Repo.SetApproval,
		Audit:   auditMod.EventLogger(), Logger: log, Now: o.now,
	})
	alertsMod.SetReplier(replies)

	// The number verified over WhatsApp is the hub identity (issue #41):
	// the licensing sign-in also signs the phone in to its household, with
	// the kernel's own repositories and token signer.
	identityMod := identity.New(identity.Config{
		Pool: pool, Users: userRepo, Tenants: tenantRepo, Settings: settingsRepo, Members: memberRepo,
		RefreshTokens: refreshRepo, Tokens: authMod.JWTService(), RefreshTTL: cfg.RefreshTTL,
		AuditLogger: auditMod.EventLogger(),
		// Household members (issue #42): invites by number over WhatsApp,
		// the kernel's revocation, and the removed member's alerts stopped.
		Guard: guard, Sender: notifier, Revoker: memberMod.RevokeUC, Now: o.now,
		OnRemoved: func(ctx context.Context, tenantID, userID string) error {
			return alertsMod.Repo.StopMember(ctx, tenantID, userID, o.now())
		},
	})

	// Pro passes (RFC-006). The sign-in codes and the expiry reminders go
	// through the same WhatsApp notifier as the alerts.
	licensingMod, err := licensingModule(cfg, pool, notifier, identityMod, log, o)
	if err != nil {
		return nil, err
	}
	var licensingRoutes registrar
	adminCfg := admin.Config{Pool: pool, Version: version, Audit: auditMod.EventLogger(), Now: o.now}
	if licensingMod != nil {
		licensingRoutes = licensingMod
		adminCfg.Grant = licensingMod.Service.CreditPass
		adminCfg.Support = licensingMod.Service
		if licensingMod.Service.Gateway != nil {
			// « Revérifier » on the « Paiements » page (issue #79).
			adminCfg.Recheck = licensingMod.Service.SettleOrder
		}
	}
	if wa != nil {
		adminCfg.WhatsApp = func() any { return wa.Status() }
	}
	// The back-office API (issue #62), platform administrators only.
	member = append(member, admin.New(adminCfg))
	// « Messages » (issue #80): announcements pushed to the households'
	// phones through the alerts' push sender, platform administrators only;
	// by WhatsApp (issue #81) to a household or a small group, queued and
	// paced (one every KERGUI_HUB_WHATSAPP_SEND_INTERVAL), the alerts
	// keeping their own direct path.
	messagesMod := messages.New(messages.Config{
		Pool: pool, Pusher: pusher, Targets: alertsMod.Repo, Notifier: notifier,
		SendInterval: cfg.WhatsApp.SendInterval, GroupMax: cfg.WhatsApp.GroupMax,
		Audit: auditMod.EventLogger(), Logger: log, Now: o.now,
	})
	member = append(member, messagesMod)
	// Complaints (issue #82): from the app and the customer area behind the
	// licensing session, answered from the back-office by push and, through
	// the WhatsApp queue above and its pace, by WhatsApp.
	complaintsCfg := complaints.Config{
		Pool: pool, Pusher: pusher, Targets: alertsMod.Repo, WhatsApp: messagesMod,
		SummaryInterval: cfg.ComplaintsSummaryInterval,
		Audit:           auditMod.EventLogger(), Logger: log, Now: o.now,
	}
	if licensingMod != nil {
		complaintsCfg.Session = licensingMod.Handler.Middleware
		complaintsCfg.Customer = func(ctx context.Context) (complaints.Customer, bool) {
			acc, ok := licensinghandler.AccountFrom(ctx)
			return complaints.Customer{AccountID: string(acc.ID), Phone: string(acc.Phone)}, ok
		}
	}
	complaintsMod := complaints.New(complaintsCfg)
	member = append(member, complaintsMod)
	// On WhatsApp (issue #83): what is no alert reply, STOP or opt-in is a
	// customer's complaint, or an admin's command (LISTE, VOIR, R, OK).
	alertsMod.SetInbox(complaintsMod)
	// The website APK's updates (issue #71): CI declares, the back-office
	// publishes, the app checks.
	releasesMod := releases.New(releases.Config{
		Pool: pool, Token: cfg.Releases.Token, Audit: auditMod.EventLogger(), Now: o.now,
	})
	member = append(member, releasesMod)
	if !cfg.Releases.DeclareEnabled() {
		log.Info("hub: CI release declarations are off, no KERGUI_HUB_RELEASES_TOKEN is set")
	}

	// The household's backups per box (issue #88): the owner's phones and
	// the household's relays send, every member reads.
	backupsMod := backups.New(backups.Config{
		Pool: pool, Guard: guard, Tokens: agentsMod.TokenResolver(), Now: o.now,
	})
	member = append(member, backupsMod)

	hc := health.New(health.Config{Version: version, DBPinger: pool})
	h := routes(routeDeps{
		cfg:    cfg,
		log:    log,
		health: hc,
		public: []registrar{authMod},
		member: append(member, tenantMod, memberMod, auditMod, agentsMod, relayMod, inventoryMod, alertsMod, replies, identityMod),
		relay:  []relayRegistrar{agentsMod, relayMod, backupsMod},
		hooks:  []webhookRegistrar{alertsMod},

		licensing: licensingRoutes,
		// Behind the licensing session too: the customers' complaints.
		licensingMore: []licensingRegistrar{complaintsMod},
		releases:      releasesMod,
	})
	hc.MarkReady()
	return &App{
		pool: pool, bus: bus, health: hc, handler: h, log: log,
		relay: relayMod, inventory: inventoryMod, alerts: alertsMod, licensing: licensingMod, notifier: notifier, whatsapp: wa,
		pusher: pusher, messages: messagesMod, complaints: complaintsMod,
	}, nil
}

// licensingModule builds the licensing module, or nil when no signing key
// is configured: a hub that cannot issue tokens sells no passes.
func licensingModule(cfg config.Config, pool *pgxpool.Pool, sender alertsdomain.Notifier,
	ident licensingusecase.Identity, log *slog.Logger, o options) (*licensing.Module, error) {
	key, err := cfg.Licensing.LoadSigningKey()
	if err != nil {
		return nil, fmt.Errorf("hub: %w", err)
	}
	if key == nil {
		log.Info("hub: Pro passes are off, no KERGUI_HUB_LICENSING_SIGNING_KEY(_FILE) is set")
		return nil, nil
	}
	rotation, err := cfg.Licensing.LoadRotation(key)
	if err != nil {
		return nil, fmt.Errorf("hub: %w", err)
	}
	gateway := o.gateway
	if gateway == nil && cfg.Licensing.NabooPayAPIKey != "" {
		gateway = naboopay.New(cfg.Licensing.NabooPayAPIKey, cfg.Licensing.NabooPayBaseURL)
	}
	if gateway == nil {
		log.Info("hub: Pro checkouts are off, no KERGUI_HUB_NABOOPAY_API_KEY is set")
	}
	play, push, err := playAdapters(cfg.Licensing.Play, log, o)
	if err != nil {
		return nil, err
	}
	m, err := licensing.New(licensing.Config{
		Pool: pool, Sender: sender, Gateway: gateway, SigningKey: key, Rotation: rotation,
		WebhookSecret: cfg.Licensing.NabooPayWebhookSecret, ReturnURL: cfg.Licensing.ReturnURL,
		BuyURL: buyURL(cfg.PublicURL), Play: play, PlayPackage: cfg.Licensing.Play.PackageName, PlayPush: push,
		Identity: ident, SweepEvery: cfg.Licensing.SweepInterval, Logger: log, Now: o.now,
	})
	if err != nil {
		return nil, fmt.Errorf("hub: %w", err)
	}
	log.Info("hub: Pro passes are on", "key_id", signingkey.KeyID(m.Service.PublicKey()),
		"next_keys", len(rotation.Next), "retiring_keys", len(rotation.Retiring), "revoked_keys", len(rotation.Revoked))
	return m, nil
}

// buyURL is the hub's public purchase page, which the J-3 expiry reminder
// links to. KERGUI_HUB_PUBLIC_URL is required, so it is always known.
func buyURL(publicURL string) string {
	if publicURL == "" {
		return ""
	}
	return strings.TrimRight(publicURL, "/") + licensinghandler.BuyPath
}

// playAdapters builds the Google Play Developer API client and the check of
// Pub/Sub's push token, each nil when it is not configured.
func playAdapters(cfg config.Play, log *slog.Logger, o options) (licensingusecase.PlayVerifier, licensingusecase.PushVerifier, error) {
	play, push := o.play, o.playPush
	if !cfg.Enabled() && play == nil {
		log.Info("hub: Google Play Billing is off, no KERGUI_HUB_PLAY_PACKAGE_NAME is set")
		return nil, nil, nil
	}
	if play == nil {
		raw, err := cfg.LoadServiceAccount()
		if err != nil {
			return nil, nil, fmt.Errorf("hub: %w", err)
		}
		sa, err := googleplay.ParseServiceAccount(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("hub: %w", err)
		}
		c, err := googleplay.New(googleplay.Config{PackageName: cfg.PackageName, ServiceAccount: sa, Now: o.now})
		if err != nil {
			return nil, nil, fmt.Errorf("hub: %w", err)
		}
		play = c
	}
	if push == nil && cfg.WebhookEnabled() {
		v, err := googleplay.NewPushVerifier(googleplay.PushConfig{
			Audience: cfg.RTDNAudience, ServiceAccount: cfg.RTDNServiceAccount, Now: o.now,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("hub: %w", err)
		}
		push = v
	}
	if push == nil {
		log.Info("hub: Google Play notifications are off, no KERGUI_HUB_PLAY_RTDN_SERVICE_ACCOUNT is set")
	}
	log.Info("hub: Google Play Billing is on", "package", cfg.PackageName)
	return play, push, nil
}

// bundleOwnerKey is the key id of the owner who signed the household's
// bundle in force, "" when it has none yet: the inventory keeps only that
// owner's phones (issue #87). The hub reads the bundle unverified, as it does
// for its version: the phones and relays check the signature themselves.
func bundleOwnerKey(tm agentsusecase.TrustMaterial) func(context.Context, string) (string, error) {
	return func(ctx context.Context, tenantID string) (string, error) {
		stored, err := tm.GetBundle(ctx, tenantID)
		if errors.Is(err, agentsdomain.ErrNoBundle) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		b, err := trust.Signed[trust.Bundle]{Payload: stored.Payload, Sig: stored.Sig}.Unverified()
		if err != nil {
			return "", nil // a bundle the hub cannot read names no owner
		}
		return b.Owner.KeyID(), nil
	}
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

// notifier builds the messaging provider named by the configuration. The
// whatsmeow session is returned too, for its admin routes and its Run.
func notifier(ctx context.Context, cfg config.WhatsApp, pool *pgxpool.Pool, log *slog.Logger) (alertsdomain.Notifier, *whatsmeow.Notifier, error) {
	switch cfg.Provider {
	case config.NotifierWhatsmeow:
		n, err := whatsmeow.Open(ctx, pool, log)
		if err != nil {
			return nil, nil, fmt.Errorf("hub: whatsapp: %w", err)
		}
		log.Info("hub: whatsapp provider configured", "provider", n.Name(), "status", n.Status())
		return n, n, nil
	default:
		// The console provider: messages go to the hub's log, so a pilot
		// without WhatsApp can still see what a household would receive.
		return &console.Notifier{Secret: []byte(cfg.WebhookSecret), Log: log}, nil, nil
	}
}

// hubKeys builds the deriver of the hub's narrow block key. No seed is not
// an error: it is a hub that sends alerts and leaves the blocking to the
// app.
func hubKeys(seed string, log *slog.Logger) (*hubkey.Deriver, error) {
	d, err := hubkey.New([]byte(seed))
	switch {
	case errors.Is(err, hubkey.ErrNoSeed):
		log.Info("hub: replying 1 to an alert will not block, no KERGUI_HUB_KEY_SEED is set")
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("hub: %w", err)
	}
	return d, nil
}

// adminLookup answers "may this user act on the household's box?" for the
// WhatsApp reply path, without that module touching the kernel's tables.
func adminLookup(members memberdomain.MembershipRepository) func(context.Context, string, string) (bool, error) {
	return func(ctx context.Context, tenantID, userID string) (bool, error) {
		m, err := members.FindByUserAndTenant(ctx, userID, tenantID)
		if err != nil {
			if errors.Is(err, memberdomain.ErrMembershipNotFound) {
				return false, nil
			}
			return false, err
		}
		if !m.IsActive() {
			return false, nil
		}
		return m.IsOwner() || m.Role == memberdomain.MEMBER_ROLE_ADMIN, nil
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

// pushChannel builds the app push channel named by the configuration.
func pushChannel(cfg config.Push, log *slog.Logger) alertsdomain.Pusher {
	if cfg.Provider == config.PushExpo {
		log.Info("hub: app push through Expo", "access_token", cfg.ExpoAccessToken != "")
		return expo.New(expo.Config{BaseURL: cfg.ExpoURL, AccessToken: cfg.ExpoAccessToken})
	}
	return &console.Pusher{Log: log}
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

// Licensing is the licensing module, nil when it is off. The integration
// tests run its sweep directly instead of waiting for the ticker.
func (a *App) Licensing() *licensing.Module { return a.licensing }

// Notifier is the messaging provider this hub was wired with. The
// integration tests read what the console provider "sent"; a deployment
// reads its name and its licence notice through the WhatsApp route.
func (a *App) Notifier() alertsdomain.Notifier { return a.notifier }

// Pusher is the app push channel this hub was wired with. The end-to-end
// tests read what the console channel "pushed".
func (a *App) Pusher() alertsdomain.Pusher { return a.pusher }

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
		runs := map[string]func(context.Context) error{
			"relay":     a.relay.Run,
			"inventory": a.inventory.Run,
			// The back-office's WhatsApp queue (issue #81).
			"messages": a.messages.Run,
			// The admins' WhatsApp summary of new complaints (issue #83).
			"complaints": a.complaints.Run,
		}
		if a.licensing != nil {
			runs["licensing"] = a.licensing.Run
		}
		if a.whatsapp != nil {
			runs["whatsapp"] = a.whatsapp.Run
		}
		for name, run := range runs {
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
