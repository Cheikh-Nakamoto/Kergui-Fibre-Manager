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
	// SkipWriteVerify disables the read-after-write verification that confirms a
	// block/unblock actually took effect on the router. Use only as an operator
	// escape hatch when the router applies writes asynchronously.
	SkipWriteVerify bool
	// Logger receives protocol-level traces from the gateway. Nil disables them.
	Logger Logger
}

// RouterPort is the contract every router adapter implements (project brief §9,
// there called RouterAdapter). The rest of the application talks only to this
// interface and never sees a router's HTTP/HTML details.
//
// Block/Unblock are gated by each model's WriteReady flag; a model without a
// wired write path returns domain.ErrNotImplemented.
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

// MACFilterSwitch is an optional RouterPort capability: a global on/off switch
// for the MAC filter. Rules in the list only take effect while it is enabled.
type MACFilterSwitch interface {
	MACFilterEnabled(ctx context.Context) (bool, error)
	// SetMACFilterEnabled turns the filter on (always in blocklist mode, never
	// allowlist, which would cut every unlisted device) or off.
	SetMACFilterEnabled(ctx context.Context, enabled bool) error
}

// WiFiAccessControl is an optional RouterPort capability: refuse a device at the
// Wi-Fi access point itself, so it is disconnected rather than just cut from
// the internet.
type WiFiAccessControl interface {
	WiFiBlockedMACs(ctx context.Context) ([]domain.MAC, error)
	WiFiBlock(ctx context.Context, mac domain.MAC) error
	WiFiUnblock(ctx context.Context, mac domain.MAC) error
}

// WiFiSSIDManager is an optional RouterPort capability: list the router's Wi-Fi
// networks and switch them on or off (with a new name or password if wanted).
type WiFiSSIDManager interface {
	WiFiSSIDs(ctx context.Context) ([]domain.WiFiSSID, error)
	SetWiFiSSID(ctx context.Context, id string, change domain.WiFiSSIDChange) error
}

// TrafficMeter is an optional RouterPort capability: per-device byte counters.
type TrafficMeter interface {
	TrafficCounters(ctx context.Context) (map[domain.MAC]domain.TrafficCounters, error)
}

// HostObserver is an optional RouterPort capability: everything one poll of
// the router says about each station (Wi-Fi association, DHCP lease, radio
// capabilities), merged per MAC. It feeds device recognition, not the
// inventory.
type HostObserver interface {
	Observe(ctx context.Context) ([]domain.Observation, error)
}

// UsageRepository stores the last counter sample per device and the bytes
// accumulated per billing period.
type UsageRepository interface {
	LastSamples(ctx context.Context, routerID string) (map[domain.MAC]domain.TrafficCounters, error)
	// RecordSamples atomically stores the new samples and adds each delta to
	// that device's usage for period.
	RecordSamples(ctx context.Context, routerID, period string, samples map[domain.MAC]domain.TrafficCounters, deltas map[domain.MAC]uint64) error
	UsageByPeriod(ctx context.Context, routerID, period string) ([]domain.Usage, error)
}

// DeviceHistory lists every device ever recorded with its consumption.
type DeviceHistory interface {
	// ListWithUsage returns one page of devices (most recently seen first),
	// optionally filtered by query (name, hostname, MAC or IP), with their usage
	// for period, and the total number of matching devices.
	ListWithUsage(ctx context.Context, routerID, period, query string, limit, offset int) ([]domain.DeviceUsage, int, error)
}

// CheckoutRequest asks a payment provider for a hosted checkout.
type CheckoutRequest struct {
	Label        string
	Description  string
	AmountXOF    int64
	CustomerName string
	Phone        string // +221XXXXXXXXX
	SuccessURL   string
	ErrorURL     string
}

// CheckoutResult is what the provider hands back.
type CheckoutResult struct {
	OrderID     string
	CheckoutURL string
	AmountXOF   int64
}

// PaymentGateway is a hosted-checkout payment provider (NabooPay).
type PaymentGateway interface {
	CreateCheckout(ctx context.Context, req CheckoutRequest) (CheckoutResult, error)
	TransactionStatus(ctx context.Context, orderID string) (domain.PaymentStatus, error)
	CancelCheckout(ctx context.Context, orderID string) error
}

// PaymentRepository persists payment requests.
type PaymentRepository interface {
	Create(ctx context.Context, p domain.Payment) error
	Update(ctx context.Context, p domain.Payment) error
	Get(ctx context.Context, id string) (domain.Payment, bool, error)
	ListByMAC(ctx context.Context, routerID string, mac domain.MAC, limit int) ([]domain.Payment, error)
	ListByStatus(ctx context.Context, status domain.PaymentStatus, limit int) ([]domain.Payment, error)
	// PaidBytes is the consumption already covered by paid payments.
	PaidBytes(ctx context.Context, routerID string, mac domain.MAC, period string) (uint64, error)
	// ListByPeriod returns the payments of a kind for one period, newest first.
	ListByPeriod(ctx context.Context, routerID, period string, kind domain.PaymentKind) ([]domain.Payment, error)
}

// MemberRepository persists members and which devices belong to whom.
type MemberRepository interface {
	Create(ctx context.Context, m domain.Member) error
	Update(ctx context.Context, m domain.Member) error
	Delete(ctx context.Context, routerID, id string) error
	Get(ctx context.Context, routerID, id string) (domain.Member, bool, error)
	// List returns all members with their MACs, by name.
	List(ctx context.Context, routerID string) ([]domain.Member, error)
	// Assign moves a device to a member; memberID "" unassigns it.
	Assign(ctx context.Context, routerID string, mac domain.MAC, memberID string) error
}

// CutRepository remembers which devices were cut for an unpaid invoice.
type CutRepository interface {
	// Cuts maps each cut device to the payment that caused the cut.
	Cuts(ctx context.Context, routerID string) (map[domain.MAC]string, error)
	RecordCut(ctx context.Context, routerID string, mac domain.MAC, paymentID string) error
	RemoveCut(ctx context.Context, routerID string, mac domain.MAC) error
}

// SettingsRepository stores admin-tunable settings.
type SettingsRepository interface {
	BillingSettings(ctx context.Context) (domain.BillingSettings, error)
	SaveBillingSettings(ctx context.Context, s domain.BillingSettings) error
}

// IdentityRepository stores identity flags and blacklist cuts.
type IdentityRepository interface {
	Identities(ctx context.Context, routerID string) (map[string]domain.IdentityState, error)
	MarkShared(ctx context.Context, routerID, identity string) error
	SetBlacklisted(ctx context.Context, routerID, identity string, at time.Time) error
	Lift(ctx context.Context, routerID, identity string, at time.Time) error
	BlacklistCuts(ctx context.Context, routerID string) (map[domain.MAC]string, error)
	RecordBlacklistCut(ctx context.Context, routerID string, mac domain.MAC, identity string) error
	RemoveBlacklistCut(ctx context.Context, routerID string, mac domain.MAC) error
}

// ReminderRepository remembers when a device postponed a payment link.
type ReminderRepository interface {
	// Snoozes maps payment ids to the time their reminder may show again.
	Snoozes(ctx context.Context, routerID string) (map[string]time.Time, error)
	Snooze(ctx context.Context, routerID, paymentID string, until time.Time) error
}

// LogEntry is one recorded log line.
type LogEntry struct {
	Seq   int64     `json:"seq"`
	Time  time.Time `json:"time"`
	Level string    `json:"level"`
	Msg   string    `json:"msg"`
}

// LogReader exposes recent log lines, e.g. to a dashboard.
type LogReader interface {
	// LogsSince returns entries with Seq > since, oldest first, at most limit.
	LogsSince(since int64, limit int) []LogEntry
}
