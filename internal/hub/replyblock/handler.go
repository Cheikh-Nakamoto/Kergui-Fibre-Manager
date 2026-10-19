package replyblock

import (
	"net/http"

	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// hubKeyResponse is what the owner's app needs in order to delegate the
// narrow key — or to decide not to. It is public key material only: the
// private half is derived on demand and never stored, let alone served.
type hubKeyResponse struct {
	Enabled bool   `json:"enabled"`
	KeyID   string `json:"keyId,omitempty"`
	Scope   string `json:"scope,omitempty"`
	Ed25519 []byte `json:"ed25519,omitempty"`
	X25519  []byte `json:"x25519,omitempty"`
}

// RegisterRoutes mounts the household route (kernel JWT required).
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/tenants/{tenantId}/hub-key", s.hubKey)
}

// hubKey serves this household's hub key, for the owner's app to put in the
// trust bundle as a hub_block delegation. Only an owner or an admin may
// read it: it is the material of a decision about the box.
func (s *Service) hubKey(w http.ResponseWriter, r *http.Request) {
	caller, err := s.cfg.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	if !caller.IsAdmin() {
		response.Error(w, kernelerrors.Forbidden("only the household's owner or an admin may read the hub key"))
		return
	}
	if !s.enabled {
		// An honest "off" rather than a key that would never work.
		response.JSON(w, http.StatusOK, hubKeyResponse{Enabled: false})
		return
	}
	id, err := s.cfg.Keys.For(caller.TenantID)
	if err != nil {
		response.Error(w, kernelerrors.WrapInternal(err))
		return
	}
	pub := id.Public()
	response.JSON(w, http.StatusOK, hubKeyResponse{
		Enabled: true, KeyID: id.KeyID(), Scope: trust.ScopeHubBlock,
		Ed25519: pub.Ed25519, X25519: pub.X25519,
	})
}
