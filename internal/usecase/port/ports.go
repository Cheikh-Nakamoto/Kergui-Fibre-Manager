// Package port declares the interfaces (ports) that the use-case layer depends
// on. Following the Dependency Rule, these are owned by the application core and
// implemented by the outer adapter ring (router gateways, SQLite, the CLI). The
// core therefore never imports a concrete framework.
package port

import (
	"context"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// RouterOptions tune how a gateway talks to a router.
type RouterOptions struct {
	// InsecureTLS skips verification of the router's (typically self-signed)
	// certificate. It applies ONLY to the LAN connection to the router.
	InsecureTLS bool
	// Timeout bounds each HTTP request. Zero means the gateway default.
	Timeout time.Duration
}

// RouterPort is the contract every router adapter implements (project brief §9,
// there called RouterAdapter). The rest of the application talks only to this
// interface and never sees a router's HTTP/HTML details.
//
// Milestone 1 is read-only: Block/Unblock return domain.ErrNotImplemented.
type RouterPort interface {
	Login(ctx context.Context, c domain.Credentials) error
	RouterInfo(ctx context.Context) (*domain.RouterInfo, error)
	Devices(ctx context.Context) ([]domain.Device, error)
	AccessRules(ctx context.Context) ([]domain.AccessRule, error)
	Block(ctx context.Context, mac domain.MAC) error
	Unblock(ctx context.Context, mac domain.MAC) error
	Meta() domain.AdapterMeta
}

// RouterFactory builds a RouterPort for a given adapter id and base URL. It is
// the seam that keeps the modular, self-registering adapter set out of the core.
type RouterFactory interface {
	// New returns a gateway for adapterID bound to baseURL. adapterID must be a
	// registered id; callers resolve "" via discovery first.
	New(adapterID, baseURL string, opts RouterOptions) (RouterPort, error)
	// Available lists the metadata of every registered adapter.
	Available() []domain.AdapterMeta
	// Lookup returns one adapter's metadata.
	Lookup(adapterID string) (domain.AdapterMeta, bool)
}

// DiscoveryPort performs non-destructive fingerprinting of a router (brief §15).
type DiscoveryPort interface {
	Discover(ctx context.Context, baseURL string, opts RouterOptions) (domain.RouterInfo, error)
}

// DeviceRepository persists the device inventory. Identity is (routerID, MAC).
type DeviceRepository interface {
	// Upsert inserts or updates a device, returning created=true on first sight.
	Upsert(ctx context.Context, d domain.Device) (created bool, err error)
	ListByRouter(ctx context.Context, routerID string) ([]domain.Device, error)
	FindByMAC(ctx context.Context, routerID string, mac domain.MAC) (domain.Device, bool, error)
	// SetCustomName sets the user's label for a device; ErrDeviceNotFound if the
	// device is not yet in the inventory.
	SetCustomName(ctx context.Context, routerID string, mac domain.MAC, name string) error
}

// AccessRuleRepository persists access-control rules (kept separate from devices).
type AccessRuleRepository interface {
	ReplaceForRouter(ctx context.Context, routerID string, rules []domain.AccessRule) error
	ListByRouter(ctx context.Context, routerID string) ([]domain.AccessRule, error)
}

// RouterRepository persists managed routers (never their credentials).
type RouterRepository interface {
	Upsert(ctx context.Context, r domain.Router) error
	Get(ctx context.Context, id string) (domain.Router, bool, error)
	List(ctx context.Context) ([]domain.Router, error)
}

// CredentialVault stores router credentials encrypted at rest (brief §12). No
// method ever returns a password to anything but the caller that must use it.
type CredentialVault interface {
	Store(ctx context.Context, routerID string, c domain.Credentials) error
	Load(ctx context.Context, routerID string) (domain.Credentials, bool, error)
	Delete(ctx context.Context, routerID string) error
}

// VendorLookup resolves a MAC OUI to a vendor name ("" when unknown). A richer
// OUI database can be slotted in later without touching the core.
type VendorLookup interface {
	Vendor(mac domain.MAC) string
}

// Clock abstracts time for testability.
type Clock interface {
	Now() time.Time
}

// Logger is a minimal structured-ish logging port.
type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}
