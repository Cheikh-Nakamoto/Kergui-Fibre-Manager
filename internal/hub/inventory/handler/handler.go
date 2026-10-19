// Package handler serves the household's inventory to the app: what devices
// the hub knows, and which addresses it is still waiting for the owner to
// confirm.
package handler

import (
	"errors"
	"net/http"
	"time"

	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/inventory/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
)

// Handler serves the inventory endpoints.
type Handler struct {
	Guard *tenantaccess.Guard
	Repo  domain.Repository
}

// RegisterRoutes mounts the household routes (kernel JWT required).
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	const p = "/api/v1/tenants/{tenantId}/router/"
	mux.HandleFunc("GET "+p+"inventory", h.inventory)
	mux.HandleFunc("GET "+p+"alerts/{windowId}", h.window)
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
	out := inventoryResponse{Devices: make([]deviceResponse, 0, len(devices)), Proposed: proposed}
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
