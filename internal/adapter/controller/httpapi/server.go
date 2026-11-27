// Package httpapi is the REST delivery adapter (Milestone 2). It exposes the same
// read-only use cases as the CLI over JSON, plus block/unblock endpoints that
// return 501 until a model's write path is verified on real hardware. It never
// returns a router password to clients (brief §12): the response DTOs carry none.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
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
	// Labels backs /api/labels (#95): the names and notes the owner's phone
	// and the relay exchange. Optional: 501 without it.
	Labels *usecase.Labels
	Filter *usecase.MACFilter
	WiFi   *usecase.WiFiAccess
	Meter  *usecase.Metering
	// Observe backs /api/observations and /api/traffic, the raw per-station
	// view the hub relay signs into its snapshots (RFC-005 H2). Optional.
	Observe  *usecase.ObserveHosts
	Billing  *usecase.Billing
	Members  *usecase.Members
	Enforce  *usecase.Enforcement
	Identity *usecase.Identity
	// AutoReject backs /api/autoreject (RFC-006, Max). Optional.
	AutoReject *usecase.AutoReject
	// Purge removes the stale rules kergui wrote (RFC-007 D6), once a day
	// after a device list: that is where the box is read anyway. Optional.
	Purge  *usecase.PurgeStaleRules
	Logger port.Logger    // optional
	Logs   port.LogReader // optional; backs GET /api/logs
	// OnChange is called (non-blocking) after anything that changes who is cut
	// or what they owe, so the captive portal updates at once. Optional.
	OnChange func()
	// Gate refuses the paid writes the current entitlement does not cover
	// (RFC-006). nil gates nothing: the CLI and `kergui serve` are free and
	// ungated; only the mobile core sets one.
	Gate Gate
}

// Gate decides whether a write may run. Check returns nil, or an error
// matching entitlement.ErrNotEntitled (answered 403 with code
// "not_entitled"), or any other error (mapped as usual, e.g. 502 when the
// box could not be asked for its serial number). It is asked for the
// give-back writes too (unblock, restore Wi-Fi access, a network on), which
// the tier table makes free, so they always pass.
type Gate interface {
	Check(ctx context.Context, f entitlement.Feature) error
}

// NotEntitledCode is the stable "code" of a 403 refusing a paid action; the
// app maps it to its lock and neutral message (mobile/src/core/client.ts).
const NotEntitledCode = "not_entitled"

// entitled runs the gate for the features a write needs, and answers the
// refusal itself. True means go on.
func (s *Server) entitled(w http.ResponseWriter, r *http.Request, fs ...entitlement.Feature) bool {
	if s.svc.Gate == nil {
		return true
	}
	for _, f := range fs {
		if err := s.svc.Gate.Check(r.Context(), f); err != nil {
			s.writeErr(w, err)
			return false
		}
	}
	return true
}

// Config is the server's fixed router target.
type Config struct {
	BaseURL   string
	AdapterID string
	Opts      port.RouterOptions
	Version   string
	Env       []EnvVar // server environment, shown in the settings panel
	// WebhookSecret signs NabooPay's payment webhooks; "" turns the webhook off.
	WebhookSecret string
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
	mux.HandleFunc("GET /api/observations", s.observations)
	mux.HandleFunc("GET /api/traffic", s.traffic)
	mux.HandleFunc("POST /api/devices/{mac}/block", s.block)
	mux.HandleFunc("POST /api/devices/{mac}/unblock", s.unblock)
	mux.HandleFunc("PATCH /api/devices/{mac}", s.rename)
	mux.HandleFunc("GET /api/labels", s.labels)
	mux.HandleFunc("POST /api/labels", s.applyLabels)
	mux.HandleFunc("GET /api/wifi/blocked", s.wifiBlocked)
	mux.HandleFunc("GET /api/blocked", s.blockedDevices)
	mux.HandleFunc("POST /api/devices/{mac}/wifi-block", s.wifiBlock)
	mux.HandleFunc("POST /api/devices/{mac}/wifi-unblock", s.wifiUnblock)
	mux.HandleFunc("GET /api/wifi/ssids", s.wifiSSIDs)
	mux.HandleFunc("PATCH /api/wifi/ssids/{id}", s.updateWiFiSSID)
	mux.HandleFunc("GET /api/autoreject", s.autoRejectStatus)
	mux.HandleFunc("PUT /api/autoreject", s.setAutoReject)
	mux.HandleFunc("POST /api/autoreject/run", s.runAutoReject)
	mux.HandleFunc("PUT /api/autoreject/enforcer", s.setAutoRejectEnforcer)
	mux.HandleFunc("POST /api/devices/{mac}/accept", s.acceptDevice)
	mux.HandleFunc("GET /api/usage", s.usage)
	mux.HandleFunc("POST /api/usage/sample", s.sampleUsage)
	mux.HandleFunc("GET /api/usage/devices/{mac}", s.deviceUsage)
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
	mux.HandleFunc("POST /api/filter/setup", s.filterSetup)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/box/first-password", s.firstPassword)
	mux.HandleFunc("GET /api/logs", s.logs)
	mux.HandleFunc("GET /api/config/env", s.envVars)
	mountPaymentReturn(mux)
	mux.HandleFunc("POST /paiement/webhook", s.paymentWebhook)
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

// firstPassword sets the admin password a new box demands at the first
// sign-in (#92), after /api/login answered 409 password_change_required:
// {username, password (the label's), newPassword}. On success the NEW
// password is stored, as /api/login stores one; the answer is /api/login's.
// It never echoes a password.
func (s *Server) firstPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	res, err := s.svc.Auth.FirstPassword(r.Context(), usecase.FirstPasswordInput{
		BaseURL:     s.cfg.BaseURL,
		AdapterID:   s.cfg.AdapterID,
		Creds:       domain.Credentials{Username: req.Username, Password: req.Password},
		NewPassword: req.NewPassword,
		Opts:        s.cfg.Opts,
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
	if req.CustomName != "" {
		s.autoAccept(r, mac) // a device the household names is one they know
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
	s.dailyPurge(r.Context())
	s.keepFilterOn(r.Context())
	writeJSON(w, http.StatusOK, views)
}

// keepFilterOn switches the box's filter back on when it is off while kergui's
// blocks are on the box (#115): the app has no switch for it, so whoever lists
// the devices (the phone, the relay) keeps it on. It never fails the list.
func (s *Server) keepFilterOn(ctx context.Context) {
	if s.svc.Filter == nil {
		return
	}
	if _, err := s.svc.Filter.KeepOnIfDue(ctx, s.filterInput()); err != nil {
		s.svc.Logger.Warnf("Vérification des blocages de la box impossible pour l'instant : %v", err)
	}
}

// dailyPurge runs the purge of stale rules when it is due. Its outcome is in
// the journal; a purge that fails never fails the device list.
func (s *Server) dailyPurge(ctx context.Context) {
	if s.svc.Purge == nil {
		return
	}
	// Each removal or addition is its own journal line (usecase.PurgeStaleRules).
	if _, _, err := s.svc.Purge.RunIfDue(ctx, s.filterInput()); err != nil {
		s.svc.Logger.Warnf("Nettoyage des règles de la box impossible pour l'instant : %v", err)
	}
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

// observations serves one poll of everything the router says about each
// station. It is read-only, and it is the only endpoint that exposes DHCP
// leases and radio capabilities: device recognition needs them, a UI does not.
func (s *Server) observations(w http.ResponseWriter, r *http.Request) {
	if s.svc.Observe == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	obs, err := s.svc.Observe.Observations(r.Context(), s.filterInput())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	// The counters come from the same Wi-Fi client table, so merging them
	// here costs one extra read and saves the caller a second poll that
	// would not line up in time with this one.
	traffic, err := s.svc.Observe.Traffic(r.Context(), s.filterInput())
	if err != nil {
		s.svc.Logger.Warnf("observations: traffic counters unavailable: %v", err)
	}
	writeJSON(w, http.StatusOK, port.NewObservationViews(obs, traffic))
}

// traffic serves the cumulative byte counters per station, untouched. The
// deltas are computed by whoever holds two samples (domain.TrafficDelta).
func (s *Server) traffic(w http.ResponseWriter, r *http.Request) {
	if s.svc.Observe == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	counters, err := s.svc.Observe.Traffic(r.Context(), s.filterInput())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	out := make([]trafficRow, 0, len(counters))
	for mac, c := range counters {
		out = append(out, trafficRow{
			MAC:         mac.String(),
			TrafficView: port.TrafficView{Rx: c.Rx, Tx: c.Tx, LinkSecs: c.LinkSecs},
		})
	}
	// A map iterates in a random order; a stable list is comparable between
	// two polls and diffable in a test.
	sort.Slice(out, func(i, j int) bool { return out[i].MAC < out[j].MAC })
	writeJSON(w, http.StatusOK, out)
}

// trafficRow is one station's counters, keyed by its MAC.
type trafficRow struct {
	MAC string `json:"mac"`
	port.TrafficView
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

// filterSetup is the setup step after the first sign-in to a box: the MAC
// filter on in blocklist mode (an empty list cuts nobody), never allowlist.
// Not gated: it cuts no one by itself. A box without the switch answers
// "unsupported", not an error.
func (s *Server) filterSetup(w http.ResponseWriter, r *http.Request) {
	if s.svc.Filter == nil {
		writeJSON(w, http.StatusOK, map[string]string{"result": string(usecase.SetupUnsupported)})
		return
	}
	res, err := s.svc.Filter.Setup(r.Context(), s.filterInput())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": string(res)})
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
	// The MAC filter keeps every device not on its list off the Wi-Fi:
	// turning it on cuts Wi-Fi access, turning it off gives it back.
	feature := entitlement.FeatureRestoreWiFiAccess
	if req.Enabled {
		feature = entitlement.FeatureCutWiFiAccess
	}
	if !s.entitled(w, r, feature) {
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
	period := r.URL.Query().Get("period")
	// The current month is free; picking another one is the Max monthly
	// filter (RFC-006).
	if period != "" && period != s.svc.Meter.Period() && !s.entitled(w, r, entitlement.FeatureMonthlyFilter) {
		return
	}
	u, err := s.svc.Meter.Usage(r.Context(), s.cfg.BaseURL, period)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// deviceHistoryMonths is the device curve's default window.
const deviceHistoryMonths = 6

// deviceUsage is one device's consumption over the last ?months= months
// (6 by default, at most the 13 kept), oldest first, a month with nothing
// measured at 0. It is the Max consumption history.
func (s *Server) deviceUsage(w http.ResponseWriter, r *http.Request) {
	if s.svc.Meter == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	mac, err := domain.ParseMAC(r.PathValue("mac"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	months := deviceHistoryMonths
	if v := r.URL.Query().Get("months"); v != "" {
		if months, err = strconv.Atoi(v); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "months must be a number"})
			return
		}
	}
	if !s.entitled(w, r, entitlement.FeatureConsumptionHistory) {
		return
	}
	u, err := s.svc.Meter.DeviceHistory(r.Context(), s.cfg.BaseURL, mac, months)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// sampleUsage reads the box's byte counters once and adds what each device
// consumed since the previous sample. `kergui serve` samples on its own
// timer; the phone has no server, so the app calls this on each refresh and
// from its watch service. 501 when the box model has no counters.
func (s *Server) sampleUsage(w http.ResponseWriter, r *http.Request) {
	if s.svc.Meter == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	res, err := s.svc.Meter.Sample(r.Context(), s.filterInput())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": res.Devices, "bytes": res.Bytes, "baseline": res.Baseline})
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

// blockedDevice is one device the box's deny lists hold (GET /api/blocked).
type blockedDevice struct {
	MAC      string `json:"mac"`
	Name     string `json:"name"`
	LastSeen string `json:"last_seen"` // RFC 3339, "" when the inventory does not know it
	Present  bool   `json:"present"`
	Firewall bool   `json:"firewall"`
	WiFi     bool   `json:"wifi"`
	ByKergui bool   `json:"by_kergui"`
}

// blockedDevices lists every device the box's deny lists hold, absent ones
// included: what the app offers to remove when the list is full (RFC-007 D6).
func (s *Server) blockedDevices(w http.ResponseWriter, r *http.Request) {
	if s.svc.Purge == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	list, err := s.svc.Purge.Blocked(r.Context(), s.filterInput())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	out := make([]blockedDevice, 0, len(list))
	for _, b := range list {
		seen := ""
		if !b.LastSeen.IsZero() {
			seen = b.LastSeen.UTC().Format(time.RFC3339)
		}
		out = append(out, blockedDevice{MAC: b.MAC.String(), Name: b.Name, LastSeen: seen,
			Present: b.Present, Firewall: b.Firewall, WiFi: b.WiFi, ByKergui: b.ByKergui})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
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
	feature := entitlement.FeatureRestoreWiFiAccess
	if blocked {
		feature = entitlement.FeatureCutWiFiAccess
	}
	if !s.entitled(w, r, feature) {
		return
	}
	if err := s.svc.WiFi.Set(r.Context(), s.filterInput(), mac, blocked); err != nil {
		s.writeErr(w, err)
		return
	}
	if !blocked {
		s.autoAccept(r, mac) // given back: auto-reject must not refuse it again
	}
	writeJSON(w, http.StatusOK, map[string]any{"mac": mac.String(), "wifi_blocked": blocked})
}

func (s *Server) wifiSSIDs(w http.ResponseWriter, r *http.Request) {
	if s.svc.WiFi == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	ssids, err := s.svc.WiFi.SSIDs(r.Context(), s.filterInput())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ssids)
}

// updateWiFiSSID applies a partial change ({"enabled", "name", "password",
// "hidden"}, each optional). The password is never echoed back.
func (s *Server) updateWiFiSSID(w http.ResponseWriter, r *http.Request) {
	var change domain.WiFiSSIDChange
	if err := json.NewDecoder(r.Body).Decode(&change); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if s.svc.WiFi == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	if !s.entitled(w, r, ssidFeatures(change)...) {
		return
	}
	id := r.PathValue("id")
	if err := s.svc.WiFi.SetSSID(r.Context(), s.filterInput(), id, change); err != nil {
		s.writeErr(w, err)
		return
	}
	s.svc.Logger.Infof("Wi-Fi %s changed (enabled=%s name=%s hidden=%s password changed=%v)",
		id, optStr(change.Enabled), optStr(change.Name), optStr(change.Hidden), change.Password != nil)
	ssids, err := s.svc.WiFi.SSIDs(r.Context(), s.filterInput())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	for _, ssid := range ssids {
		if ssid.ID == id {
			writeJSON(w, http.StatusOK, ssid)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

// ssidFeatures is what an SSID change needs: turning a network on is free
// forever; turning it off, renaming or hiding it is SSID management, a new
// password its own feature (both Max).
func ssidFeatures(c domain.WiFiSSIDChange) []entitlement.Feature {
	var fs []entitlement.Feature
	if c.Enabled != nil && *c.Enabled {
		fs = append(fs, entitlement.FeatureNetworkOn)
	}
	if (c.Enabled != nil && !*c.Enabled) || c.Name != nil || c.Hidden != nil {
		fs = append(fs, entitlement.FeatureSSIDManage)
	}
	if c.Password != nil {
		fs = append(fs, entitlement.FeatureWiFiPassword)
	}
	return fs
}

// optStr renders an optional field for the journal ("-" when unchanged).
func optStr[T any](v *T) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprint(*v)
}

func (s *Server) block(w http.ResponseWriter, r *http.Request)   { s.change(w, r, true) }
func (s *Server) unblock(w http.ResponseWriter, r *http.Request) { s.change(w, r, false) }

func (s *Server) change(w http.ResponseWriter, r *http.Request, block bool) {
	mac, err := domain.ParseMAC(r.PathValue("mac"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	feature := entitlement.FeatureUnblock
	if block {
		feature = entitlement.FeatureBlock
	}
	if !s.entitled(w, r, feature) {
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
	var ne *entitlement.NotEntitledError
	if errors.As(err, &ne) {
		// A stable code the app maps to its lock, not the message: the
		// sentence the household reads is the app's, and neutral.
		writeJSON(w, status, map[string]string{
			"error": err.Error(), "code": NotEntitledCode,
			"feature": string(ne.Feature), "required": string(ne.Required),
		})
		return
	}
	var full *domain.AccessListFullError
	if errors.As(err, &full) {
		// The app says « La box accepte N appareils au plus » from these,
		// not the box's raw answer.
		writeJSON(w, status, map[string]any{
			"error": err.Error(), "code": AccessListFullCode,
			"kind": full.Kind, "limit": full.Limit, "used": full.Used,
		})
		return
	}
	var lock *domain.SelfLockoutError
	if errors.As(err, &lock) {
		// The app says why in its own words (« C'est ce téléphone… »),
		// from the reason, not the message.
		writeJSON(w, status, map[string]string{
			"error": err.Error(), "code": SelfLockoutCode, "reason": lock.Reason,
		})
		return
	}
	if code := errCode(err); code != "" {
		writeJSON(w, status, map[string]string{"error": err.Error(), "code": code})
		return
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// Stable codes for the errors the app answers with a screen of its own,
// rather than with the message.
const (
	// PasswordChangeRequiredCode (409): the box accepted the login but demands
	// a new admin password first; POST /api/box/first-password follows.
	PasswordChangeRequiredCode = "password_change_required"
	// WeakPasswordCode (400): the new password breaks the box's rule.
	WeakPasswordCode = "weak_password"
	// PasswordRejectedCode (422): the box refused the new password; "error"
	// carries its message.
	PasswordRejectedCode = "password_rejected"
	// AuthFailedCode (401): wrong username or password.
	AuthFailedCode = "auth_failed"
	// SessionHeldCode (503): another client holds the box's single session
	// and this core yields to it (a relay, to the owner at home, #94).
	SessionHeldCode = "session_held"
	// AccessListFullCode (409): the box refused a rule, its list is full
	// (RFC-007 D6); "kind" (firewall | wifi), "limit" and "used" count
	// devices, limit 0 when unknown.
	AccessListFullCode = "access_list_full"
	// SelfLockoutCode (409): the write would cut the device running Kergui
	// off the box (UX audit 2026-11-26, C1); "reason" is self_device,
	// self_network or last_network (domain.SelfLockout*).
	SelfLockoutCode = "self_lockout"
)

func errCode(err error) string {
	switch {
	case errors.Is(err, domain.ErrPasswordChangeRequired):
		return PasswordChangeRequiredCode
	case errors.Is(err, domain.ErrWeakPassword):
		return WeakPasswordCode
	case errors.Is(err, domain.ErrPasswordRejected):
		return PasswordRejectedCode
	case errors.Is(err, domain.ErrAuthFailed):
		return AuthFailedCode
	case errors.Is(err, domain.ErrSessionHeld):
		return SessionHeldCode
	}
	return ""
}

type nopLogger struct{}

func (nopLogger) Debugf(string, ...any) {}
func (nopLogger) Infof(string, ...any)  {}
func (nopLogger) Warnf(string, ...any)  {}
func (nopLogger) Errorf(string, ...any) {}

func errToStatus(err error) int {
	switch {
	case errors.Is(err, entitlement.ErrNotEntitled):
		return http.StatusForbidden // 403: the entitlement does not cover this action
	case errors.Is(err, domain.ErrNotImplemented):
		return http.StatusNotImplemented // 501
	case errors.Is(err, domain.ErrAccessListFull):
		return http.StatusConflict // 409: the box's list is full (RFC-007 D6)
	case errors.Is(err, domain.ErrSelfLockout):
		return http.StatusConflict // 409: it would cut the device running Kergui off the box
	case errors.Is(err, domain.ErrPasswordChangeRequired):
		return http.StatusConflict // 409: signed in, but the box demands a new password first
	case errors.Is(err, domain.ErrPasswordRejected):
		return http.StatusUnprocessableEntity // 422: the box refused the new password
	case errors.Is(err, domain.ErrAuthFailed):
		return http.StatusUnauthorized // 401: wrong or missing router credentials
	case errors.Is(err, domain.ErrSessionHeld):
		// Someone else holds the box's single session and this caller yields
		// to them (a relay, to the owner at home, #94): try again later.
		return http.StatusServiceUnavailable // 503
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
