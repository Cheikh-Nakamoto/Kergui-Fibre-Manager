package usecase

import (
	"context"
	"errors"
)

// ErrHubIdentityOff is a request for kernel tokens on a hub built without
// the identity bridge (unit tests, a licensing-only deployment).
var ErrHubIdentityOff = errors.New("licensing: this hub does not sign phones in to a household")

// HubSession is the kernel side of a sign-in (issue #41): the household the
// verified number owns, and a kernel access token plus its rotating refresh
// token, usable on /api/v1/tenants/{TenantID}/... The refresh token is
// rotated by the kernel's own POST /api/v1/auth/refresh.
type HubSession struct {
	UserID       string
	TenantID     string
	AccessToken  string
	RefreshToken string
}

// Identity makes a verified number the hub identity: it creates or finds
// the kernel user and household of an account and signs it in. It lives
// outside this module (internal/hub/identity), which keeps the licensing
// module free of the kernel's auth, tenant and membership packages.
type Identity interface {
	SignIn(ctx context.Context, a Account) (HubSession, error)
	// SignOut revokes the account's kernel refresh tokens, every phone's
	// (support, issue #78).
	SignOut(ctx context.Context, a Account) error
}

// HubSession issues a fresh kernel token pair for an account that is
// already signed in with a kgs_ session: the app's way back in when its
// kernel refresh token has run out or was revoked while the licensing
// session still stands. The two are one sign-in.
func (s *Service) HubSession(ctx context.Context, a Account) (HubSession, error) {
	if s.Identity == nil {
		return HubSession{}, ErrHubIdentityOff
	}
	return s.Identity.SignIn(ctx, a)
}
