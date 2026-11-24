// Package handler serves a household's backups (issue #88): the owner's
// phones and the household's relays send one, every member reads it.
package handler

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/backup"
	agentshandler "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/handler"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/backups/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/backups/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
)

// maxBody bounds a request: the archive in base64 (4/3 of MaxArchive) and
// the JSON around it.
const maxBody = domain.MaxArchive/3*4 + 4<<10

// Handler serves the backup endpoints.
type Handler struct {
	Guard  *tenantaccess.Guard
	Tokens *agentshandler.TokenResolver
	Svc    *usecase.Service
}

// RegisterRoutes mounts the household routes (kernel JWT required).
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	const p = "/api/v1/tenants/{tenantId}/router/backups"
	mux.HandleFunc("GET "+p, h.list)
	mux.HandleFunc("GET "+p+"/{boxId}", h.get)
	mux.HandleFunc("PUT "+p+"/{boxId}", h.putFromPhone)
}

// RegisterRelayRoutes mounts the route a relay sends its backup on, with its
// kga_ token: the household is the relay's own.
func (h *Handler) RegisterRelayRoutes(mux *http.ServeMux) {
	mux.Handle("PUT /relay/v1/backups/{boxId}", h.Tokens.Middleware(http.HandlerFunc(h.putFromRelay)))
}

type metaResponse struct {
	BoxID     string        `json:"boxId"`
	Source    domain.Source `json:"source"`
	UpdatedAt time.Time     `json:"updatedAt"`
	Devices   int           `json:"devices"`
	Months    int           `json:"months"`
	Size      int           `json:"size,omitempty"`
}

func meta(b domain.Backup) metaResponse {
	return metaResponse{BoxID: b.BoxID, Source: b.Source, UpdatedAt: b.UpdatedAt, Devices: b.Devices, Months: b.Months, Size: len(b.Archive)}
}

type backupResponse struct {
	metaResponse
	// Archive is the zip, base64: what the app's POST /api/import takes.
	Archive string `json:"archive"`
	// Name is a file name for « Télécharger », as GET /api/export gives.
	Name string `json:"name"`
}

type savedResponse struct {
	metaResponse
	Merged bool `json:"merged"`
}

// list is every box's last backup, without the archives: Réglages' « Dernière
// sauvegarde » and support's view. Any member reads.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	list, err := h.Svc.List(r.Context(), caller.TenantID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	out := make([]metaResponse, 0, len(list))
	for _, b := range list {
		out = append(out, meta(b))
	}
	response.JSON(w, http.StatusOK, map[string]any{"backups": out})
}

// get is the box's backup: a new or second phone of the household imports
// it. Any member reads: the names make sense of the list on their phone too.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	b, err := h.Svc.Get(r.Context(), caller.TenantID, r.PathValue("boxId"))
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusOK, backupResponse{
		metaResponse: meta(b),
		Archive:      base64.StdEncoding.EncodeToString(b.Archive),
		Name:         "kergui-sauvegarde-" + b.UpdatedAt.Format("2006-01-02") + ".zip",
	})
}

// putFromPhone is the owner's phone sending its backup. The owner or an
// admin of the household only; support (a platform administrator) reads, it
// never writes a household's data.
func (h *Handler) putFromPhone(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.RequireAdmin(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	if caller.PlatformAdmin {
		response.Error(w, kernelerrors.Forbidden("only the household sends its backup"))
		return
	}
	h.save(w, r, caller.TenantID, domain.Source{Kind: domain.SourcePhone, ID: caller.UserID})
}

// putFromRelay is a relay of the household (`kergui serve --hub`, a
// dedicated phone) sending its backup.
func (h *Handler) putFromRelay(w http.ResponseWriter, r *http.Request) {
	agent := agentshandler.AgentFrom(r.Context())
	if agent.TenantID == "" {
		response.Error(w, kernelerrors.Unauthorized("relay token required"))
		return
	}
	h.save(w, r, agent.TenantID, domain.Source{Kind: domain.SourceRelay, ID: agent.ID})
}

func (h *Handler) save(w http.ResponseWriter, r *http.Request, tenantID string, src domain.Source) {
	var req struct {
		Archive string `json:"archive"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			response.Error(w, mapErr(domain.ErrTooLarge))
			return
		}
		response.Error(w, kernelerrors.BadRequest("invalid JSON body"))
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.Archive)
	if err != nil || len(raw) == 0 {
		response.Error(w, kernelerrors.New("BAD_BACKUP", "archive: not base64", http.StatusBadRequest, nil))
		return
	}
	saved, err := h.Svc.Save(r.Context(), tenantID, r.PathValue("boxId"), src, raw)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusOK, savedResponse{metaResponse: meta(saved.Backup), Merged: saved.Merged})
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		return kernelerrors.NotFound("this box has no backup yet")
	case errors.Is(err, domain.ErrBadBox):
		return kernelerrors.BadRequest("not a box id")
	case errors.Is(err, domain.ErrTooLarge):
		return kernelerrors.New("PAYLOAD_TOO_LARGE", "the backup is too large", http.StatusRequestEntityTooLarge, nil)
	case errors.Is(err, backup.ErrBadBackup):
		return kernelerrors.New("BAD_BACKUP", backup.ErrBadBackup.Error(), http.StatusBadRequest, nil)
	default:
		return kernelerrors.WrapInternal(err)
	}
}
