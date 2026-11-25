package usecase

import (
	"context"
	"fmt"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Authenticate logs in to a router and, unless this is only a connection test,
// stores the credentials encrypted and records the router (brief §12).
type Authenticate struct {
	factory port.RouterFactory
	vault   port.CredentialVault
	routers port.RouterRepository
	disco   port.DiscoveryPort
	clock   port.Clock
	log     port.Logger
}

// NewAuthenticate wires the interactor.
func NewAuthenticate(f port.RouterFactory, v port.CredentialVault, r port.RouterRepository, d port.DiscoveryPort, c port.Clock, l port.Logger) *Authenticate {
	return &Authenticate{factory: f, vault: v, routers: r, disco: d, clock: c, log: l}
}

// AuthenticateInput parameterises a login.
type AuthenticateInput struct {
	BaseURL   string
	AdapterID string // optional; discovered when empty
	Creds     domain.Credentials
	Opts      port.RouterOptions
	// Persist stores the credentials and router on success. The `login` command
	// sets it; `login --test` (a pure connection test) does not.
	Persist bool
}

// AuthenticateResult reports the outcome.
type AuthenticateResult struct {
	RouterID  string
	AdapterID string
	Info      *domain.RouterInfo
	Stored    bool
}

// Execute logs in and optionally persists. A router that demands a new admin
// password answers domain.ErrPasswordChangeRequired and nothing is stored:
// FirstPassword is the way on.
func (uc *Authenticate) Execute(ctx context.Context, in AuthenticateInput) (AuthenticateResult, error) {
	adapterID, info, err := resolveAdapterID(ctx, uc.disco, in.BaseURL, in.AdapterID, in.Opts)
	if err != nil {
		return AuthenticateResult{}, err
	}
	gw, err := uc.factory.New(adapterID, in.BaseURL, in.Opts)
	if err != nil {
		return AuthenticateResult{}, err
	}
	if err := gw.Login(ctx, in.Creds); err != nil {
		return AuthenticateResult{}, err
	}

	res := AuthenticateResult{RouterID: RouterID(in.BaseURL), AdapterID: adapterID, Info: info}
	if !in.Persist {
		return res, nil
	}

	if err := uc.persist(ctx, &res, in.BaseURL, gw, in.Creds, info); err != nil {
		return res, err
	}
	return res, nil
}

// persist stores the credentials encrypted and records the router.
func (uc *Authenticate) persist(ctx context.Context, res *AuthenticateResult, baseURL string, gw port.RouterPort, creds domain.Credentials, info *domain.RouterInfo) error {
	if err := uc.vault.Store(ctx, res.RouterID, creds); err != nil {
		return fmt.Errorf("store credentials: %w", err)
	}
	meta := gw.Meta()
	r := domain.Router{
		ID:        res.RouterID,
		Name:      baseURL,
		BaseURL:   baseURL,
		AdapterID: res.AdapterID,
		Vendor:    meta.Vendor,
		CreatedAt: uc.clock.Now(),
	}
	if info != nil {
		r.Model = info.Model
		if info.Vendor != "" {
			r.Vendor = info.Vendor
		}
	}
	if err := uc.routers.Upsert(ctx, r); err != nil {
		return fmt.Errorf("record router: %w", err)
	}
	res.Stored = true
	return nil
}

// FirstPasswordInput parameterises the first-password change (#92).
type FirstPasswordInput struct {
	BaseURL   string
	AdapterID string // optional; discovered when empty
	// Creds are the current login, the one on the box's label.
	Creds domain.Credentials
	// NewPassword replaces Creds.Password; the username stays.
	NewPassword string
	Opts        port.RouterOptions
}

// FirstPassword sets the admin password a router demands before it opens its
// interface (Execute answered domain.ErrPasswordChangeRequired): it changes
// the password, signs in again with the new one, then stores the NEW
// password and records the router as a login does. The label password is
// never stored. The new password is checked against the box's rule first
// (domain.ErrWeakPassword), and the router may still refuse it
// (domain.ErrPasswordRejected).
func (uc *Authenticate) FirstPassword(ctx context.Context, in FirstPasswordInput) (AuthenticateResult, error) {
	if err := domain.ValidateBoxPassword(in.NewPassword); err != nil {
		return AuthenticateResult{}, err
	}
	adapterID, info, err := resolveAdapterID(ctx, uc.disco, in.BaseURL, in.AdapterID, in.Opts)
	if err != nil {
		return AuthenticateResult{}, err
	}
	gw, err := uc.factory.New(adapterID, in.BaseURL, in.Opts)
	if err != nil {
		return AuthenticateResult{}, err
	}
	ch, ok := gw.(port.AdminPasswordChanger)
	if !ok {
		return AuthenticateResult{}, fmt.Errorf("%w: adapter %s cannot set the admin password", domain.ErrNotImplemented, adapterID)
	}
	if err := ch.ChangeAdminPassword(ctx, in.Creds, in.NewPassword); err != nil {
		return AuthenticateResult{}, err
	}
	uc.log.Infof("auth: the router's admin password was set at its demand (user %s)", in.Creds.Username)
	res := AuthenticateResult{RouterID: RouterID(in.BaseURL), AdapterID: adapterID, Info: info}
	creds := domain.Credentials{Username: in.Creds.Username, Password: in.NewPassword}
	if err := uc.persist(ctx, &res, in.BaseURL, gw, creds, info); err != nil {
		return res, err
	}
	return res, nil
}
