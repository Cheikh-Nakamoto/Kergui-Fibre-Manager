package identity

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"

	alerts "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Error codes of the members routes, which the app words (src/i18n).
const (
	CodeInvalidPhone   = "INVALID_PHONE"
	CodeInvalidRole    = "INVALID_ROLE"
	CodeOwnerOnly      = "OWNER_ONLY"
	CodeAlreadyMember  = "ALREADY_MEMBER"
	CodeOwnerUntouched = "OWNER_UNTOUCHED"
	CodeInviteLimit    = "INVITE_LIMIT"
	CodeWhatsAppDown   = "WHATSAPP_UNAVAILABLE"
	CodeInvalidKey     = "INVALID_KEY"
)

// RegisterRoutes mounts the household's members routes (kernel JWT
// required, membership checked by the guard):
//
//	GET    /api/v1/tenants/{tenantId}/members
//	POST   /api/v1/tenants/{tenantId}/members/invites          {phone, role}
//	DELETE /api/v1/tenants/{tenantId}/members/invites/{inviteId}
//	PATCH  /api/v1/tenants/{tenantId}/members/{membershipId}   {role}
//	DELETE /api/v1/tenants/{tenantId}/members/{membershipId}
//	PUT    /api/v1/tenants/{tenantId}/members/me/key           {ed25519, x25519}
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	const p = "/api/v1/tenants/{tenantId}/members"
	mux.HandleFunc("GET "+p, m.list)
	mux.HandleFunc("POST "+p+"/invites", m.invite)
	mux.HandleFunc("DELETE "+p+"/invites/{inviteId}", m.cancelInvite)
	mux.HandleFunc("PATCH "+p+"/{membershipId}", m.changeRole)
	mux.HandleFunc("DELETE "+p+"/{membershipId}", m.remove)
	mux.HandleFunc("PUT "+p+"/me/key", m.putKey)
}

// keysView is a member phone's public keys (base64 in JSON) and their id.
type keysView struct {
	KeyID   string `json:"keyId"`
	Ed25519 []byte `json:"ed25519"`
	X25519  []byte `json:"x25519"`
}

type keyRequest struct {
	Ed25519 []byte `json:"ed25519"`
	X25519  []byte `json:"x25519"`
}

func (m *Module) putKey(w http.ResponseWriter, r *http.Request) {
	caller, err := m.cfg.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	var req keyRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	if err := m.SetMemberKey(r.Context(), caller, MemberKeys{Ed25519: req.Ed25519, X25519: req.X25519}); err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusNoContent, nil)
}

type memberView struct {
	MembershipID string     `json:"membershipId"`
	UserID       string     `json:"userId"`
	Role         Role       `json:"role"`
	Phone        string     `json:"phone,omitempty"`
	JoinedAt     *time.Time `json:"joinedAt,omitempty"`
	Keys         *keysView  `json:"keys,omitempty"`
}

type inviteView struct {
	ID        string    `json:"id"`
	Phone     string    `json:"phone"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type householdView struct {
	Household struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"household"`
	You     memberView    `json:"you"`
	Members []memberView  `json:"members"`
	Invites *[]inviteView `json:"invites,omitempty"`
}

func viewMember(mb Member) memberView {
	v := memberView{MembershipID: mb.MembershipID, UserID: mb.UserID, Role: mb.Role, Phone: string(mb.Phone), JoinedAt: mb.JoinedAt}
	if mb.Keys != nil {
		v.Keys = &keysView{KeyID: trust.KeyID(mb.Keys.Ed25519), Ed25519: mb.Keys.Ed25519, X25519: mb.Keys.X25519}
	}
	return v
}

func viewInvite(i Invite) inviteView {
	return inviteView{ID: i.ID, Phone: string(i.Phone), Role: i.Role, CreatedAt: i.CreatedAt, ExpiresAt: i.ExpiresAt}
}

func (m *Module) list(w http.ResponseWriter, r *http.Request) {
	caller, err := m.cfg.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	h, err := m.ListMembers(r.Context(), caller)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	var out householdView
	out.Household.ID, out.Household.Name = h.TenantID, h.Name
	out.You = viewMember(h.You)
	out.Members = make([]memberView, 0, len(h.Members))
	for _, mb := range h.Members {
		out.Members = append(out.Members, viewMember(mb))
	}
	if caller.IsOwner() {
		inv := make([]inviteView, 0, len(h.Invites))
		for _, i := range h.Invites {
			inv = append(inv, viewInvite(i))
		}
		out.Invites = &inv
	}
	response.JSON(w, http.StatusOK, out)
}

type roleRequest struct {
	Phone string `json:"phone"`
	Role  Role   `json:"role"`
}

func (m *Module) invite(w http.ResponseWriter, r *http.Request) {
	caller, err := m.cfg.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	var req roleRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	inv, err := m.InviteMember(r.Context(), caller, req.Phone, req.Role)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusCreated, viewInvite(inv))
}

func (m *Module) cancelInvite(w http.ResponseWriter, r *http.Request) {
	caller, err := m.cfg.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	if err := m.CancelInvite(r.Context(), caller, r.PathValue("inviteId")); err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusNoContent, nil)
}

func (m *Module) changeRole(w http.ResponseWriter, r *http.Request) {
	caller, err := m.cfg.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	var req roleRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	mb, err := m.ChangeRole(r.Context(), caller, r.PathValue("membershipId"), req.Role)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusOK, viewMember(mb))
}

func (m *Module) remove(w http.ResponseWriter, r *http.Request) {
	caller, err := m.cfg.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	if err := m.RemoveMember(r.Context(), caller, r.PathValue("membershipId")); err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusNoContent, nil)
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return kernelerrors.BadRequest("malformed request body")
	}
	return nil
}

func mapErr(err error) error {
	var app *kernelerrors.AppError
	switch {
	case errors.Is(err, alerts.ErrInvalidPhone):
		return kernelerrors.New(CodeInvalidPhone, "invalid phone number", http.StatusBadRequest, err)
	case errors.Is(err, ErrInvalidKey):
		return kernelerrors.New(CodeInvalidKey, err.Error(), http.StatusBadRequest, err)
	case errors.Is(err, ErrInvalidRole):
		return kernelerrors.New(CodeInvalidRole, err.Error(), http.StatusBadRequest, err)
	case errors.Is(err, ErrOwnerOnly):
		return kernelerrors.New(CodeOwnerOnly, err.Error(), http.StatusForbidden, err)
	case errors.Is(err, ErrAlreadyMember):
		return kernelerrors.New(CodeAlreadyMember, err.Error(), http.StatusConflict, err)
	case errors.Is(err, ErrOwnerUntouched):
		return kernelerrors.New(CodeOwnerUntouched, err.Error(), http.StatusBadRequest, err)
	case errors.Is(err, ErrInviteLimit):
		return kernelerrors.New(CodeInviteLimit, err.Error(), http.StatusTooManyRequests, err)
	case errors.Is(err, ErrMessageNotSent):
		return kernelerrors.New(CodeWhatsAppDown, "WhatsApp is unavailable, try again later", http.StatusBadGateway, err)
	case errors.Is(err, ErrNoSuchMember), errors.Is(err, ErrNoSuchInvite):
		return kernelerrors.NotFound(err.Error())
	case errors.Is(err, ErrMembersDisabled):
		return kernelerrors.NotFound(err.Error())
	case errors.As(err, &app):
		return err
	default:
		return kernelerrors.WrapInternal(err)
	}
}
