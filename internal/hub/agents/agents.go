// Package agents is the hub module that owns a household's relays: the
// pairing codes that let one join, the tokens it authenticates with, its
// public keys, and the trust material the owner signs or seals for it.
//
// It follows the kernel's custom-module convention: embedded migrations, a
// Config of dependencies, New, and RegisterRoutes.
package agents

import (
	"embed"
	"net/http"
	"time"

	"github.com/agile-factory/kernel/platform/audit"
	memberdomain "github.com/agile-factory/kernel/platform/membership/domain"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/handler"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/infrastructure"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
)

// Migrations holds the SQL this module owns; the hub composes it through
// migrator.Run under its own source name.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// Config captures what the module needs.
type Config struct {
	Pool        *pgxpool.Pool
	Members     memberdomain.MembershipRepository
	AuditLogger audit.EventLogger
	// Now is the module's clock; tests inject one to expire a pairing code
	// without waiting five minutes.
	Now func() time.Time
}

// Module is the self-contained agents module.
type Module struct {
	Repo    *infrastructure.Repo
	Handler *handler.Handler
	Tokens  *handler.TokenResolver
	Trust   usecase.TrustMaterial
}

// New wires the module.
func New(cfg Config) *Module {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	repo := infrastructure.New(cfg.Pool)
	tokens := &handler.TokenResolver{Repo: repo, Now: now}
	trust := usecase.TrustMaterial{Repo: repo, Agents: repo}
	return &Module{
		Repo:   repo,
		Tokens: tokens,
		Trust:  trust,
		Handler: &handler.Handler{
			Guard:   tenantaccess.New(cfg.Members),
			Issue:   usecase.IssuePairing{Pairings: repo, Now: usecase.Clock(now)},
			Enroll:  usecase.Enroll{Pairings: repo, Agents: repo, Now: usecase.Clock(now)},
			Agents:  usecase.Agents{Repo: repo, Now: usecase.Clock(now)},
			Trust:   trust,
			Audit:   cfg.AuditLogger,
			Resolve: tokens,
		},
	}
}

// RegisterRoutes mounts the household routes (kernel JWT required).
func (m *Module) RegisterRoutes(mux *http.ServeMux) { m.Handler.RegisterRoutes(mux) }

// RegisterRelayRoutes mounts the /relay/v1 routes (agent token required).
func (m *Module) RegisterRelayRoutes(mux *http.ServeMux) { m.Handler.RegisterRelayRoutes(mux) }

// Agents exposes the agent repository to the relay module, which needs to
// resolve tokens and read a relay's public keys.
func (m *Module) Agents() domain.Repository { return m.Repo }

// TokenResolver exposes the relay authentication middleware.
func (m *Module) TokenResolver() *handler.TokenResolver { return m.Tokens }
