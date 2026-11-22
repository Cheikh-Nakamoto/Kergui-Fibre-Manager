// Package licensing is the hub module that sells the app's Pro tiers
// (RFC-006): accounts verified by a WhatsApp code, the 7-day trial, NabooPay
// passes and their webhook, Google Play Billing subscriptions and their
// real-time notifications, box transfers, the expiry reminder, and the
// Ed25519 entitlement token the app verifies offline.
//
// It follows the hub's module convention: embedded migrations, a Config of
// dependencies, New, RegisterRoutes, and a Run the composition root drives.
// Its routes carry their own authentication (a kgs_ session token,
// NabooPay's signature, or Pub/Sub's OIDC token), so they are mounted
// outside the kernel's JWT.
package licensing

import (
	"context"
	"crypto/ed25519"
	"embed"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/handler"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/infrastructure"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/signingkey"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Migrations holds the SQL this module owns.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// DefaultSweepEvery is how often Run sends reminders and re-checks orders.
const DefaultSweepEvery = 10 * time.Minute

// Config captures what the module needs.
type Config struct {
	Pool *pgxpool.Pool
	// Sender carries the sign-in codes and the reminders: the alerts
	// module's notifier (Evolution Go, or the console in a pilot).
	Sender usecase.Sender
	// Gateway is NabooPay; nil turns checkouts off.
	Gateway port.PaymentGateway
	// SigningKey is required: without it there is no token to sell. It is
	// the active key; Rotation holds the others the hub publishes.
	SigningKey ed25519.PrivateKey
	Rotation   signingkey.Rotation
	// WebhookSecret authenticates NabooPay's webhook; empty turns it off.
	WebhookSecret string
	// ReturnURL is where NabooPay sends the payer back.
	ReturnURL string
	// BuyURL is the public purchase page the expiry reminder links to.
	BuyURL string
	// Play is the Google Play Developer API; nil turns the Play channel
	// off (its routes answer 404). PlayPackage is the app's package name.
	Play        usecase.PlayVerifier
	PlayPackage string
	// PlayPush authenticates Pub/Sub's push of Play's notifications; nil
	// turns the notification webhook off.
	PlayPush   usecase.PushVerifier
	SweepEvery time.Duration
	Logger     *slog.Logger
	Now        func() time.Time
}

// Module is the self-contained licensing module.
type Module struct {
	Repo    *infrastructure.Repo
	Service *usecase.Service
	Handler *handler.Handler
	every   time.Duration
	log     *slog.Logger
}

// New wires the module.
func New(cfg Config) (*Module, error) {
	if len(cfg.SigningKey) != ed25519.PrivateKeySize {
		return nil, errors.New("licensing: an Ed25519 signing key is required")
	}
	if cfg.Sender == nil {
		return nil, errors.New("licensing: a WhatsApp sender is required")
	}
	repo := infrastructure.New(cfg.Pool)
	var log usecase.Logger
	if cfg.Logger != nil {
		log = cfg.Logger // a nil *slog.Logger in an interface would not be nil
	}
	svc := &usecase.Service{
		Store: repo, Sender: cfg.Sender, Gateway: cfg.Gateway, SigningKey: cfg.SigningKey,
		NextKeys: cfg.Rotation.Next, RetiringKeys: cfg.Rotation.Retiring, RevokedKeyIDs: cfg.Rotation.Revoked,
		ReturnURL: cfg.ReturnURL, BuyURL: cfg.BuyURL, Play: cfg.Play, PlayPackage: cfg.PlayPackage, Now: cfg.Now, Log: log,
	}
	every := cfg.SweepEvery
	if every <= 0 {
		every = DefaultSweepEvery
	}
	return &Module{
		Repo: repo, Service: svc, every: every, log: cfg.Logger,
		Handler: &handler.Handler{Svc: svc, WebhookSecret: cfg.WebhookSecret, PlayPush: cfg.PlayPush, Log: log},
	}, nil
}

// RegisterRoutes mounts the /licensing/v1 routes.
func (m *Module) RegisterRoutes(mux *http.ServeMux) { m.Handler.RegisterRoutes(mux) }

// Sweep runs the background work once: the expiry reminders, then the
// NabooPay orders whose webhook never came.
func (m *Module) Sweep(ctx context.Context) error {
	sent, rerr := m.Service.SendReminders(ctx)
	credited, perr := m.Service.SettlePending(ctx)
	if m.log != nil && (sent > 0 || credited > 0) {
		m.log.Info("licensing: sweep", "reminders", sent, "credited", credited)
	}
	return errors.Join(rerr, perr)
}

// Run sweeps on a ticker until ctx ends. A failed sweep is logged and the
// next one retries: nothing it does is lost by waiting.
func (m *Module) Run(ctx context.Context) error {
	t := time.NewTicker(m.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := m.Sweep(ctx); err != nil && ctx.Err() == nil && m.log != nil {
				m.log.Warn("licensing: sweep failed", "error", err)
			}
		}
	}
}
