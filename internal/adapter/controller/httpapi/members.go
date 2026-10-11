package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

type memberDTO struct {
	domain.Member
	MonthBytes  uint64          `json:"month_bytes"`  // current month, all their devices
	LastInvoice *domain.Payment `json:"last_invoice"` // previous month's invoice, if any
}

func (s *Server) membersReady(w http.ResponseWriter) bool {
	if s.svc.Members == nil || s.svc.Billing == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return false
	}
	return true
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	if !s.membersReady(w) {
		return
	}
	ctx := r.Context()
	ms, err := s.svc.Members.List(ctx, s.cfg.BaseURL)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	usage := map[string]uint64{}
	if s.svc.Meter != nil {
		us, err := s.svc.Meter.Usage(ctx, s.cfg.BaseURL, "")
		if err != nil {
			s.writeErr(w, err)
			return
		}
		for _, u := range us {
			usage[u.MAC.String()] = u.Bytes
		}
	}
	invs, err := s.svc.Billing.Invoices(ctx, s.cfg.BaseURL, "")
	if err != nil {
		s.writeErr(w, err)
		return
	}
	lastInv := map[string]*domain.Payment{}
	for i := range invs {
		inv := &invs[i]
		if prev, ok := lastInv[inv.MemberID]; !ok || inv.CreatedAt.After(prev.CreatedAt) {
			lastInv[inv.MemberID] = inv
		}
	}
	out := make([]memberDTO, 0, len(ms))
	for _, m := range ms {
		d := memberDTO{Member: m, LastInvoice: lastInv[m.ID]}
		for _, mac := range m.MACs {
			d.MonthBytes += usage[mac.String()]
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, out)
}

type memberInput struct {
	Name      string            `json:"name"`
	Phone     string            `json:"phone"`
	Kind      domain.MemberKind `json:"kind"`
	Active    *bool             `json:"active"`
	Protected bool              `json:"protected"`
}

func (in memberInput) member() domain.Member {
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	return domain.Member{Name: in.Name, Phone: in.Phone, Kind: in.Kind, Active: active, Protected: in.Protected}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return false
	}
	return true
}

func (s *Server) createMember(w http.ResponseWriter, r *http.Request) {
	var in memberInput
	if !s.membersReady(w) || !decode(w, r, &in) {
		return
	}
	m, err := s.svc.Members.Create(r.Context(), s.cfg.BaseURL, in.member())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) updateMember(w http.ResponseWriter, r *http.Request) {
	var in memberInput
	if !s.membersReady(w) || !decode(w, r, &in) {
		return
	}
	m := in.member()
	m.ID = r.PathValue("id")
	got, err := s.svc.Members.Update(r.Context(), s.cfg.BaseURL, m)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.enforceSoon()
	writeJSON(w, http.StatusOK, got)
}

func (s *Server) deleteMember(w http.ResponseWriter, r *http.Request) {
	if !s.membersReady(w) {
		return
	}
	if err := s.svc.Members.Delete(r.Context(), s.cfg.BaseURL, r.PathValue("id")); err != nil {
		s.writeErr(w, err)
		return
	}
	s.enforceSoon()
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) assignDevice(w http.ResponseWriter, r *http.Request) {
	if !s.membersReady(w) {
		return
	}
	mac, err := domain.ParseMAC(r.PathValue("mac"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	var in struct {
		MemberID string `json:"member_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.svc.Members.Assign(r.Context(), s.cfg.BaseURL, mac, in.MemberID); err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"mac": mac.String(), "member_id": in.MemberID})
}

func (s *Server) invoices(w http.ResponseWriter, r *http.Request) {
	if !s.billingReady(w) {
		return
	}
	invs, err := s.svc.Billing.Invoices(r.Context(), s.cfg.BaseURL, r.URL.Query().Get("period"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, invs)
}

func (s *Server) closeMonth(w http.ResponseWriter, r *http.Request) {
	if !s.billingReady(w) {
		return
	}
	var in struct {
		Period string `json:"period"`
	}
	if !decode(w, r, &in) {
		return
	}
	rep, err := s.svc.Billing.CloseMonth(r.Context(), s.cfg.BaseURL, in.Period)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.changed()
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) markPaid(w http.ResponseWriter, r *http.Request) {
	if !s.billingReady(w) {
		return
	}
	p, err := s.svc.Billing.MarkPaid(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.enforceSoon()
	s.changed()
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) runEnforce(w http.ResponseWriter, r *http.Request) {
	if s.svc.Enforce == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	rep, err := s.svc.Enforce.Run(r.Context(), s.filterInput())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.changed()
	writeJSON(w, http.StatusOK, rep)
}

// enforceSoon restores (or cuts) right after a change that settles an invoice,
// without making the admin's request wait for the router.
func (s *Server) enforceSoon() {
	if s.svc.Enforce == nil {
		return
	}
	in := s.filterInput()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := s.svc.Enforce.Run(ctx, in); err != nil {
			s.svc.Logger.Warnf("enforcement: run after change failed: %v", err)
		}
	}()
}

func (s *Server) liftBlacklist(w http.ResponseWriter, r *http.Request) {
	if s.svc.Identity == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return
	}
	var in struct {
		Identity string `json:"identity"`
	}
	if !decode(w, r, &in) {
		return
	}
	restored, err := s.svc.Identity.Lift(r.Context(), s.filterInput(), in.Identity)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.changed()
	writeJSON(w, http.StatusOK, map[string]any{"identity": in.Identity, "restored": restored})
}
