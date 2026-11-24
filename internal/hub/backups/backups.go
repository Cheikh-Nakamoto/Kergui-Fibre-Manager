// Package backups is the hub module that keeps a household's Kergui backup
// per box (issue #88, parent #70): the same archive as `kergui export` and
// the app's « Importer une sauvegarde » (appareils.csv, conso.csv). The
// owner's phones and the household's relays send theirs; every member reads
// it, so a new or second phone gets the names back. When two sources send,
// the hub merges with the import's rules (internal/backup).
//
// It follows the hub's module convention: embedded migrations, a Config of
// dependencies, New and the route registrations.
package backups

import (
	"embed"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	agentshandler "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/handler"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/backups/handler"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/backups/infrastructure"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/backups/usecase"
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
	// Tokens resolves a relay's kga_ token (the agents module's).
	Tokens *agentshandler.TokenResolver
	Now    func() time.Time
}

// Module is the self-contained backups module.
type Module struct {
	Service *usecase.Service
	Handler *handler.Handler
}

// New wires the module.
func New(cfg Config) *Module {
	svc := &usecase.Service{Store: infrastructure.New(cfg.Pool), Now: cfg.Now}
	return &Module{Service: svc, Handler: &handler.Handler{Guard: cfg.Guard, Tokens: cfg.Tokens, Svc: svc}}
}

// RegisterRoutes mounts the household routes (kernel JWT required).
func (m *Module) RegisterRoutes(mux *http.ServeMux) { m.Handler.RegisterRoutes(mux) }

// RegisterRelayRoutes mounts the relay's route (kga_ token).
func (m *Module) RegisterRelayRoutes(mux *http.ServeMux) { m.Handler.RegisterRelayRoutes(mux) }
