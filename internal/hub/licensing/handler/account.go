package handler

import (
	"net/http"
	"time"

	"github.com/agile-factory/kernel/kernel/response"
)

// The customer area of kergui.click (issue #63): an account reads its
// payments and its trial, and asks for the trial, with the session its
// WhatsApp code gave it (Middleware).

type paymentView struct {
	ID         string     `json:"id"`
	Provider   string     `json:"provider"`
	BoxID      string     `json:"boxId,omitempty"`
	Pass       string     `json:"pass"`
	AmountXOF  int64      `json:"amountXof"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"createdAt"`
	CreditedAt *time.Time `json:"creditedAt,omitempty"`
	// Waiting: paid, and waiting for the app to bind a box to start.
	Waiting bool `json:"waiting,omitempty"`
}

func (h *Handler) payments(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.Payments(r.Context(), accountFrom(r.Context()).ID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	out := make([]paymentView, 0, len(list))
	for _, p := range list {
		out = append(out, paymentView{
			ID: p.ID, Provider: p.Provider, BoxID: string(p.BoxID), Pass: p.PassCode,
			AmountXOF: p.AmountXOF, Status: string(p.Status), CreatedAt: p.CreatedAt, CreditedAt: p.CreditedAt,
			Waiting: p.BoxID == "" && p.Status == "credited",
		})
	}
	response.JSON(w, http.StatusOK, map[string]any{"payments": out})
}

type trialView struct {
	State       string     `json:"state"`
	BoxID       string     `json:"boxId,omitempty"`
	Days        int        `json:"days"`
	RequestedAt *time.Time `json:"requestedAt,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	EndsAt      *time.Time `json:"endsAt,omitempty"`
}

func optTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (h *Handler) trialStatus(w http.ResponseWriter, r *http.Request) {
	acc := accountFrom(r.Context())
	st, err := h.Svc.TrialStatus(r.Context(), acc.ID, acc.Phone)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusOK, trialView{
		State: string(st.State), BoxID: string(st.BoxID), Days: 7,
		RequestedAt: optTime(st.RequestedAt), StartedAt: optTime(st.StartedAt), EndsAt: optTime(st.EndsAt),
	})
}

// requestTrial asks for the 7-day Max trial for the signed-in number. Like
// the purchase page's form, it waits for the app to bind a box, and the
// days start there.
func (h *Handler) requestTrial(w http.ResponseWriter, r *http.Request) {
	acc := accountFrom(r.Context())
	if _, err := h.Svc.RequestTrial(r.Context(), string(acc.Phone)); err != nil {
		response.Error(w, mapErr(err))
		return
	}
	h.trialStatus(w, r)
}
