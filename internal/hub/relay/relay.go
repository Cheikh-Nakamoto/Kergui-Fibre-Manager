// Package relay is the hub module that moves commands between a household's
// app and the relay on its Wi-Fi: the 60-second lease that keeps one relay
// on the box, the long-polled command queue, and the signed results and
// snapshots that come back.
//
// It follows the kernel's custom-module convention: embedded migrations, a
// Config of dependencies, New, and RegisterRoutes.
package relay

import (
	"context"
	"embed"
	"log/slog"
	"net/http"
	"time"

	"github.com/agile-factory/kernel/platform/audit"
	"github.com/jackc/pgx/v5/pgxpool"

	agentsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
	agentshandler "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/handler"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay/handler"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay/infrastructure"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
)

// Migrations holds the SQL this module owns.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// sweepInterval is how often expired commands are marked. It only affects
// how quickly an app is told "nobody picked this up"; the relay never sees
// an expired command, because Claim filters on the deadline.
const sweepInterval = 30 * time.Second

// Config captures what the module needs. Trust, Agents and Tokens come from
// the agents module: the two are separate concerns over one household, not
// two systems.
type Config struct {
	Pool        *pgxpool.Pool
	Guard       *tenantaccess.Guard
	Trust       usecase.TrustReader
	Agents      agentsdomain.Repository
	Tokens      *agentshandler.TokenResolver
	IsOwner     usecase.OwnerLookup
	AuditLogger audit.EventLogger
	// Snapshots, when set, receives every verified snapshot: the inventory
	// module reads the observations out of it (RFC-005 H2).
	Snapshots usecase.SnapshotSink
	Logger    *slog.Logger
	Now       func() time.Time
	// CallerWait is how long an app request waits for a relay's answer
	// before it is handed the command id instead. Tests shorten it.
	CallerWait time.Duration
	// Timings are the lease timings; zero values take the RFC-005
	// defaults. Tests shorten them, keeping the ratios.
	Timings domain.Timings
}

// Module is the self-contained relay module.
type Module struct {
	Repo    *infrastructure.Repo
	Handler *handler.Handler
	agents  agentsdomain.Repository
	log     *slog.Logger
	now     func() time.Time
}

// New wires the module.
func New(cfg Config) *Module {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	wait := cfg.CallerWait
	if wait <= 0 {
		wait = usecase.CallerWait
	}
	repo := infrastructure.New(cfg.Pool)
	waiters := usecase.NewWaiters()
	// A nil *slog.Logger in an interface is not nil, so it is left out
	// rather than wrapped: the module logs only when the hub gave it one.
	var log usecase.Logger
	if cfg.Logger != nil {
		log = cfg.Logger
	}
	return &Module{
		Repo:   repo,
		agents: cfg.Agents,
		log:    cfg.Logger,
		now:    now,
		Handler: &handler.Handler{
			Guard:      cfg.Guard,
			Tokens:     cfg.Tokens,
			Heartbeat:  usecase.Heartbeat{Leases: repo, Trust: cfg.Trust, IsOwner: cfg.IsOwner, Now: usecase.Clock(now), Timings: cfg.Timings},
			Queue:      usecase.Queue{Commands: repo, Leases: repo, Waiters: waiters, Now: usecase.Clock(now)},
			Snapshots:  usecase.Snapshots{Repo: repo, Now: usecase.Clock(now), Sink: cfg.Snapshots, Log: log},
			Policy:     &handler.Policy{Trust: cfg.Trust, Agents: cfg.Agents},
			LongPoll:   cfg.Timings.LongPollOrDefault(),
			Audit:      cfg.AuditLogger,
			Now:        now,
			CallerWait: wait,
		},
	}
}

// RegisterRoutes mounts the household routes (kernel JWT required).
func (m *Module) RegisterRoutes(mux *http.ServeMux) { m.Handler.RegisterRoutes(mux) }

// RegisterRelayRoutes mounts the /relay/v1 routes (agent token required).
func (m *Module) RegisterRelayRoutes(mux *http.ServeMux) { m.Handler.RegisterRelayRoutes(mux) }

// Run sweeps commands nobody picked up until ctx ends. The hub calls it
// alongside the kernel's outbox dispatcher.
func (m *Module) Run(ctx context.Context) error {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			n, err := m.Repo.Expire(ctx, m.now().UTC())
			if err != nil && m.log != nil {
				m.log.Warn("relay: expiring commands", "error", err)
			}
			if n > 0 && m.log != nil {
				m.log.Info("relay: commands expired before a relay picked them up", "count", n)
			}
		}
	}
}
