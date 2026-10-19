// Package alerts is the hub module that tells a household a new device
// joined its Wi-Fi, and reads what it answers (RFC-005 H2).
//
// It owns no judgement of its own: it subscribes to the inventory's
// router.device_first_seen event, composes a message from what that event
// says, and sends it through a Notifier. One port, two adapters — the
// console one for the pilot and CI, Evolution Go for WhatsApp — plus a push
// port whose real adapter lands with the app in H3.
//
// It follows the kernel's custom-module convention: embedded migrations, a
// Config of dependencies, New, RegisterRoutes and Subscribe.
package alerts

import (
	"context"
	"embed"
	"log/slog"
	"net/http"
	"time"

	"github.com/agile-factory/kernel/kernel/events"
	"github.com/agile-factory/kernel/platform/audit"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/handler"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/infrastructure"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/usecase"
	inventorydomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/inventory/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
)

// Migrations holds the SQL this module owns.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// Config captures what the module needs.
type Config struct {
	Pool  *pgxpool.Pool
	Guard *tenantaccess.Guard
	// Notifier is the messaging provider. It is required: a hub wired
	// without one would drop households' alerts silently, so the
	// composition root plugs in the console adapter instead.
	Notifier domain.Notifier
	// Pusher and Targets are the second channel. Targets has no
	// implementation before H3, so the path walks through and finds nobody.
	Pusher      domain.Pusher
	Targets     domain.PushTargets
	AuditLogger audit.EventLogger
	// Number is the household-facing Kergui WhatsApp number (E.164).
	Number domain.Phone
	// WebhookSecret authenticates the provider's inbound calls; empty
	// turns the inbound route off.
	WebhookSecret string
	Logger        *slog.Logger
	Now           func() time.Time
}

// Module is the self-contained alerts module.
type Module struct {
	Repo    *infrastructure.Repo
	Handler *handler.Handler
	Alerts  usecase.Alerts
}

// New wires the module.
func New(cfg Config) *Module {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	repo := infrastructure.New(cfg.Pool)
	var log usecase.Logger
	if cfg.Logger != nil {
		// A nil *slog.Logger inside an interface is not nil, so it is left
		// out rather than wrapped.
		log = cfg.Logger
	}
	optIn := usecase.OptIn{
		Repo: repo, Number: cfg.Number, Now: usecase.Clock(now),
		Provider: cfg.Notifier.Name(), Notice: cfg.Notifier.Attribution(),
	}
	return &Module{
		Repo: repo,
		Alerts: usecase.Alerts{
			Repo: repo, Notifier: cfg.Notifier, Pusher: cfg.Pusher, Targets: cfg.Targets,
			Now: usecase.Clock(now), Log: log,
		},
		Handler: &handler.Handler{
			Guard: cfg.Guard, OptIn: optIn, Notifier: cfg.Notifier, Repo: repo,
			Audit: cfg.AuditLogger, Now: now, WebhookSecret: cfg.WebhookSecret,
			Log: handlerLogger(log),
		},
	}
}

// RegisterRoutes mounts the household routes (kernel JWT required).
func (m *Module) RegisterRoutes(mux *http.ServeMux) { m.Handler.RegisterRoutes(mux) }

// RegisterWebhookRoutes mounts the provider's inbound route, which carries
// its own secret instead of a user's token.
func (m *Module) RegisterWebhookRoutes(mux *http.ServeMux) { m.Handler.RegisterWebhookRoutes(mux) }

// Subscribe registers the module's outbox handlers. It must be called
// before the dispatcher starts, as the kernel's own modules require.
func (m *Module) Subscribe(bus *events.Bus) {
	bus.Subscribe(inventorydomain.EventDeviceFirstSeen, func(ctx context.Context, ev events.Event) error {
		return m.Alerts.HandleFirstSeen(ctx, ev.Payload)
	})
}

// SetReplier wires the "1 = block" path once it exists. It is a setter
// because the command side depends on this module's alerts, and this module
// must not depend back on the relay: the composition root closes the loop.
func (m *Module) SetReplier(r handler.Replier) { m.Handler.Replier = r }

// handlerLogger adapts the use cases' logging port to the handler's, which
// wants the same two methods.
func handlerLogger(l usecase.Logger) handler.Logger {
	if l == nil {
		return nil
	}
	return l
}
