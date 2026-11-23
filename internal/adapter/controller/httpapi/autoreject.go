package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// Auto-reject (RFC-006, Max). The setting lives in the core, so the app and
// the watch service read the same one: the app turns it on and off, the
// watch service calls POST /api/autoreject/run on each round.

type autoRejectDeviceView struct {
	MAC       string `json:"mac"`
	State     string `json:"state"`
	FirstSeen string `json:"first_seen"`
	Deadline  string `json:"deadline"`
}

type autoRejectView struct {
	Enabled      bool                   `json:"enabled"`
	Since        string                 `json:"since"`
	DelayMinutes int                    `json:"delay_minutes"`
	Devices      []autoRejectDeviceView `json:"devices"`
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func newAutoRejectView(st usecase.AutoRejectStatus) autoRejectView {
	v := autoRejectView{
		Enabled: st.Settings.Enabled, Since: rfc3339(st.Settings.Since),
		DelayMinutes: int(domain.AutoRejectDelay / time.Minute),
		Devices:      make([]autoRejectDeviceView, 0, len(st.Devices)),
	}
	for _, d := range st.Devices {
		v.Devices = append(v.Devices, autoRejectDeviceView{
			MAC: d.MAC.String(), State: string(d.State), FirstSeen: rfc3339(d.FirstSeen), Deadline: rfc3339(d.Deadline()),
		})
	}
	return v
}

func (s *Server) autoRejectStatus(w http.ResponseWriter, r *http.Request) {
	if s.svc.AutoReject == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	st, err := s.svc.AutoReject.Status(r.Context(), s.cfg.BaseURL)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newAutoRejectView(st))
}

// setAutoReject turns the rule on (Max: every device present is accepted)
// or off (free, whatever the pass).
func (s *Server) setAutoReject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if s.svc.AutoReject == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	var (
		st  usecase.AutoRejectStatus
		err error
	)
	if req.Enabled {
		if !s.entitled(w, r, entitlement.FeatureAutoReject) {
			return
		}
		st, err = s.svc.AutoReject.Enable(r.Context(), s.filterInput())
	} else if err = s.svc.AutoReject.Disable(r.Context(), s.cfg.BaseURL); err == nil {
		st, err = s.svc.AutoReject.Status(r.Context(), s.cfg.BaseURL)
	}
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newAutoRejectView(st))
}

type refusedView struct {
	MAC  string `json:"mac"`
	Name string `json:"name"`
}

// Who enforces the rule this round (#36).
const (
	enforcerPhone = "phone"
	enforcerRelay = "relay"
)

// runAutoRejectRequest is the optional body of a round: when the app knows,
// from the hub, when the household's relay agent last sent a heartbeat.
type runAutoRejectRequest struct {
	RelayHeartbeatAt *time.Time `json:"relayHeartbeatAt"`
}

// runAutoReject is one round of the rule. A pass that no longer covers it
// stops it without an error: the round answers entitled=false and touches
// nothing, so the watch service keeps going quietly.
func (s *Server) runAutoReject(w http.ResponseWriter, r *http.Request) {
	if s.svc.AutoReject == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	st, err := s.svc.AutoReject.Status(r.Context(), s.cfg.BaseURL)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	var req runAutoRejectRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			s.writeErr(w, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err))
			return
		}
	}
	out := map[string]any{"enabled": st.Settings.Enabled, "entitled": true, "refused": []refusedView{}, "pending": 0,
		"enforcer": enforcerPhone}
	if !st.Settings.Enabled {
		writeJSON(w, http.StatusOK, out)
		return
	}
	// One enforcer at a time (#36): while the household's relay agent has
	// heartbeated the hub in the last two minutes, it enforces the rule and
	// the phone stays off the box, so the two never fight over its single
	// admin session. No pass is needed to stand down. The heartbeat is the
	// round's own, or the hub's last answer the app handed over (PUT
	// /api/autoreject/enforcer), whichever is the later.
	last := s.svc.AutoReject.LastRelayHeartbeat()
	if req.RelayHeartbeatAt != nil && req.RelayHeartbeatAt.After(last) {
		last = *req.RelayHeartbeatAt
	}
	if s.svc.AutoReject.RelayEnforces(last) {
		out["enforcer"] = enforcerRelay
		writeJSON(w, http.StatusOK, out)
		return
	}
	if s.svc.Gate != nil {
		if err := s.svc.Gate.Check(r.Context(), entitlement.FeatureAutoReject); err != nil {
			if !errors.Is(err, entitlement.ErrNotEntitled) {
				s.writeErr(w, err)
				return
			}
			out["entitled"] = false
			writeJSON(w, http.StatusOK, out)
			return
		}
	}
	run, err := s.svc.AutoReject.Enforce(r.Context(), s.filterInput())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	refused := make([]refusedView, 0, len(run.Refused))
	for _, d := range run.Refused {
		refused = append(refused, refusedView{MAC: d.MAC.String(), Name: d.DisplayName()})
	}
	out["refused"], out["pending"] = refused, run.Pending
	writeJSON(w, http.StatusOK, out)
}

// enforcerAnswer is the hub's GET /api/v1/tenants/{id}/router/auto-reject
// answer, as the app hands it over.
type enforcerAnswer struct {
	Enforcer                 string   `json:"enforcer"`
	RelayHeartbeatAgeSeconds *float64 `json:"relayHeartbeatAgeSeconds"`
}

// setAutoRejectEnforcer takes the hub's word on who enforces the rule (#36).
// The app asks the hub before its rounds and hands the answer here, so the
// Android watch service, which never calls the hub, goes by it too: "relay"
// stands every round down while the relay's heartbeat is under two minutes
// old on this core's clock, "phone" lets the next round act at once. A
// stale answer (the app closed, the hub out of reach) ages out by itself and
// the phone takes over, which is the safe side. It answers who enforces now.
func (s *Server) setAutoRejectEnforcer(w http.ResponseWriter, r *http.Request) {
	if s.svc.AutoReject == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	var req enforcerAnswer
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	switch {
	case req.Enforcer == enforcerPhone:
		s.svc.AutoReject.ForgetRelay()
	case req.Enforcer == enforcerRelay && req.RelayHeartbeatAgeSeconds != nil && *req.RelayHeartbeatAgeSeconds >= 0:
		s.svc.AutoReject.HeardRelay(time.Duration(*req.RelayHeartbeatAgeSeconds * float64(time.Second)))
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `enforcer must be "phone", or "relay" with relayHeartbeatAgeSeconds`})
		return
	}
	who := enforcerPhone
	if s.svc.AutoReject.RelayEnforces(s.svc.AutoReject.LastRelayHeartbeat()) {
		who = enforcerRelay
	}
	writeJSON(w, http.StatusOK, map[string]string{"enforcer": who})
}

// acceptDevice accepts a device under the rule: "Accepter" on its
// new-device notification or its sheet. Free: it only spares a device.
func (s *Server) acceptDevice(w http.ResponseWriter, r *http.Request) {
	mac, err := domain.ParseMAC(r.PathValue("mac"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if s.svc.AutoReject == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	if err := s.svc.AutoReject.Accept(r.Context(), s.cfg.BaseURL, mac); err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"mac": mac.String(), "state": string(domain.AutoRejectAccepted)})
}

// autoAccept accepts a device the household just renamed or gave its access
// back to. Best effort: the rename or the restore already happened.
func (s *Server) autoAccept(r *http.Request, mac domain.MAC) {
	if s.svc.AutoReject == nil {
		return
	}
	if err := s.svc.AutoReject.Accept(r.Context(), s.cfg.BaseURL, mac); err != nil {
		s.svc.Logger.Warnf("auto : %s non marqué accepté : %v", mac, err)
	}
}
