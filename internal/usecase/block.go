package usecase

import (
	"context"
	"fmt"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// ChangeAccessInput parameterises a block/unblock. Confirm must be true: a write
// to the router is an explicit, outward-facing action and is never performed on
// an unconfirmed request (brief §5 staging, §12 care).
type ChangeAccessInput struct {
	BaseURL   string
	AdapterID string
	MAC       domain.MAC
	Opts      port.RouterOptions
	Creds     domain.Credentials
	Confirm   bool
}

// errConfirm is returned when a write is requested without confirmation.
var errConfirm = fmt.Errorf("%w: confirmation required", domain.ErrBlockFailed)

// BlockDevice adds a MAC to the router's block list. Until a model's write path
// is verified on real hardware the gateway returns domain.ErrNotImplemented, so
// this interactor is safe to wire up ahead of that validation.
type BlockDevice struct {
	factory port.RouterFactory
	vault   port.CredentialVault
	disco   port.DiscoveryPort
	clock   port.Clock
	log     port.Logger
}

// NewBlockDevice wires the interactor.
func NewBlockDevice(f port.RouterFactory, v port.CredentialVault, d port.DiscoveryPort, c port.Clock, l port.Logger) *BlockDevice {
	return &BlockDevice{factory: f, vault: v, disco: d, clock: c, log: l}
}

// Execute blocks the MAC.
func (uc *BlockDevice) Execute(ctx context.Context, in ChangeAccessInput) error {
	gw, err := prepareWrite(ctx, uc.factory, uc.vault, uc.disco, in)
	if err != nil {
		return err
	}
	return gw.Block(ctx, in.MAC)
}

// UnblockDevice removes a MAC from the router's block list. Same safety posture
// as BlockDevice.
type UnblockDevice struct {
	factory port.RouterFactory
	vault   port.CredentialVault
	disco   port.DiscoveryPort
	clock   port.Clock
	log     port.Logger
}

// NewUnblockDevice wires the interactor.
func NewUnblockDevice(f port.RouterFactory, v port.CredentialVault, d port.DiscoveryPort, c port.Clock, l port.Logger) *UnblockDevice {
	return &UnblockDevice{factory: f, vault: v, disco: d, clock: c, log: l}
}

// Execute unblocks the MAC.
func (uc *UnblockDevice) Execute(ctx context.Context, in ChangeAccessInput) error {
	gw, err := prepareWrite(ctx, uc.factory, uc.vault, uc.disco, in)
	if err != nil {
		return err
	}
	return gw.Unblock(ctx, in.MAC)
}

// prepareWrite validates confirmation, resolves the adapter and credentials, and
// returns a logged-in gateway ready for a write.
func prepareWrite(ctx context.Context, factory port.RouterFactory, vault port.CredentialVault, disco port.DiscoveryPort, in ChangeAccessInput) (port.RouterPort, error) {
	if !in.Confirm {
		return nil, errConfirm
	}
	if in.MAC.IsZero() {
		return nil, domain.ErrInvalidMAC
	}
	return loggedInGateway(ctx, factory, vault, disco, routerTarget{in.BaseURL, in.AdapterID, in.Opts, in.Creds})
}

// routerTarget identifies the router to talk to and how.
type routerTarget struct {
	BaseURL   string
	AdapterID string
	Opts      port.RouterOptions
	Creds     domain.Credentials // optional override of the stored credentials
}

// loggedInGateway resolves the adapter and credentials and returns a gateway
// that has completed Login.
func loggedInGateway(ctx context.Context, factory port.RouterFactory, vault port.CredentialVault, disco port.DiscoveryPort, in routerTarget) (port.RouterPort, error) {
	adapterID, _, err := resolveAdapterID(ctx, disco, in.BaseURL, in.AdapterID, in.Opts)
	if err != nil {
		return nil, err
	}
	creds, err := loadCreds(ctx, vault, in.BaseURL, in.Creds)
	if err != nil {
		return nil, err
	}
	gw, err := factory.New(adapterID, in.BaseURL, in.Opts)
	if err != nil {
		return nil, err
	}
	if err := gw.Login(ctx, creds); err != nil {
		return nil, err
	}
	return gw, nil
}
