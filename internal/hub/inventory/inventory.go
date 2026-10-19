// Package inventory is the hub module that keeps the household's reference
// list of devices and decides what is new (RFC-005 H2).
//
// It never invents anything: every record comes from an observations
// snapshot a relay signed, and every alert carries the digest of that
// snapshot so the owner's app can check it. The hub transports; the owner
// decides.
//
// It follows the kernel's custom-module convention: embedded migrations, a
// Config of dependencies, New, RegisterRoutes and Run.
package inventory

import (
	"context"
	"embed"
	"log/slog"
	"net/http"
	"time"

	"github.com/agile-factory/kernel/kernel/events"
	"github.com/jackc/pgx/v5/pgxpool"

	coredomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/inventory/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/inventory/handler"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/inventory/infrastructure"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/inventory/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
)

// Migrations holds the SQL this module owns.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// sweepInterval is how often closed windows are announced. It only bounds how
// late an alert can be: a window of two minutes announced within ten seconds
// of closing is well inside what a WhatsApp message takes to arrive anyway.
const sweepInterval = 10 * time.Second

// Config captures what the module needs.
type Config struct {
	Pool  *pgxpool.Pool
	Guard *tenantaccess.Guard
	// Publisher is the kernel's transactional outbox. The module publishes
	// inside the transaction that closes a window.
	Publisher events.Publisher
	Logger    *slog.Logger
	Now       func() time.Time
	// Rules are the recognition thresholds; zero takes the defaults of
	// internal/domain, which the recognition tests document.
	Rules coredomain.RecognitionRules
	// Window overrides the 2-minute grouping window. Tests shorten it.
	Window time.Duration
}

// Module is the self-contained inventory module.
type Module struct {
	Repo    *infrastructure.Repo
	Handler *handler.Handler
	Ingest  usecase.Ingest
	windows usecase.Windows
	sweep   time.Duration
	log     *slog.Logger
}

// New wires the module.
func New(cfg Config) *Module {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	repo := infrastructure.New(cfg.Pool)
	log := cfg.Logger
	// A sweep never needs to be slower than the window it announces, which
	// is what lets a test shorten both with one option.
	sweep := sweepInterval
	if cfg.Window > 0 && cfg.Window < sweep {
		sweep = cfg.Window
	}
	return &Module{
		Repo:    repo,
		Handler: &handler.Handler{Guard: cfg.Guard, Repo: repo},
		Ingest: usecase.Ingest{
			Repo: repo, Now: usecase.Clock(now), Rules: cfg.Rules, Window: cfg.Window, Log: logger{log},
		},
		windows: usecase.Windows{
			Repo: repo, Announcer: outbox{cfg.Publisher, now}, Now: usecase.Clock(now), Log: logger{log},
		},
		sweep: sweep,
		log:   log,
	}
}

// RegisterRoutes mounts the household routes (kernel JWT required).
func (m *Module) RegisterRoutes(mux *http.ServeMux) { m.Handler.RegisterRoutes(mux) }

// Snapshots exposes the sink the relay module calls once it has verified a
// relay's snapshot. The relay module carries bytes and knows nothing about
// devices; this module reads them and knows nothing about leases.
func (m *Module) Snapshots() usecase.Ingest { return m.Ingest }

// Run announces the windows that are due until ctx ends. The hub runs it
// alongside the kernel's outbox dispatcher and the relay sweep.
func (m *Module) Run(ctx context.Context) error {
	t := time.NewTicker(m.sweep)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			n, err := m.windows.Sweep(ctx)
			if err != nil && m.log != nil {
				m.log.Warn("inventory: sweeping alert windows", "error", err)
			}
			if n > 0 && m.log != nil {
				m.log.Info("inventory: new devices announced", "windows", n)
			}
		}
	}
}

// outbox publishes a closed window as the event the alerts module listens
// for. It runs inside the transaction that closed the window, which is what
// the kernel's publisher requires and what makes "announced once" true.
type outbox struct {
	publisher events.Publisher
	now       func() time.Time
}

func (o outbox) Announce(ctx context.Context, w domain.Window) error {
	if o.publisher == nil {
		return nil // a hub wired without an outbox (a unit test) alerts nobody
	}
	devices := make([]map[string]any, 0, len(w.Items))
	for _, it := range w.Items {
		devices = append(devices, map[string]any{
			"no": it.No, "mac": it.MAC, "device_id": it.DeviceID,
			"hostname": it.Hostname, "ip": it.IP, "ssid": it.SSID,
			"snapshot_sha256": it.SnapshotSHA256, "as_of": it.AsOf,
		})
	}
	return o.publisher.Publish(ctx, events.Event{
		Type: domain.EventDeviceFirstSeen,
		Payload: map[string]any{
			"tenant_id": w.TenantID,
			"window_id": w.ID,
			"opened_at": w.OpenedAt,
			"devices":   devices,
		},
		Meta: events.EventMeta{TenantID: w.TenantID, OccurredAt: o.now().UTC()},
	})
}

// logger adapts *slog.Logger to the use cases' small logging port, and
// tolerates a hub wired without one.
type logger struct{ log *slog.Logger }

func (l logger) Info(msg string, args ...any) {
	if l.log != nil {
		l.log.Info(msg, args...)
	}
}

func (l logger) Warn(msg string, args ...any) {
	if l.log != nil {
		l.log.Warn(msg, args...)
	}
}
