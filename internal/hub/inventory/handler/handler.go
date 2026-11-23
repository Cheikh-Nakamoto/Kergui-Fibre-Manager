// Package handler serves the household's inventory to the app: what devices
// the hub knows, and which addresses it is still waiting for the owner to
// confirm.
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"

	coredomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/inventory/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
)

// Handler serves the inventory endpoints.
type Handler struct {
	Guard *tenantaccess.Guard
	Repo  domain.Repository
	Now   func() time.Time
}

// RegisterRoutes mounts the household routes (kernel JWT required).
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	const p = "/api/v1/tenants/{tenantId}/router/"
	mux.HandleFunc("GET "+p+"inventory", h.inventory)
	mux.HandleFunc("GET "+p+"alerts/{windowId}", h.window)
	mux.HandleFunc("PUT "+p+"approvals", h.approveMany)
	mux.HandleFunc("PUT "+p+"approvals/{mac}", h.approve)
	mux.HandleFunc("DELETE "+p+"approvals/{mac}", h.withdraw)
}

type deviceResponse struct {
	domain.Device
	MACs []domain.MACRecord `json:"macs"`
}

type inventoryResponse struct {
	Devices []deviceResponse `json:"devices"`
	// Proposed are the addresses recognition has only corroborating evidence
	// for. The app shows them as "is this your phone?"; the hub binds none of
	// them on its own.
	Proposed []domain.MACRecord `json:"proposed"`
	// Approved are the addresses the household approved (issue #58): they
	// never alert. Every phone of the household reads its badges from here.
	Approved []domain.Approval `json:"approved"`
}

// inventory serves the household's reference inventory. Any member may read
// it: knowing which devices are at home is what the app is for.
func (h *Handler) inventory(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	devices, macs, err := h.Repo.Inventory(r.Context(), caller.TenantID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	byDevice := map[string][]domain.MACRecord{}
	var proposed []domain.MACRecord
	for _, m := range macs {
		byDevice[m.DeviceID] = append(byDevice[m.DeviceID], m)
		if m.State == domain.StateProposed {
			proposed = append(proposed, m)
		}
	}
	approved, err := h.Repo.Approvals(r.Context(), caller.TenantID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	if approved == nil {
		approved = []domain.Approval{}
	}
	out := inventoryResponse{Devices: make([]deviceResponse, 0, len(devices)), Proposed: proposed, Approved: approved}
	for _, d := range devices {
		out.Devices = append(out.Devices, deviceResponse{Device: d, MACs: byDevice[d.ID]})
	}
	response.JSON(w, http.StatusOK, out)
}

type windowResponse struct {
	ID       string              `json:"id"`
	OpenedAt time.Time           `json:"openedAt"`
	ClosesAt time.Time           `json:"closesAt"`
	SentAt   *time.Time          `json:"sentAt,omitempty"`
	Items    []domain.WindowItem `json:"items"`
}

// window serves one alert: the devices it grouped, each with the digest of
// the signed snapshot it came from. That digest is what lets the app check
// the alert against the relay's signature instead of trusting the hub.
func (h *Handler) window(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	win, err := h.Repo.Window(r.Context(), caller.TenantID, r.PathValue("windowId"))
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusOK, windowResponse{
		ID: win.ID, OpenedAt: win.OpenedAt, ClosesAt: win.ClosesAt,
		SentAt: win.ClosedAt, Items: win.Items,
	})
}

// approver resolves a member who may approve: the owner or an admin of the
// household. Support (a platform administrator) reads, it never decides
// which devices a household trusts.
func (h *Handler) approver(r *http.Request) (tenantaccess.Caller, error) {
	caller, err := h.Guard.RequireAdmin(r)
	if err != nil {
		return caller, err
	}
	if caller.PlatformAdmin {
		return caller, kernelerrors.Forbidden("only the household approves its devices")
	}
	return caller, nil
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

// approve is « Approuver » (issue #58): the address never alerts again.
func (h *Handler) approve(w http.ResponseWriter, r *http.Request) {
	h.setApproval(w, r, true)
}

// withdraw is the device sheet's « Retirer l'approbation »: the address
// alerts again on its next arrival.
func (h *Handler) withdraw(w http.ResponseWriter, r *http.Request) {
	h.setApproval(w, r, false)
}

func (h *Handler) setApproval(w http.ResponseWriter, r *http.Request, approved bool) {
	caller, err := h.approver(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	mac, err := coredomain.ParseMAC(r.PathValue("mac"))
	if err != nil {
		response.Error(w, kernelerrors.BadRequest("not a MAC address"))
		return
	}
	if err := h.Repo.SetApproval(r.Context(), caller.TenantID, mac.String(), approved, caller.UserID, h.now()); err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"mac": mac.String(), "approved": approved})
}

// approveMany approves several addresses at once: the phone's sync of the
// devices the household had already approved or named (issue #58). It only
// ever adds; withdrawing is one device at a time, from its sheet.
func (h *Handler) approveMany(w http.ResponseWriter, r *http.Request) {
	caller, err := h.approver(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	var req struct {
		MACs []string `json:"macs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		response.Error(w, kernelerrors.BadRequest("invalid JSON body"))
		return
	}
	if len(req.MACs) > 512 {
		response.Error(w, kernelerrors.BadRequest("too many addresses"))
		return
	}
	now, done := h.now(), 0
	for _, raw := range req.MACs {
		mac, err := coredomain.ParseMAC(raw)
		if err != nil {
			continue
		}
		if err := h.Repo.SetApproval(r.Context(), caller.TenantID, mac.String(), true, caller.UserID, now); err != nil {
			response.Error(w, mapErr(err))
			return
		}
		done++
	}
	response.JSON(w, http.StatusOK, map[string]any{"approved": done})
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNoWindow):
		return kernelerrors.NotFound("no such alert")
	case errors.Is(err, domain.ErrNoInventory):
		return kernelerrors.NotFound("this household has no inventory yet")
	default:
		return kernelerrors.WrapInternal(err)
	}
}
