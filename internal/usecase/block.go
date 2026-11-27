package usecase

import (
	"context"
	"errors"
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
	purge   *PurgeStaleRules // nil: a full list is reported as is
	self    port.SelfLocator // nil: no device to spare (selfguard.go)
}

// NewBlockDevice wires the interactor.
func NewBlockDevice(f port.RouterFactory, v port.CredentialVault, d port.DiscoveryPort, c port.Clock, l port.Logger) *BlockDevice {
	return &BlockDevice{factory: f, vault: v, disco: d, clock: c, log: l}
}

// WithPurge makes a block the box refuses because its list is full first
// remove the rules of the devices away 30 days, then try once more (RFC-007
// D6). It returns uc.
func (uc *BlockDevice) WithPurge(p *PurgeStaleRules) *BlockDevice {
	uc.purge = p
	return uc
}

// WithSelf makes the block refuse the device running Kergui, found at the
// address self gives (selfguard.go): on the F6600P nobody checked whether a
// firewall rule also cuts the box's own pages, so it is refused all the
// same. It returns uc.
func (uc *BlockDevice) WithSelf(self port.SelfLocator) *BlockDevice {
	uc.self = self
	return uc
}

// Execute blocks the MAC.
func (uc *BlockDevice) Execute(ctx context.Context, in ChangeAccessInput) error {
	gw, err := prepareWrite(ctx, uc.factory, uc.vault, uc.disco, in)
	if err != nil {
		return err
	}
	if err := refuseSelf(ctx, uc.self, gw, in.BaseURL, in.MAC); err != nil {
		return err
	}
	err = gw.Block(ctx, in.MAC)
	if errors.Is(err, domain.ErrAccessListFull) && uc.purge != nil {
		rep, perr := uc.purge.purge(ctx, gw, in.BaseURL, true)
		if perr != nil {
			uc.log.Warnf("purge before the block of %s: %v", in.MAC, perr)
		}
		if len(rep.Removed) > 0 {
			err = gw.Block(ctx, in.MAC)
		}
	}
	if errors.Is(err, domain.ErrAccessListFull) {
		uc.log.Warnf("La liste de la box est pleine : %s n'a pas pu être bloqué", in.MAC)
	}
	if err != nil {
		return err
	}
	return ensureFilterOn(ctx, gw, uc.log)
}

// ensureFilterOn switches the router's global MAC filter on when it is off:
// rules in the list have no effect until it is, so a cut would silently do
// nothing.
func ensureFilterOn(ctx context.Context, gw port.RouterPort, log port.Logger) error {
	sw, ok := gw.(port.MACFilterSwitch)
	if !ok {
		return nil
	}
	on, err := sw.MACFilterEnabled(ctx)
	if errors.Is(err, domain.ErrNotImplemented) {
		return nil
	}
	if err != nil {
		return err
	}
	if on {
		return nil
	}
	log.Warnf("MAC filter was disabled on the router: enabling it so the cut takes effect")
	return sw.SetMACFilterEnabled(ctx, true)
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
