// Package httpapi is the REST delivery adapter (Milestone 2). It exposes the same
// read-only use cases as the CLI over JSON, plus block/unblock endpoints that
// return 501 until a model's write path is verified on real hardware. It never
// returns a router password to clients (brief §12): the response DTOs carry none.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Services bundles the interactors the API drives.
type Services struct {
	Discover *usecase.DiscoverRouter
	List     *usecase.ListDevices
	Inspect  *usecase.InspectRouter
	Block    *usecase.BlockDevice
	Unblock  *usecase.UnblockDevice
}

// Config is the server's fixed router target.
type Config struct {
	BaseURL   string
	AdapterID string
	Opts      port.RouterOptions
	Version   string
}

// Server serves the JSON API for one configured router.
type Server struct {
	svc Services
	cfg Config
}

// New builds the server.
func New(svc Services, cfg Config) *Server { return &Server{svc: svc, cfg: cfg} }

// Handler returns the router (Go 1.22+ method+pattern mux).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/discover", s.discover)
	mux.HandleFunc("GET /api/devices", s.devices)
	mux.HandleFunc("GET /api/inspect", s.inspect)
	mux.HandleFunc("POST /api/devices/{mac}/block", s.block)
	mux.HandleFunc("POST /api/devices/{mac}/unblock", s.unblock)
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.cfg.Version})
}

func (s *Server) discover(w http.ResponseWriter, r *http.Request) {
	info, err := s.svc.Discover.Execute(r.Context(), usecase.DiscoverInput{BaseURL: s.cfg.BaseURL, Opts: s.cfg.Opts})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) devices(w http.ResponseWriter, r *http.Request) {
	views, err := s.svc.List.Execute(r.Context(), usecase.ListDevicesInput{
		BaseURL: s.cfg.BaseURL, AdapterID: s.cfg.AdapterID, Opts: s.cfg.Opts, Persist: true,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if views == nil {
		views = []port.DeviceView{}
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) inspect(w http.ResponseWriter, r *http.Request) {
	rep, err := s.svc.Inspect.Execute(r.Context(), usecase.InspectInput{
		BaseURL: s.cfg.BaseURL, AdapterID: s.cfg.AdapterID, Opts: s.cfg.Opts,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) block(w http.ResponseWriter, r *http.Request)   { s.change(w, r, true) }
func (s *Server) unblock(w http.ResponseWriter, r *http.Request) { s.change(w, r, false) }

func (s *Server) change(w http.ResponseWriter, r *http.Request, block bool) {
	mac, err := domain.ParseMAC(r.PathValue("mac"))
	if err != nil {
		writeErr(w, err)
		return
	}
	in := usecase.ChangeAccessInput{
		BaseURL: s.cfg.BaseURL, AdapterID: s.cfg.AdapterID, MAC: mac, Opts: s.cfg.Opts,
		Confirm: true, // an explicit POST is the confirmation
	}
	if block {
		err = s.svc.Block.Execute(r.Context(), in)
	} else {
		err = s.svc.Unblock.Execute(r.Context(), in)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	action := "unblocked"
	if block {
		action = "blocked"
	}
	writeJSON(w, http.StatusOK, map[string]string{"mac": mac.String(), "status": action})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	writeJSON(w, errToStatus(err), map[string]string{"error": err.Error()})
}

func errToStatus(err error) int {
	switch {
	case errors.Is(err, domain.ErrNotImplemented):
		return http.StatusNotImplemented // 501
	case errors.Is(err, domain.ErrAuthFailed), errors.Is(err, domain.ErrSessionExpired):
		return http.StatusUnauthorized // 401
	case errors.Is(err, domain.ErrInvalidMAC):
		return http.StatusBadRequest // 400
	case errors.Is(err, domain.ErrUnsupportedModel), errors.Is(err, domain.ErrUnsupportedFirmware):
		return http.StatusUnprocessableEntity // 422
	case errors.Is(err, domain.ErrRouterUnreachable):
		return http.StatusBadGateway // 502
	default:
		return http.StatusInternalServerError // 500
	}
}
