package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

type historyItemDTO struct {
	MAC         string          `json:"mac"`
	Name        string          `json:"name"`
	Hostname    string          `json:"hostname"`
	IP          string          `json:"ip"`
	Vendor      string          `json:"vendor"`
	FirstSeen   time.Time       `json:"first_seen"`
	LastSeen    time.Time       `json:"last_seen"`
	Connected   bool            `json:"connected"`
	Blocked     bool            `json:"blocked"`
	Bytes       uint64          `json:"bytes"`
	DueBytes    uint64          `json:"due_bytes"`
	EstimateXOF int64           `json:"estimate_xof"`
	Pending     *domain.Payment `json:"pending,omitempty"`
	MemberID    string          `json:"member_id,omitempty"`
	MemberName  string          `json:"member_name,omitempty"`
	RandomMAC   bool            `json:"random_mac"`
	Identity    string          `json:"identity,omitempty"`
	Losses      int             `json:"losses"`
	Blacklisted bool            `json:"blacklisted"`
	SharedName  bool            `json:"shared_name"`
}

type historyPageDTO struct {
	Items           []historyItemDTO       `json:"items"`
	Total           int                    `json:"total"`
	Page            int                    `json:"page"`
	Size            int                    `json:"size"`
	Pages           int                    `json:"pages"`
	Period          string                 `json:"period"`
	Settings        domain.BillingSettings `json:"settings"`
	PaymentsEnabled bool                   `json:"payments_enabled"`
	LossLimit       int                    `json:"loss_limit"`
}

func (s *Server) billingReady(w http.ResponseWriter) bool {
	if s.svc.Billing == nil {
		s.writeErr(w, domain.ErrNotImplemented)
		return false
	}
	return true
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	if !s.billingReady(w) {
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("size"))
	if size == 0 {
		size = 20
	}
	hp, err := s.svc.Billing.History(r.Context(), s.cfg.BaseURL, q.Get("q"), page, size)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	set, err := s.svc.Billing.Settings(r.Context())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	out := historyPageDTO{
		Items: make([]historyItemDTO, 0, len(hp.Items)), Total: hp.Total, Page: hp.Page, Size: hp.Size,
		Pages: max(1, (hp.Total+hp.Size-1)/hp.Size), Period: hp.Period, Settings: set,
		PaymentsEnabled: s.svc.Billing.PaymentsEnabled(),
		LossLimit:       domain.IdentityLossLimit,
	}
	owner := map[string]domain.Member{}
	if s.svc.Members != nil {
		ms, err := s.svc.Members.List(r.Context(), s.cfg.BaseURL)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		for _, m := range ms {
			for _, mac := range m.MACs {
				owner[mac.String()] = m
			}
		}
	}
	idInfo := map[string]usecase.IdentityInfo{}
	if s.svc.Identity != nil {
		info, err := s.svc.Identity.Info(r.Context(), s.cfg.BaseURL)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		for mac, i := range info {
			idInfo[mac.String()] = i
		}
	}
	for _, it := range hp.Items {
		d := it.Device
		ii := idInfo[d.MAC.String()]
		name := d.CustomName
		if name == "" {
			name = d.Hostname
		}
		out.Items = append(out.Items, historyItemDTO{
			MAC: d.MAC.String(), Name: name, Hostname: d.Hostname, IP: d.IP.String(), Vendor: d.Vendor,
			FirstSeen: d.FirstSeen, LastSeen: d.LastSeen, Connected: d.Connected, Blocked: d.Blocked,
			Bytes: it.Bytes, DueBytes: it.DueBytes, EstimateXOF: it.EstimateXOF, Pending: it.Pending,
			MemberID: owner[d.MAC.String()].ID, MemberName: owner[d.MAC.String()].Name,
			RandomMAC: d.MAC.IsRandomized(), Identity: ii.Identity, Losses: ii.Losses,
			Blacklisted: ii.Blacklisted, SharedName: ii.Shared,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) billingSettings(w http.ResponseWriter, r *http.Request) {
	if !s.billingReady(w) {
		return
	}
	set, err := s.svc.Billing.Settings(r.Context())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

func (s *Server) saveBillingSettings(w http.ResponseWriter, r *http.Request) {
	if !s.billingReady(w) {
		return
	}
	var set domain.BillingSettings
	if err := json.NewDecoder(r.Body).Decode(&set); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if err := s.svc.Billing.SaveSettings(r.Context(), set); err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

func (s *Server) devicePayments(w http.ResponseWriter, r *http.Request) {
	if !s.billingReady(w) {
		return
	}
	mac, err := domain.ParseMAC(r.PathValue("mac"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	ps, err := s.svc.Billing.Payments(r.Context(), s.cfg.BaseURL, mac)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (s *Server) requestPayment(w http.ResponseWriter, r *http.Request) {
	if !s.billingReady(w) {
		return
	}
	mac, err := domain.ParseMAC(r.PathValue("mac"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	var req struct {
		CustomerName string `json:"customer_name"`
		Phone        string `json:"phone"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	p, err := s.svc.Billing.RequestPayment(r.Context(), usecase.RequestPaymentInput{
		BaseURL: s.cfg.BaseURL, MAC: mac, CustomerName: req.CustomerName, Phone: req.Phone,
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if s.svc.Enforce != nil {
		if err := s.svc.Enforce.LinkPayment(r.Context(), s.cfg.BaseURL, p); err != nil {
			s.svc.Logger.Warnf("billing: could not tie the cut of %s to payment %s: %v", mac, p.ID, err)
		}
	}
	s.changed()
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) refreshPayment(w http.ResponseWriter, r *http.Request) {
	if !s.billingReady(w) {
		return
	}
	p, err := s.svc.Billing.Refresh(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if p.Status == domain.PaymentPaid {
		s.enforceSoon()
	}
	s.changed()
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) cancelPayment(w http.ResponseWriter, r *http.Request) {
	if !s.billingReady(w) {
		return
	}
	p, err := s.svc.Billing.Cancel(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.changed()
	writeJSON(w, http.StatusOK, p)
}
