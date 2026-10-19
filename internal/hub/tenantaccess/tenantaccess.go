// Package tenantaccess answers one question for every household route: may
// this caller act on this household, and in which role?
//
// The kernel's JWT carries the user and the platform role, never the tenant,
// and its TenantContext middleware takes the tenant id straight from the URL
// without checking it. Any route under /tenants/{tenantId}/ must therefore
// check membership itself; the kernel has the repository for it but keeps its
// own check private (platform/tenant's requireActiveMembership).
package tenantaccess

import (
	"context"
	"errors"
	"net/http"

	kernelauth "github.com/agile-factory/kernel/kernel/auth"
	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	memberdomain "github.com/agile-factory/kernel/platform/membership/domain"
)

// Role is the caller's role inside the household.
type Role = memberdomain.MemberRole

// Caller is an authenticated member of the household in the URL.
type Caller struct {
	UserID   string
	TenantID string
	Role     Role
	// PlatformAdmin is set when the caller is a platform administrator
	// rather than a member. Support can then read a household without
	// being able to sign anything: every write to the box needs the
	// owner's key, which no admin holds.
	PlatformAdmin bool
}

// IsAdmin reports whether the caller owns or administrates the household.
func (c Caller) IsAdmin() bool {
	return c.PlatformAdmin || c.Role == memberdomain.MEMBER_ROLE_OWNER || c.Role == memberdomain.MEMBER_ROLE_ADMIN
}

// IsOwner reports whether the caller is the household's owner.
func (c Caller) IsOwner() bool {
	return c.Role == memberdomain.MEMBER_ROLE_OWNER
}

// Guard checks membership against the kernel's repository.
type Guard struct {
	members memberdomain.MembershipRepository
}

// New wires the guard.
func New(members memberdomain.MembershipRepository) *Guard { return &Guard{members: members} }

// Require resolves the caller for the {tenantId} in the request path.
//
// It returns 401 when no token was presented, and 403 for a tenant the
// caller is not an active member of — never 404, which would confirm that a
// household exists to someone with no business knowing.
func (g *Guard) Require(r *http.Request) (Caller, error) {
	tenantID := r.PathValue("tenantId")
	if tenantID == "" {
		return Caller{}, kernelerrors.BadRequest("missing household id")
	}
	ctx := r.Context()
	userID := kernelauth.UserIDFromContext(ctx)
	if userID == "" {
		return Caller{}, kernelerrors.Unauthorized("authentication required")
	}
	if kernelauth.IsPlatformAdmin(ctx) {
		return Caller{UserID: userID, TenantID: tenantID, PlatformAdmin: true}, nil
	}

	m, err := g.members.FindByUserAndTenant(ctx, userID, tenantID)
	if err != nil {
		if errors.Is(err, memberdomain.ErrMembershipNotFound) {
			return Caller{}, kernelerrors.Forbidden("not a member of this household")
		}
		return Caller{}, kernelerrors.WrapDBError(err)
	}
	if !m.IsActive() {
		return Caller{}, kernelerrors.Forbidden("membership is not active")
	}
	return Caller{UserID: userID, TenantID: tenantID, Role: m.Role}, nil
}

// RequireAdmin is Require plus the OWNER/ADMIN check that guards every write
// to the household's trust material.
func (g *Guard) RequireAdmin(r *http.Request) (Caller, error) {
	c, err := g.Require(r)
	if err != nil {
		return Caller{}, err
	}
	if !c.IsAdmin() {
		return Caller{}, kernelerrors.Forbidden("this action is reserved to the household's owner")
	}
	return c, nil
}

// WithTenant puts the resolved tenant in the context, where the kernel's
// own helpers look for it.
func WithTenant(ctx context.Context, c Caller) context.Context {
	return kernelauth.WithTenantID(ctx, c.TenantID)
}
