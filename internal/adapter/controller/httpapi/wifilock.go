package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// The closed Wi-Fi (RFC-007 D4, D8, #107): the routes the phone's core serves
// and, with the same paths, the relay (#109).
//
//	GET  /api/wifi/lock          the box's state, the intent, the places, the window
//	POST /api/wifi/lock/close    {"preview", "visitor_aps", "visitors_closed"}: Max, the preview free
//	POST /api/wifi/lock/open     free, even with a lapsed pass
//	POST /api/wifi/lock/window   {"action": "open" | "extend" | "close"}: open and extend Max
//
// « Refermer maintenant » (window "close") is not gated: it only does early
// what the deadline does anyway, for free, a few minutes later. Neither is the
// re-close at the deadline, which runs on every device listing (TickIfDue).

// wifiAPView is one access point as the box reports it.
type wifiAPView struct {
	ID       string `json:"id"`
	Band     string `json:"band"`
	Name     string `json:"name"`
	Mode     string `json:"mode"`
	Visitors bool   `json:"visitors"`
}

// wifiWindowView is the add window; until is "" when none runs.
type wifiWindowView struct {
	Until       string `json:"until"`
	MinutesLeft int    `json:"minutes_left"`
	CanExtend   bool   `json:"can_extend"`
}

type wifiLockStatusView struct {
	// State is what the button shows: open, closed, window, busy.
	State          string         `json:"state"`
	APs            []wifiAPView   `json:"aps"`
	HouseholdAPs   []string       `json:"household_aps"`
	VisitorAPs     []string       `json:"visitor_aps"`
	VisitorsClosed bool           `json:"visitors_closed"`
	Window         wifiWindowView `json:"window"`
	Allowed        int            `json:"allowed"`
	Places         int            `json:"places"`
	PlacesKnown    bool           `json:"places_known"`
	FoundOpen      bool           `json:"found_open"`
	// Intended: the household closed its Wi-Fi (the stored intent).
	Intended bool     `json:"intended"`
	Since    string   `json:"since"`
	Cut      []string `json:"cut"`
}

type wifiLockFullView struct {
	Kind  string `json:"kind"`
	Limit int    `json:"limit"`
	Used  int    `json:"used"`
}

type wifiLockPreviewView struct {
	HouseholdAPs   []string `json:"household_aps"`
	VisitorAPs     []string `json:"visitor_aps"`
	ClosedAPs      []string `json:"closed_aps"`
	VisitorsClosed bool     `json:"visitors_closed"`
	Devices        []string `json:"devices"`
	Always         []string `json:"always"`
	// Relays are the relays whose address is known; none while the
	// household has one is « relais inconnu ».
	Relays      []string          `json:"relays"`
	Self        string            `json:"self"`
	SelfPrivate bool              `json:"self_private"`
	Places      int               `json:"places"`
	PlacesKnown bool              `json:"places_known"`
	Full        *wifiLockFullView `json:"full"`
	// Closed: the box was closed (false for a preview).
	Closed bool `json:"closed"`
}

func macList(ms []domain.MAC) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		if !m.IsZero() {
			out = append(out, m.String())
		}
	}
	return out
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func windowView(w usecase.WiFiWindow) wifiWindowView {
	return wifiWindowView{Until: stamp(w.Until), MinutesLeft: w.MinutesLeft, CanExtend: w.CanExtend}
}

func previewView(p usecase.WiFiLockPreview) wifiLockPreviewView {
	v := wifiLockPreviewView{
		HouseholdAPs: orEmpty(p.HouseholdAPs), VisitorAPs: orEmpty(p.VisitorAPs), ClosedAPs: orEmpty(p.ClosedAPs),
		VisitorsClosed: p.VisitorsClosed, Devices: macList(p.Devices), Always: macList(p.Always),
		Relays: macList(p.Relays), SelfPrivate: p.SelfPrivate, Places: p.Places, PlacesKnown: p.PlacesKnown,
	}
	if !p.Self.IsZero() {
		v.Self = p.Self.String()
	}
	if p.Full != nil {
		v.Full = &wifiLockFullView{Kind: p.Full.Kind, Limit: p.Full.Limit, Used: p.Full.Used}
	}
	return v
}

// decodeOptional reads an optional JSON body into v: an empty body keeps v.
func decodeOptional(w http.ResponseWriter, r *http.Request, v any) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

func (s *Server) wifiLockStatus(w http.ResponseWriter, r *http.Request) {
	if s.svc.WiFiLock == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	// The app back in the foreground reads this first: an add window past
	// its deadline is closed again before the button shows anything.
	s.wifiLockTick(r.Context())
	st, err := s.svc.WiFiLock.Status(r.Context(), s.filterInput())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	v := wifiLockStatusView{
		State: st.State, APs: []wifiAPView{}, HouseholdAPs: orEmpty(st.HouseholdAPs), VisitorAPs: orEmpty(st.VisitorAPs),
		VisitorsClosed: st.VisitorsClosed, Window: windowView(st.Window), Allowed: st.Allowed,
		Places: st.Places, PlacesKnown: st.PlacesKnown, FoundOpen: st.FoundOpen,
		Intended: st.Closed, Since: stamp(st.Since), Cut: macList(st.Cut),
	}
	for _, ap := range st.APs {
		v.APs = append(v.APs, wifiAPView{ID: ap.ID, Band: ap.Band, Name: ap.Name, Mode: ap.Mode, Visitors: ap.Visitors})
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) wifiLockClose(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Preview bool `json:"preview"`
		// VisitorAPs: the household's answer to « Ce réseau est-il pour vos
		// visiteurs ? »; absent keeps the answer stored.
		VisitorAPs     []string `json:"visitor_aps"`
		VisitorsClosed bool     `json:"visitors_closed"`
	}
	if err := decodeOptional(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if s.svc.WiFiLock == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	in := usecase.WiFiCloseInput{MACFilterInput: s.filterInput(), VisitorAPs: req.VisitorAPs, VisitorsClosed: req.VisitorsClosed}
	if req.Preview {
		p, err := s.svc.WiFiLock.Preview(r.Context(), in)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, previewView(p))
		return
	}
	if !s.entitled(w, r, entitlement.FeatureWiFiLock) {
		return
	}
	p, err := s.svc.WiFiLock.Close(r.Context(), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.changed()
	v := previewView(p)
	v.Closed = true
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) wifiLockOpen(w http.ResponseWriter, r *http.Request) {
	if s.svc.WiFiLock == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	if !s.entitled(w, r, entitlement.FeatureWiFiUnlock) {
		return
	}
	res, err := s.svc.WiFiLock.Open(r.Context(), s.filterInput())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.changed()
	writeJSON(w, http.StatusOK, map[string]any{"open": true, "restoring": macList(res.Restoring)})
}

func (s *Server) wifiLockWindow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if err := decodeOptional(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if s.svc.WiFiLock == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	var (
		win usecase.WiFiWindow
		err error
	)
	switch req.Action {
	case "open", "extend":
		if !s.entitled(w, r, entitlement.FeatureWiFiLock) {
			return
		}
		if req.Action == "open" {
			win, err = s.svc.WiFiLock.OpenWindow(r.Context(), s.filterInput())
		} else {
			win, err = s.svc.WiFiLock.ExtendWindow(r.Context(), s.filterInput())
		}
	case "close":
		err = s.svc.WiFiLock.CloseWindow(r.Context(), s.filterInput())
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `action must be "open", "extend" or "close"`})
		return
	}
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.changed()
	writeJSON(w, http.StatusOK, map[string]any{"action": req.Action, "window": windowView(win)})
}

// wifiLockTick closes the Wi-Fi again once an add window is past its
// deadline. It never fails the call it runs in: its outcome is in the journal.
func (s *Server) wifiLockTick(ctx context.Context) {
	if s.svc.WiFiLock == nil {
		return
	}
	if _, err := s.svc.WiFiLock.TickIfDue(ctx, s.filterInput()); err != nil {
		s.svc.Logger.Warnf("Le Wi-Fi n'a pas pu être refermé pour l'instant : %v", err)
	}
}

// trustThroughLock is a block or an unblock while the closed Wi-Fi is
// involved (usecase.WiFiLock.Tracks, or « Le déconnecter aussi du Wi-Fi »
// asked): it goes through SetTrust, which writes the allow list too. False
// means the plain path.
func (s *Server) trustThroughLock(ctx context.Context, mac domain.MAC, cutWiFi bool) bool {
	if s.svc.WiFiLock == nil {
		return false
	}
	if cutWiFi {
		return true
	}
	on, err := s.svc.WiFiLock.Tracks(ctx, s.cfg.BaseURL, mac)
	if err != nil {
		s.svc.Logger.Warnf("état du Wi-Fi fermé illisible : %v", err)
	}
	return on
}
