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

// Execute logs in and optionally persists.
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

	if err := uc.vault.Store(ctx, res.RouterID, in.Creds); err != nil {
		return res, fmt.Errorf("store credentials: %w", err)
	}
	meta := gw.Meta()
	r := domain.Router{
		ID:        res.RouterID,
		Name:      in.BaseURL,
		BaseURL:   in.BaseURL,
		AdapterID: adapterID,
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
		return res, fmt.Errorf("record router: %w", err)
	}
	res.Stored = true
	return res, nil
}
