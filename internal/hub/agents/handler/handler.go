// Package handler exposes the agents module over HTTP: the household routes
// the app calls with a kernel JWT, and the /relay/v1 routes a relay calls
// with its kga_ token.
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"
	"github.com/agile-factory/kernel/platform/audit"
	auditdomain "github.com/agile-factory/kernel/platform/audit/domain"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Audit actions this module records. The owner can always see who added or
// removed a relay, and when the household's trust material changed.
const (
	ActionAgentPaired   = auditdomain.AuditAction("AGENT_PAIRED")
	ActionAgentRevoked  = auditdomain.AuditAction("AGENT_REVOKED")
	ActionBundleUpdated = auditdomain.AuditAction("TRUST_BUNDLE_UPDATED")
	ActionCredsSealed   = auditdomain.AuditAction("CREDENTIALS_SEALED")
	ActionNetworkSet    = auditdomain.AuditAction("NETWORK_UPDATED")
	ActionPairingIssued = auditdomain.AuditAction("PAIRING_CODE_ISSUED")
)

// Handler serves the agents endpoints.
type Handler struct {
	Guard   *tenantaccess.Guard
	Issue   usecase.IssuePairing
	Enroll  usecase.Enroll
	Agents  usecase.Agents
	Trust   usecase.TrustMaterial
	Audit   audit.EventLogger
	Resolve *TokenResolver
}

// RegisterRoutes mounts the household routes. They sit behind the kernel's
// Auth middleware; the membership check is this module's own.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	const p = "/api/v1/tenants/{tenantId}/"
	mux.HandleFunc("POST "+p+"agents/pairing", h.issuePairing)
	mux.HandleFunc("GET "+p+"agents", h.listAgents)
	mux.HandleFunc("DELETE "+p+"agents/{agentId}", h.revokeAgent)
	mux.HandleFunc("PUT "+p+"agents/{agentId}/credentials", h.putCredentials)
	mux.HandleFunc("GET "+p+"network", h.getNetwork)
	mux.HandleFunc("PUT "+p+"network", h.putNetwork)
	mux.HandleFunc("GET "+p+"trust-bundle", h.getBundle)
	mux.HandleFunc("PUT "+p+"trust-bundle", h.putBundle)
}

// RegisterRelayRoutes mounts the routes a relay calls. They carry no JWT:
// the agent token identifies the caller, and TokenResolver resolves it.
func (h *Handler) RegisterRelayRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /relay/v1/pair/redeem", h.redeem)
	mux.Handle("GET /relay/v1/trust", h.Resolve.Middleware(http.HandlerFunc(h.relayBundle)))
	mux.Handle("GET /relay/v1/credentials", h.Resolve.Middleware(http.HandlerFunc(h.relayCredentials)))
}

// --- household routes -----------------------------------------------------

type pairingRequest struct {
	Kind      string `json:"kind"`
	ForUserID string `json:"forUserId"`
}

type pairingResponse struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (h *Handler) issuePairing(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.RequireAdmin(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	var req pairingRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	kind := domain.Kind(req.Kind)
	if kind == "" {
		kind = domain.KindPhone
	}
	out, err := h.Issue.Execute(r.Context(), usecase.IssuePairingParams{
		TenantID: caller.TenantID, CreatedBy: caller.UserID, Kind: kind, ForUserID: req.ForUserID,
	})
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	// The code itself is a secret and stays out of the audit metadata.
	h.log(r, caller, ActionPairingIssued, "pairing", "", map[string]any{"kind": string(kind)})
	response.JSON(w, http.StatusCreated, pairingResponse{Code: out.Code, ExpiresAt: out.ExpiresAt})
}

type agentView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Kind       string     `json:"kind"`
	UserID     string     `json:"userId,omitempty"`
	KeyID      string     `json:"keyId"`
	Ed25519    []byte     `json:"ed25519"`
	X25519     []byte     `json:"x25519"`
	CreatedAt  time.Time  `json:"createdAt"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
	LastSeenAt *time.Time `json:"lastSeenAt,omitempty"`
}

func (h *Handler) listAgents(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	list, err := h.Agents.List(r.Context(), caller.TenantID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	out := make([]agentView, 0, len(list))
	for _, a := range list {
		out = append(out, agentView{
			ID: a.ID, Name: a.Name, Kind: string(a.Kind), UserID: a.UserID, KeyID: a.KeyID,
			Ed25519: a.Ed25519, X25519: a.X25519,
			CreatedAt: a.CreatedAt, RevokedAt: a.RevokedAt, LastSeenAt: a.LastSeenAt,
		})
	}
	response.JSON(w, http.StatusOK, map[string]any{"agents": out})
}

func (h *Handler) revokeAgent(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.RequireAdmin(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	agentID := r.PathValue("agentId")
	if err := h.Agents.Revoke(r.Context(), caller.TenantID, agentID); err != nil {
		response.Error(w, mapErr(err))
		return
	}
	h.log(r, caller, ActionAgentRevoked, "agent", agentID, nil)
	response.JSON(w, http.StatusNoContent, nil)
}

type credentialsRequest struct {
	Blob    []byte `json:"blob"`
	Version uint64 `json:"version"`
}

func (h *Handler) putCredentials(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.RequireAdmin(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	var req credentialsRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	agentID := r.PathValue("agentId")
	if err := h.Trust.PutCredentials(r.Context(), caller.TenantID, agentID, req.Blob, req.Version); err != nil {
		response.Error(w, mapErr(err))
		return
	}
	// Only the fact is recorded: the blob is the box password.
	h.log(r, caller, ActionCredsSealed, "agent", agentID, map[string]any{"version": req.Version})
	response.JSON(w, http.StatusNoContent, nil)
}

type networkRequest struct {
	SSIDs     []string `json:"ssids"`
	BSSIDs    []string `json:"bssids"`
	RouterPin string   `json:"routerPin"`
}

func (h *Handler) getNetwork(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	n, err := h.Trust.GetNetwork(r.Context(), caller.TenantID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusOK, networkRequest{SSIDs: n.SSIDs, BSSIDs: n.BSSIDs, RouterPin: n.RouterPin})
}

func (h *Handler) putNetwork(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.RequireAdmin(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	var req networkRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	n := domain.Network{TenantID: caller.TenantID, SSIDs: req.SSIDs, BSSIDs: req.BSSIDs, RouterPin: req.RouterPin}
	if err := h.Trust.PutNetwork(r.Context(), n); err != nil {
		response.Error(w, mapErr(err))
		return
	}
	h.log(r, caller, ActionNetworkSet, "network", caller.TenantID, map[string]any{"ssids": len(req.SSIDs)})
	response.JSON(w, http.StatusNoContent, nil)
}

// bundleBody is the wire shape of a signed bundle, in and out.
type bundleBody struct {
	Payload []byte `json:"payload"`
	Sig     []byte `json:"sig"`
	Version uint64 `json:"version,omitempty"`
}

func (h *Handler) getBundle(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	b, err := h.Trust.GetBundle(r.Context(), caller.TenantID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusOK, bundleBody{Payload: b.Payload, Sig: b.Sig, Version: b.Version})
}

func (h *Handler) putBundle(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.RequireAdmin(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	var req bundleBody
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	signed := trust.Signed[trust.Bundle]{Payload: req.Payload, Sig: req.Sig}
	if err := h.Trust.PutBundle(r.Context(), caller.TenantID, signed); err != nil {
		response.Error(w, mapErr(err))
		return
	}
	b, _ := signed.Unverified()
	h.log(r, caller, ActionBundleUpdated, "trust_bundle", caller.TenantID, map[string]any{"version": b.Version})
	response.JSON(w, http.StatusNoContent, nil)
}

// --- relay routes ---------------------------------------------------------

type redeemRequest struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Ed25519 []byte `json:"ed25519"`
	X25519  []byte `json:"x25519"`
}

type redeemResponse struct {
	AgentID  string `json:"agentId"`
	TenantID string `json:"tenantId"`
	Token    string `json:"token"`
	KeyID    string `json:"keyId"`
}

// redeem turns a pairing code into an agent. It is the only unauthenticated
// write in the hub, which is why the code is 128 bits, single-use, five
// minutes long, and the route is rate-limited.
func (h *Handler) redeem(w http.ResponseWriter, r *http.Request) {
	var req redeemRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	out, err := h.Enroll.Execute(r.Context(), usecase.EnrollParams{
		Code: req.Code, Name: req.Name, Ed25519: req.Ed25519, X25519: req.X25519,
	})
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	h.Audit.Execute(r.Context(), audit.LogEventParams{
		TenantID: out.TenantID, ActorID: out.AgentID, Action: ActionAgentPaired,
		TargetType: "agent", TargetID: out.AgentID, Metadata: map[string]any{"key_id": out.KeyID},
	})
	response.JSON(w, http.StatusCreated, redeemResponse{
		AgentID: out.AgentID, TenantID: out.TenantID, Token: out.Token, KeyID: out.KeyID,
	})
}

func (h *Handler) relayBundle(w http.ResponseWriter, r *http.Request) {
	agent := AgentFrom(r.Context())
	b, err := h.Trust.GetBundle(r.Context(), agent.TenantID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusOK, bundleBody{Payload: b.Payload, Sig: b.Sig, Version: b.Version})
}

func (h *Handler) relayCredentials(w http.ResponseWriter, r *http.Request) {
	agent := AgentFrom(r.Context())
	c, err := h.Trust.GetCredentials(r.Context(), agent.TenantID, agent.ID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusOK, credentialsRequest{Blob: c.Blob, Version: c.Version})
}

// --- plumbing -------------------------------------------------------------

func (h *Handler) log(r *http.Request, c tenantaccess.Caller, action auditdomain.AuditAction, targetType, targetID string, meta map[string]any) {
	h.Audit.Execute(r.Context(), audit.LogEventParams{
		TenantID: c.TenantID, ActorID: c.UserID, Action: action,
		TargetType: targetType, TargetID: targetID, Metadata: meta,
	})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return kernelerrors.BadRequest("malformed request body")
	}
	return nil
}

// mapErr turns a domain error into the kernel's AppError, so the response
// carries a stable code and the right status.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		return kernelerrors.NotFound("no such agent")
	case errors.Is(err, domain.ErrPairingNotFound):
		// One message for unknown, spent and expired: a caller probing
		// codes learns nothing from the difference.
		return kernelerrors.NotFound("this pairing code is not usable")
	case errors.Is(err, domain.ErrRevoked):
		return kernelerrors.Unauthorized("this agent has been revoked")
	case errors.Is(err, domain.ErrStaleVersion):
		return kernelerrors.Conflict("a newer version is already stored")
	case errors.Is(err, domain.ErrNoBundle):
		return kernelerrors.NotFound("this household has no trust bundle yet")
	case errors.Is(err, domain.ErrNoCredentials):
		return kernelerrors.NotFound("no credentials are sealed for this agent")
	case errors.Is(err, trust.ErrTenant), errors.Is(err, trust.ErrUnsigned), errors.Is(err, trust.ErrMalformed):
		return kernelerrors.BadRequest(err.Error())
	default:
		return kernelerrors.WrapInternal(err)
	}
}
