// Package releases is the hub module behind the website APK's in-app
// updates (issue #65, hub side #71): CI declares each build it makes, a
// platform administrator publishes it from the back-office after trying it,
// may withdraw it and sets the minimum build; the app asks which build it
// should run.
//
// It follows the hub's module convention: embedded migrations, a Config of
// dependencies, New and the route registrations. Its /releases/v1 routes
// carry no user token (CI's own bearer token, an anonymous check), so they
// are mounted outside the kernel's JWT; the back-office routes are inside it.
package releases

import (
	"embed"
	"net/http"
	"time"

	"github.com/agile-factory/kernel/platform/audit"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/releases/handler"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/releases/infrastructure"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/releases/usecase"
)

// Migrations holds the SQL this module owns.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// Config captures what the module needs.
type Config struct {
	Pool *pgxpool.Pool
	// Token is CI's declare token (KERGUI_HUB_RELEASES_TOKEN); empty turns
	// the declare route off.
	Token string
	Audit audit.EventLogger
	Now   func() time.Time
}

// Module is the self-contained releases module.
type Module struct {
	Service *usecase.Service
	Handler *handler.Handler
}

// New wires the module.
func New(cfg Config) *Module {
	svc := &usecase.Service{Store: infrastructure.New(cfg.Pool), Now: cfg.Now}
	return &Module{Service: svc, Handler: &handler.Handler{Svc: svc, Token: cfg.Token, Audit: cfg.Audit}}
}

// RegisterRoutes mounts the back-office routes (/api/v1/admin/releases).
func (m *Module) RegisterRoutes(mux *http.ServeMux) { m.Handler.RegisterRoutes(mux) }

// RegisterPublicRoutes mounts /releases/v1: CI's declaration and the app's
// check.
func (m *Module) RegisterPublicRoutes(mux *http.ServeMux) { m.Handler.RegisterPublicRoutes(mux) }
