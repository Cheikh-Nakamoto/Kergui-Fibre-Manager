// Package port declares the interfaces (ports) that the use-case layer depends
// on. Following the Dependency Rule, these are owned by the application core and
// implemented by the outer adapter ring (router gateways, SQLite, the CLI). The
// core therefore never imports a concrete framework.
package port

import (
	"context"
	"syscall"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// RouterOptions tune how a gateway talks to a router.
type RouterOptions struct {
	// InsecureTLS skips verification of the router's (typically self-signed)
	// certificate. It applies ONLY to the LAN connection to the router.
	InsecureTLS bool
	// PinSHA256, when set together with InsecureTLS, is the hex SHA-256 of the
	// router's leaf certificate (trust on first use): a self-signed cert is
	// accepted only if it is still the one recorded the first time.
	PinSHA256 string
	// Timeout bounds each HTTP request. Zero means the gateway default.
	Timeout time.Duration
	// SkipWriteVerify disables the read-after-write verification that confirms a
	// block/unblock actually took effect on the router. Use only as an operator
	// escape hatch when the router applies writes asynchronously.
	SkipWriteVerify bool
	// Logger receives protocol-level traces from the gateway. Nil disables them.
	Logger Logger
	// DialControl, when set, runs on every socket to the router before it
	// connects (net.Dialer.Control). A phone relay binds it to the home
	// Wi-Fi with it (#45): Go's sockets do not follow Android's
	// bindProcessToNetwork. An error refuses the connection.
	DialControl func(network, address string, c syscall.RawConn) error
	// Preempt, when set, decides whether to evict another admin session the
	// router reports at login. holder is how the router names it, e.g.
	// "192.168.1.17(user)". false leaves that session alone and the login
	// fails with domain.ErrSessionHeld. Nil always evicts: the owner's app
	// wins the box. A relay sets it to yield to the owner at home (#94).
	Preempt func(holder string) bool
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

// LabelRepository keeps the household's labels of the devices — names and
// notes with when they were set (#95) — so a relay and the owner's phone can
// exchange them. Identity is (routerID, MAC).
type LabelRepository interface {
	// Labels lists the devices that carry a label.
	Labels(ctx context.Context, routerID string) ([]domain.Label, error)
	// ApplyLabels applies labels made elsewhere, last write wins, and returns
	// the MACs whose label changed.
	ApplyLabels(ctx context.Context, routerID string, labels []domain.Label) ([]domain.MAC, error)
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

// MACFilterModeReader is an optional RouterPort capability: the MAC filter's
// mode next to its switch. Mode is "Discard" (blocklist) or "Permit"
// (allowlist), "" when the box does not say.
type MACFilterModeReader interface {
	MACFilterMode(ctx context.Context) (enabled bool, mode string, err error)
}

// WiFiAccessControl is an optional RouterPort capability: refuse a device at the
// Wi-Fi access point itself, so it is disconnected rather than just cut from
// the internet.
type WiFiAccessControl interface {
	WiFiBlockedMACs(ctx context.Context) ([]domain.MAC, error)
	WiFiBlock(ctx context.Context, mac domain.MAC) error
	WiFiUnblock(ctx context.Context, mac domain.MAC) error
}

// WiFiRuleReader is an optional RouterPort capability: the Wi-Fi refusals one
// rule at a time (a rule on an access point that refuses its devices), each
// with its access point in SSID and whether kergui wrote it (ByKergui). It
// only reads (RFC-007 D6).
type WiFiRuleReader interface {
	WiFiRules(ctx context.Context) ([]domain.AccessRule, error)
}

// RulePurgeRepository remembers when the stale rules of each box were last
// purged, so the purge runs once a day (RFC-007 D6). Zero when never.
type RulePurgeRepository interface {
	LastRulePurge(ctx context.Context, routerID string) (time.Time, error)
	SaveRulePurge(ctx context.Context, routerID string, at time.Time) error
}

// WiFiSSIDManager is an optional RouterPort capability: list the router's Wi-Fi
// networks and switch them on or off (with a new name or password if wanted).
type WiFiSSIDManager interface {
	WiFiSSIDs(ctx context.Context) ([]domain.WiFiSSID, error)
	SetWiFiSSID(ctx context.Context, id string, change domain.WiFiSSIDChange) error
}

// AdminPasswordChanger is an optional RouterPort capability: set the router's
// admin password when the router demands it, as a new F6600P does at the
// first sign-in with the password on its label (Login then answers
// domain.ErrPasswordChangeRequired). It logs in with current, changes the
// password to newPassword (same username), then logs in again with it and
// leaves that session open. It never cancels the router's demand.
type AdminPasswordChanger interface {
	ChangeAdminPassword(ctx context.Context, current domain.Credentials, newPassword string) error
}

// BoxIdentifier is an optional RouterPort capability: the box serial number,
// upper-cased (e.g. "ZTEG1A2B3C4D"), the stable identity a Pro pass binds to
// (RFC-006 "Box identity"). An adapter without it is a model not supported for
// Pro yet; the free tier works normally on it.
type BoxIdentifier interface {
	SerialNumber(ctx context.Context) (string, error)
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
// accumulated per billing period and per day (days kept
// domain.UsageRetentionMonths).
type UsageRepository interface {
	LastSamples(ctx context.Context, routerID string) (map[domain.MAC]domain.TrafficCounters, error)
	// RecordSamples atomically stores the new samples and adds each delta to
	// that device's usage for the month and the day of at, split by
	// direction, and drops the days past the retention.
	RecordSamples(ctx context.Context, routerID string, at time.Time, samples map[domain.MAC]domain.TrafficCounters, deltas map[domain.MAC]domain.UsageDelta) error
	UsageByPeriod(ctx context.Context, routerID, period string) ([]domain.Usage, error)
	// DeviceMonths adds a device's days up per month for the months
	// from..to (YYYY-MM, included), oldest first; empty months are absent.
	DeviceMonths(ctx context.Context, routerID string, mac domain.MAC, from, to string) ([]domain.Usage, error)
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

// AutoRejectRepository stores the auto-reject setting of each box and the
// devices it tracks (RFC-006, Max).
type AutoRejectRepository interface {
	AutoRejectSettings(ctx context.Context, routerID string) (domain.AutoRejectSettings, error)
	SaveAutoRejectSettings(ctx context.Context, routerID string, s domain.AutoRejectSettings) error
	AutoRejectDevices(ctx context.Context, routerID string) (map[domain.MAC]domain.AutoRejectDevice, error)
	SaveAutoRejectDevice(ctx context.Context, routerID string, d domain.AutoRejectDevice) error
	// ClearAutoRejectDevices forgets every device tracked for the box.
	ClearAutoRejectDevices(ctx context.Context, routerID string) error
}

// SelfLocator tells which address the machine running Kergui has on the
// box's network: on a phone, the phone itself, which auto-reject must never
// refuse. The zero IP means it could not tell.
type SelfLocator interface {
	SelfIP(ctx context.Context, baseURL string) domain.IP
}
