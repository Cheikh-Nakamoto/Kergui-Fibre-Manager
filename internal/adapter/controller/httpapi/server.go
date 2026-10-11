// Package httpapi is the REST delivery adapter (Milestone 2). It exposes the same
// read-only use cases as the CLI over JSON, plus block/unblock endpoints that
// return 501 until a model's write path is verified on real hardware. It never
// returns a router password to clients (brief §12): the response DTOs carry none.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/webui"
)

// Services bundles the interactors the API drives.
type Services struct {
	Discover *usecase.DiscoverRouter
	List     *usecase.ListDevices
	Inspect  *usecase.InspectRouter
	Block    *usecase.BlockDevice
	Unblock  *usecase.UnblockDevice
	Auth     *usecase.Authenticate
	Rename   *usecase.RenameDevice
	Filter   *usecase.MACFilter
	WiFi     *usecase.WiFiAccess
	Meter    *usecase.Metering
	Billing  *usecase.Billing
	Members  *usecase.Members
	Enforce  *usecase.Enforcement
	Identity *usecase.Identity
	Logger   port.Logger    // optional
	Logs     port.LogReader // optional; backs GET /api/logs
	// OnChange is called (non-blocking) after anything that changes who is cut
	// or what they owe, so the captive portal updates at once. Optional.
	OnChange func()
}

// Config is the server's fixed router target.
type Config struct {
	BaseURL   string
	AdapterID string
	Opts      port.RouterOptions
	Version   string
	Env       []EnvVar // server environment, shown in the settings panel
}

// EnvVar describes one environment variable for the settings panel. Value is
// only ever filled for non-secret variables.
type EnvVar struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Secret      bool   `json:"secret"`
	Set         bool   `json:"set"`
	Value       string `json:"value,omitempty"`
	Example     string `json:"example"`
}

// Server serves the JSON API for one configured router.
type Server struct {
	svc Services
	cfg Config
}

// New builds the server.
func New(svc Services, cfg Config) *Server {
	if svc.Logger == nil {
		svc.Logger = nopLogger{}
	}
	return &Server{svc: svc, cfg: cfg}
}

// Handler returns the router (Go 1.22+ method+pattern mux).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/discover", s.discover)
	mux.HandleFunc("GET /api/devices", s.devices)
	mux.HandleFunc("GET /api/inspect", s.inspect)
	mux.HandleFunc("POST /api/devices/{mac}/block", s.block)
	mux.HandleFunc("POST /api/devices/{mac}/unblock", s.unblock)
	mux.HandleFunc("PATCH /api/devices/{mac}", s.rename)
	mux.HandleFunc("GET /api/wifi/blocked", s.wifiBlocked)
	mux.HandleFunc("POST /api/devices/{mac}/wifi-block", s.wifiBlock)
	mux.HandleFunc("POST /api/devices/{mac}/wifi-unblock", s.wifiUnblock)
	mux.HandleFunc("GET /api/usage", s.usage)
	mux.HandleFunc("GET /api/history", s.history)
	mux.HandleFunc("GET /api/billing/settings", s.billingSettings)
	mux.HandleFunc("PUT /api/billing/settings", s.saveBillingSettings)
	mux.HandleFunc("GET /api/devices/{mac}/payments", s.devicePayments)
	mux.HandleFunc("POST /api/devices/{mac}/payments", s.requestPayment)
	mux.HandleFunc("POST /api/payments/{id}/refresh", s.refreshPayment)
	mux.HandleFunc("POST /api/payments/{id}/cancel", s.cancelPayment)
	mux.HandleFunc("POST /api/payments/{id}/mark-paid", s.markPaid)
	mux.HandleFunc("GET /api/members", s.listMembers)
	mux.HandleFunc("POST /api/members", s.createMember)
	mux.HandleFunc("PUT /api/members/{id}", s.updateMember)
	mux.HandleFunc("DELETE /api/members/{id}", s.deleteMember)
	mux.HandleFunc("PUT /api/devices/{mac}/member", s.assignDevice)
	mux.HandleFunc("GET /api/invoices", s.invoices)
	mux.HandleFunc("POST /api/billing/close", s.closeMonth)
	mux.HandleFunc("POST /api/billing/enforce", s.runEnforce)
	mux.HandleFunc("POST /api/blacklist/lift", s.liftBlacklist)
	mux.HandleFunc("GET /api/filter", s.filterState)
	mux.HandleFunc("POST /api/filter/toggle", s.filterToggle)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("GET /api/logs", s.logs)
	mux.HandleFunc("GET /api/config/env", s.envVars)
	// Static dashboard (least-specific; API patterns above take precedence).
	mux.Handle("GET /", webui.Handler())
	return s.logRequests(mux)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// logRequests records every API call except the dashboard's own log polling
// and static assets, which would drown the journal.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/logs" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		d := time.Since(start).Round(time.Millisecond)
		switch {
		case rec.status >= 500:
			s.svc.Logger.Errorf("HTTP %s %s -> %d (%s)", r.Method, r.URL.Path, rec.status, d)
		case rec.status >= 400:
			s.svc.Logger.Infof("HTTP %s %s -> %d (%s)", r.Method, r.URL.Path, rec.status, d)
		default:
			s.svc.Logger.Infof("HTTP %s %s -> %d (%s)", r.Method, r.URL.Path, rec.status, d)
		}
	})
}

func (s *Server) envVars(w http.ResponseWriter, _ *http.Request) {
	out := make([]EnvVar, 0, len(s.cfg.Env))
	for _, v := range s.cfg.Env {
		if v.Secret {
			v.Value = ""
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	if s.svc.Logs == nil {
		writeJSON(w, http.StatusOK, []port.LogEntry{})
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 500
	}
	writeJSON(w, http.StatusOK, s.svc.Logs.LogsSince(since, limit))
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": s.cfg.Version,
		"router":  s.cfg.BaseURL,
		"adapter": s.cfg.AdapterID,
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Test     bool   `json:"test"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	res, err := s.svc.Auth.Execute(r.Context(), usecase.AuthenticateInput{
		BaseURL:   s.cfg.BaseURL,
		AdapterID: s.cfg.AdapterID,
		Creds:     domain.Credentials{Username: req.Username, Password: req.Password},
		Opts:      s.cfg.Opts,
		Persist:   !req.Test, // never echoes the password back
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "stored": res.Stored, "adapter": res.AdapterID, "router_id": res.RouterID,
	})
}

func (s *Server) rename(w http.ResponseWriter, r *http.Request) {
	mac, err := domain.ParseMAC(r.PathValue("mac"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	var req struct {
		CustomName string `json:"custom_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if err := s.svc.Rename.Execute(r.Context(), usecase.RenameInput{BaseURL: s.cfg.BaseURL, MAC: mac, Name: req.CustomName}); err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"mac": mac.String(), "custom_name": req.CustomName})
}

func (s *Server) discover(w http.ResponseWriter, r *http.Request) {
	info, err := s.svc.Discover.Execute(r.Context(), usecase.DiscoverInput{BaseURL: s.cfg.BaseURL, Opts: s.cfg.Opts})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) devices(w http.ResponseWriter, r *http.Request) {
	views, err := s.svc.List.Execute(r.Context(), usecase.ListDevicesInput{
		BaseURL: s.cfg.BaseURL, AdapterID: s.cfg.AdapterID, Opts: s.cfg.Opts, Persist: true,
	})
	if err != nil {
		s.writeErr(w, err)
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
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) filterInput() usecase.MACFilterInput {
	return usecase.MACFilterInput{BaseURL: s.cfg.BaseURL, AdapterID: s.cfg.AdapterID, Opts: s.cfg.Opts}
}

func (s *Server) filterState(w http.ResponseWriter, r *http.Request) {
	if s.svc.Filter == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	enabled, err := s.svc.Filter.Enabled(r.Context(), s.filterInput())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": enabled})
}

func (s *Server) filterToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if s.svc.Filter == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	if err := s.svc.Filter.Set(r.Context(), s.filterInput(), req.Enabled); err != nil {
		s.writeErr(w, err)
		return
	}
	s.svc.Logger.Infof("MAC filter switched enabled=%v", req.Enabled)
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": req.Enabled})
}

func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	if s.svc.Meter == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	u, err := s.svc.Meter.Usage(r.Context(), s.cfg.BaseURL, r.URL.Query().Get("period"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) wifiBlocked(w http.ResponseWriter, r *http.Request) {
	if s.svc.WiFi == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	macs, err := s.svc.WiFi.Blocked(r.Context(), s.filterInput())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	out := make([]string, 0, len(macs))
	for _, m := range macs {
		out = append(out, m.String())
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) wifiBlock(w http.ResponseWriter, r *http.Request)   { s.wifiChange(w, r, true) }
func (s *Server) wifiUnblock(w http.ResponseWriter, r *http.Request) { s.wifiChange(w, r, false) }

func (s *Server) wifiChange(w http.ResponseWriter, r *http.Request, blocked bool) {
	mac, err := domain.ParseMAC(r.PathValue("mac"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if s.svc.WiFi == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	if err := s.svc.WiFi.Set(r.Context(), s.filterInput(), mac, blocked); err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mac": mac.String(), "wifi_blocked": blocked})
}

func (s *Server) block(w http.ResponseWriter, r *http.Request)   { s.change(w, r, true) }
func (s *Server) unblock(w http.ResponseWriter, r *http.Request) { s.change(w, r, false) }

func (s *Server) change(w http.ResponseWriter, r *http.Request, block bool) {
	mac, err := domain.ParseMAC(r.PathValue("mac"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	// A member's devices are cut and restored together (see Members.Siblings).
	targets := []domain.MAC{mac}
	if s.svc.Members != nil {
		if sib, err := s.svc.Members.Siblings(r.Context(), s.cfg.BaseURL, mac); err == nil {
			targets = sib
		}
	}
	done := make([]string, 0, len(targets))
	for _, m := range targets {
		in := usecase.ChangeAccessInput{
			BaseURL: s.cfg.BaseURL, AdapterID: s.cfg.AdapterID, MAC: m, Opts: s.cfg.Opts,
			Confirm: true, // an explicit POST is the confirmation
		}
		if block {
			err = s.svc.Block.Execute(r.Context(), in)
		} else {
			err = s.svc.Unblock.Execute(r.Context(), in)
		}
		if err != nil {
			if len(done) > 0 {
				s.changed()
			}
			s.writeErr(w, err)
			return
		}
		done = append(done, m.String())
	}
	s.changed()
	action := "unblocked"
	if block {
		action = "blocked"
	}
	writeJSON(w, http.StatusOK, map[string]any{"mac": mac.String(), "status": action, "devices": done})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func (s *Server) changed() {
	if s.svc.OnChange != nil {
		s.svc.OnChange()
	}
}

func (s *Server) writeErr(w http.ResponseWriter, err error) {
	status := errToStatus(err)
	// A refused input (bad phone, nothing to pay...) is not a fault of kergui:
	// only 5xx are errors, or the journal's error counter means nothing.
	if status >= 500 {
		s.svc.Logger.Errorf("%v", err)
	} else {
		s.svc.Logger.Infof("request refused: %v", err)
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

type nopLogger struct{}

func (nopLogger) Debugf(string, ...any) {}
func (nopLogger) Infof(string, ...any)  {}
func (nopLogger) Warnf(string, ...any)  {}
func (nopLogger) Errorf(string, ...any) {}

func errToStatus(err error) int {
	switch {
	case errors.Is(err, domain.ErrNotImplemented):
		return http.StatusNotImplemented // 501
	case errors.Is(err, domain.ErrAuthFailed):
		return http.StatusUnauthorized // 401: wrong or missing router credentials
	case errors.Is(err, domain.ErrSessionExpired):
		// The router ended the session (e.g. someone logged into its web UI) and
		// re-login did not help this time: temporary, not a credentials problem.
		return http.StatusServiceUnavailable // 503
	case errors.Is(err, domain.ErrInvalidInput):
		return http.StatusBadRequest // 400
	case errors.Is(err, domain.ErrNothingToPay):
		return http.StatusConflict // 409
	case errors.Is(err, domain.ErrPaymentUnavailable):
		return http.StatusServiceUnavailable // 503
	case errors.Is(err, domain.ErrPaymentNotFound), errors.Is(err, domain.ErrMemberNotFound):
		return http.StatusNotFound // 404
	case errors.Is(err, domain.ErrInvalidMAC):
		return http.StatusBadRequest // 400
	case errors.Is(err, domain.ErrDeviceNotFound):
		return http.StatusNotFound // 404
	case errors.Is(err, domain.ErrUnsupportedModel), errors.Is(err, domain.ErrUnsupportedFirmware):
		return http.StatusUnprocessableEntity // 422
	case errors.Is(err, domain.ErrRouterUnreachable):
		return http.StatusBadGateway // 502
	default:
		return http.StatusInternalServerError // 500
	}
}
