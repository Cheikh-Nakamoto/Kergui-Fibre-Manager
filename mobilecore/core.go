// Package mobilecore is the composition root of the mobile app: the Go core
// compiled into a native library with `gomobile bind` (an .aar on Android, an
// .xcframework on iOS). There is no server in v1 — the phone talks to the box
// directly on the home Wi-Fi.
//
// Rather than inventing a second API, the core serves the same JSON API as
// `kergui serve` (internal/adapter/controller/httpapi), in memory: the app calls
// Request(method, path, body) and gets the status and body the dashboard would
// get, with the same error mapping (501 not supported, 502 box unreachable...).
// No socket is opened for it. Only the routes the v1 app needs are reachable;
// billing, members, payments, the captive portal and the static dashboard are
// not. Usage (the Conso tab) is reachable, and sampled by the app itself:
// there is no server to do it. Unlike `kergui serve`, the paid writes are
// gated by the entitlement token the app hands over (SetPublicKeys,
// SetEntitlement; RFC-006).
//
// Every exported signature sticks to the types gomobile can bind: string, int,
// bool, error, pointers to structs of those, and interfaces.
package mobilecore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/controller/httpapi"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/discovery"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/vault"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/catalog"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/httpkit"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/vendor"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/clock"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/logging"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// version is overridable via -ldflags "-X .../mobilecore.version=...".
var version = "0.1.0-dev"

// Version returns the core version, shown in the app's settings.
func Version() string { return version }

// DemoUsername and DemoPassword are the credentials the embedded demo router
// accepts. They are public on purpose: store reviewers use them.
const (
	DemoUsername = "admin"
	DemoPassword = "admin"
)

// Timeouts. A phone is held in someone's hand: a call that cannot finish
// has to say so rather than spin.
const (
	// requestTimeout bounds one Request, router round trips included. A
	// login plus a read is a handful of round trips at routerTimeout each,
	// and anything beyond this is a box that is not answering.
	requestTimeout = 30 * time.Second
	// routerTimeout bounds one HTTP round trip to the box. Without it the
	// router client fell back to httpkit's own default, which is the same
	// 15 s for a LAN that either answers in milliseconds or not at all.
	routerTimeout = 10 * time.Second
	// fingerprintTimeout bounds the TLS probe of the connection screen.
	fingerprintTimeout = 8 * time.Second
)

// journalLimit is how many entries Journal returns when the app asks for no
// limit: enough to see a connection attempt and what led to it.
const journalLimit = 500

// routes are the API patterns the v1 app may reach. Anything else answers 404.
var routes = []string{
	"GET /api/health",
	"GET /api/discover",
	"GET /api/devices",
	"GET /api/inspect",
	"PATCH /api/devices/{mac}",
	"POST /api/devices/{mac}/block",
	"POST /api/devices/{mac}/unblock",
	"GET /api/wifi/blocked",
	"POST /api/devices/{mac}/wifi-block",
	"POST /api/devices/{mac}/wifi-unblock",
	"GET /api/wifi/ssids",
	"PATCH /api/wifi/ssids/{id}",
	"GET /api/filter",
	"POST /api/filter/toggle",
	"POST /api/filter/setup",
	"POST /api/login",
	// A new box demands a new admin password after the first sign-in (#92):
	// /api/login answers 409 password_change_required, this sets it.
	"POST /api/box/first-password",
	// The Conso tab (free): the current month per device, and the sample the
	// app takes on each refresh and from its watch service.
	"GET /api/usage",
	"POST /api/usage/sample",
	// Max: another month (GET /api/usage?period=) and one device's last
	// months, refused by the gate without it.
	"GET /api/usage/devices/{mac}",
	// Auto-reject (Max to turn on; off, a round and accepting a device are
	// free): the setting, one round from the watch service, and "Accepter".
	"GET /api/autoreject",
	"PUT /api/autoreject",
	"POST /api/autoreject/run",
	"POST /api/devices/{mac}/accept",
	// Who enforces it (#36): the hub's last answer, handed over by the app,
	// which the watch service's rounds go by.
	"PUT /api/autoreject/enforcer",
	// The app's own journal screen: the core's recent log lines, including
	// one per API call with its status and duration.
	"GET /api/logs",
}

// Logger receives the core's log lines. The native module forwards them to
// Logcat / os_log. Implemented on the Kotlin / Swift side.
type Logger interface {
	Log(level, message string)
}

// Response is the outcome of a Request: an HTTP status and a JSON body.
type Response struct {
	Status int
	Body   string
}

// Core owns the local database and the API handler for one router.
type Core struct {
	mu      sync.RWMutex
	db      *sqlite.DB
	svc     httpapi.Services
	handler http.Handler // nil until Configure
	// routerURL is the box Configure pointed at, whose login the vault
	// keeps under usecase.RouterID (credentials.go seals it).
	routerURL string
	demo      *http.Server
	demoURL   string
	// demoStop ends the demo's traffic while the demo runs.
	demoStop chan struct{}
	// demoBox is the demo router while it runs.
	demoBox *mockrouter.F6600P
	log     port.Logger
	// journal is the in-memory ring buffer the app's journal screen reads.
	// It is the same buffer `kergui serve` shows in its dashboard, kept here
	// because a household debugging a connection on the spot cannot read
	// Logcat or a Mac's console.
	journal *logging.Logger
	// gate refuses the paid writes the entitlement does not cover
	// (entitlement.go).
	gate *coreGate
	// clock dates the usage samples (which month they count in). Real time,
	// swapped by the tests.
	clock *swapClock
	// usage is the consumption store, which the demo seeds with past months.
	usage *sqlite.UsageRepo
	// self finds the phone's own address on the box's network, which
	// auto-reject never refuses. Swapped by the tests.
	self *selfLocator
	// vault holds the box password, and the owner's keys (owner.go).
	vault *vault.AESGCMVault
	// ownerMu guards the owner's state, loaded from the vault once.
	ownerMu sync.Mutex
	owner   *ownerState
	ownerID *trust.Identity
	// member and memberID are this phone's member key (member.go), under
	// the same lock.
	member   *memberState
	memberID *trust.Identity
	// recoveryClient talks to a relay on the home network (recovery.go);
	// nil is a plain client. Swapped by the tests.
	recoveryClient *http.Client
	// routerCfg is what Configure pointed the API at: the relay builds its
	// own handler on it (relayrun.go).
	routerCfg httpapi.Config
	// approvals are the devices the household approved, per box (#58).
	approvals *approvalStore
	// owners are the owner's phones the hub lists, per box and per
	// household (#87).
	owners *ownerStore
	relayFields
}

// swapClock is a port.Clock whose source can be replaced (tests).
type swapClock struct {
	mu  sync.RWMutex
	now func() time.Time
}

func (c *swapClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now()
}

func (c *swapClock) set(now func() time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

// Open opens (or creates) the local database in dataDir. vaultKey encrypts the
// stored router credentials; the app generates it once and keeps it in the
// Keychain / Keystore. logger may be nil.
func Open(dataDir, vaultKey string, logger Logger) (*Core, error) {
	if strings.TrimSpace(vaultKey) == "" {
		return nil, errors.New("mobilecore: empty vault key")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("mobilecore: data dir: %w", err)
	}
	db, err := sqlite.Open(filepath.Join(dataDir, "kergui.db"))
	if err != nil {
		return nil, fmt.Errorf("mobilecore: open database: %w", err)
	}
	// Everything is kept in a ring buffer, and also forwarded to the
	// platform's log when the app wired one. io.Discard because there is no
	// console on a phone; verbose because the journal is what the household
	// will be asked to send when something does not work.
	journal := logging.New(io.Discard, true)
	log := port.Logger(journal)
	if logger != nil {
		log = teeLogger{journal, bridgeLogger{logger}}
	}

	// One serialised session per router: the box allows a single admin login.
	factory := router.NewSharedFactory(catalog.New())
	clk := clock.Real{}
	disco := discovery.New(factory, clk)
	vlt := vault.New(sqlite.NewCredentialStore(db), vaultKey)
	devices := sqlite.NewDeviceRepo(db)

	meterClock := &swapClock{now: time.Now}
	usage := sqlite.NewUsageRepo(db)
	self := &selfLocator{find: routeIP}
	c := &Core{db: db, log: log, journal: journal, clock: meterClock, usage: usage, self: self, vault: vlt, approvals: openApprovals(dataDir), owners: openOwners(dataDir)}
	c.svc = httpapi.Services{
		Discover: usecase.NewDiscoverRouter(disco),
		Auth:     usecase.NewAuthenticate(factory, vlt, sqlite.NewRouterRepo(db), disco, clk, log),
		List:     usecase.NewListDevices(factory, vlt, devices, vendor.New(nil), disco, clk, log),
		Inspect:  usecase.NewInspectRouter(factory, disco, vlt, clk, log),
		Block:    usecase.NewBlockDevice(factory, vlt, disco, clk, log),
		Unblock:  usecase.NewUnblockDevice(factory, vlt, disco, clk, log),
		Rename:   usecase.NewRenameDevice(devices),
		Filter:   usecase.NewMACFilter(factory, vlt, disco, log),
		WiFi:     usecase.NewWiFiAccess(factory, vlt, disco),
		Observe:  usecase.NewObserveHosts(factory, vlt, disco, log),
		// Free: the current month's consumption, measured only while the
		// app or its watch service runs (the phone is the only sampler).
		Meter: usecase.NewMetering(factory, vlt, disco, usage, meterClock, log),
		// Max: refuse at the Wi-Fi access control the devices nobody
		// accepted within 15 min, sparing the phone itself. The watch
		// service runs it; the setting is kept here for both.
		AutoReject: usecase.NewAutoReject(factory, vlt, disco, devices, sqlite.NewAutoRejectRepo(db), self, meterClock, log),
		Logger:     log,
		Logs:       journal,
	}
	c.gate = newGate(c.svc.Inspect, log)
	c.svc.Gate = c.gate
	return c, nil
}

// Configure points the core at a router. adapterID may be empty (auto-detect).
// pinSHA256 is the fingerprint returned by CertificateFingerprint the first time
// (trust on first use); it is ignored for plain-HTTP routers.
func (c *Core) Configure(routerURL, adapterID, pinSHA256 string) error {
	u, err := url.Parse(strings.TrimSpace(routerURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("mobilecore: invalid router URL %q", routerURL)
	}
	// The adapter's own traces (session takeover, re-login) go to the journal
	// and logcat: without them a box that refuses the session is a black box.
	// DialControl binds the sockets to the home Wi-Fi while this phone
	// relays (relayrun.go); otherwise it does nothing.
	opts := port.RouterOptions{Timeout: routerTimeout, Logger: c.log, DialControl: c.dialControl}
	if u.Scheme == "https" {
		// Home routers ship self-signed certificates: accept it, but only the
		// one recorded on first use.
		opts.InsecureTLS = true
		opts.PinSHA256 = pinSHA256
	}
	cfg := httpapi.Config{BaseURL: u.String(), AdapterID: adapterID, Opts: opts, Version: version}
	c.mu.Lock()
	c.handler = c.withApprovals(buildHandler(c.svc, cfg))
	c.routerURL = u.String()
	c.routerCfg = cfg
	c.mu.Unlock()
	// Another box: its serial is read again before the next paid action.
	c.gate.retarget(usecase.InspectInput{BaseURL: u.String(), AdapterID: adapterID, Opts: opts}, false)
	// The URL and whether the certificate is pinned, never a credential:
	// this line is the first thing to look at when a box stops answering.
	c.log.Infof("core: pointed at %s (pinned=%t, adapter=%s)", u.Redacted(), opts.PinSHA256 != "", orAuto(adapterID))
	return nil
}

// orAuto names the adapter in a log line.
func orAuto(adapterID string) string {
	if adapterID == "" {
		return "auto"
	}
	return adapterID
}

func buildHandler(svc httpapi.Services, cfg httpapi.Config) http.Handler {
	inner := httpapi.New(svc, cfg).Handler()
	mux := http.NewServeMux()
	for _, p := range routes {
		mux.Handle(p, inner)
	}
	return mux
}

// Request runs one API call in memory. path may carry a query string; body is
// JSON or empty. A non-nil error means the call could not be made at all
// (core not configured, bad method); API errors come back as a Response with a
// 4xx/5xx status and {"error": "..."}.
func (c *Core) Request(method, path, body string) (*Response, error) {
	c.mu.RLock()
	h := c.handler
	c.mu.RUnlock()
	if h == nil {
		return nil, errors.New("mobilecore: not configured (call Configure or StartDemo first)")
	}
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("mobilecore: path must start with /: %q", path)
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), "http://core.local"+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	// One call at a time reaches the box, the relay's included.
	c.io.Lock()
	h.ServeHTTP(rec, req)
	c.io.Unlock()
	return &Response{Status: rec.status, Body: rec.body.String()}, nil
}

// Journal returns the core's recent log lines as a JSON array, oldest
// first: {"seq","time","level","msg"}. since is the last sequence number the
// app already has (0 for everything kept), limit caps the answer.
//
// It works before Configure, which is the point: the connection screen's own
// failures — nothing listening on 443, a certificate refused, a box that
// answers but is not a router it knows — are exactly what a household needs
// to be able to show someone.
func (c *Core) Journal(since int64, limit int) (string, error) {
	if limit <= 0 {
		limit = journalLimit
	}
	entries := c.journal.LogsSince(since, limit)
	out, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("mobilecore: encode journal: %w", err)
	}
	return string(out), nil
}

// Note records one line from the app in the same journal. The app uses it for
// what only it knows: which address the household typed, that a certificate
// was refused, that a screen gave up waiting.
//
// It is for diagnostics, so it is lenient about the level and strict about
// what belongs in it: never a password, never a pairing code. Callers keep
// that promise; this only enforces a length.
func (c *Core) Note(level, message string) {
	const maxNote = 500
	if len(message) > maxNote {
		message = message[:maxNote] + "…"
	}
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error":
		c.log.Errorf("app: %s", message)
	case "warn", "warning":
		c.log.Warnf("app: %s", message)
	case "debug":
		c.log.Debugf("app: %s", message)
	default:
		c.log.Infof("app: %s", message)
	}
}

// CertificateFingerprint connects to an https router and returns the hex
// SHA-256 of the certificate it presents, without trusting it. The app shows
// it once, stores it, and passes it to Configure from then on.
func CertificateFingerprint(routerURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(routerURL))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", fmt.Errorf("mobilecore: not an https URL: %q", routerURL)
	}
	return httpkit.ProbeFingerprint(context.Background(), routerURL, fingerprintTimeout)
}

// StartDemo starts the bundled mock router on 127.0.0.1 and points the core at
// it. Log in with DemoUsername / DemoPassword. This is what store reviewers
// use: they have no ZTE box. (It returns only an error so that Swift imports
// it as `throws`.)
func (c *Core) StartDemo() error {
	// The demo never relays: it has no hub, and its box is not the
	// household's.
	c.RelayStop()
	c.StopDemo()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("mobilecore: demo listener: %w", err)
	}
	// The demo is the F6600P: the model households have, and the one that
	// shows every feature (serial number, Wi-Fi access control, per-device
	// traffic counters, SSIDs).
	box := mockrouter.NewF6600P(DemoUsername, DemoPassword)
	// A fresh box, as a household's arrives: the MAC filter off, so the
	// app's setup step (#56) shows it being switched on.
	box.SetMACFilter(false, "Discard")
	// « Ce téléphone » (#86): the demo box lists the phone running the app,
	// as a real box does, so the Devices tab shows it hidden.
	box.Join(mockrouter.Station{MAC: DemoPhoneMAC, Hostname: demoPhoneName, IP: demoPhoneIP})
	// The owner's other phone (#87): the demo household's owner has two,
	// this one and DemoOwnerPhoneMAC, as the hub would list them.
	box.Join(mockrouter.Station{MAC: DemoOwnerPhoneMAC, Hostname: demoOwnerPhoneName, IP: demoOwnerPhoneIP})
	srv := &http.Server{Handler: box.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	stop := make(chan struct{})
	go demoTraffic(box, stop, demoTrafficEvery)
	c.mu.Lock()
	c.demo = srv
	c.demoURL = "http://" + ln.Addr().String()
	c.demoStop = stop
	c.demoBox = box
	c.mu.Unlock()
	c.self.setDemo(c.DemoURL(), domain.MustIP(demoPhoneIP))
	if err := c.Configure(c.DemoURL(), "", ""); err != nil {
		c.StopDemo()
		return err
	}
	c.owners.set(c.boxID(), []string{DemoPhoneMAC, DemoOwnerPhoneMAC})
	// The demo box opens every tier, as the app's own gate does.
	c.gate.mu.Lock()
	c.gate.demo = true
	c.gate.mu.Unlock()
	// Best effort: a demo without its past months still runs.
	if err := c.seedDemoHistory(); err != nil {
		c.log.Warnf("core: demo history: %v", err)
	}
	return nil
}

// demoHistoryMonths is how many past months the demo shows.
const demoHistoryMonths = 6

// seedDemoHistory gives the demo devices the months before this one, so the
// month selector and the device curve (Max, open in the demo) have
// something to show: there is no past on a box that just started. It only
// ever writes under the demo router's id, and only once: a demo whose last
// month already has figures is left as it is.
func (c *Core) seedDemoHistory() error {
	c.mu.RLock()
	box, url := c.demoBox, c.demoURL
	c.mu.RUnlock()
	if box == nil || url == "" {
		return nil
	}
	ctx := context.Background()
	routerID := usecase.RouterID(url)
	now := c.clock.Now()
	first := time.Date(now.Year(), now.Month(), 1, 12, 0, 0, 0, now.Location())
	if done, err := c.usage.UsageByPeriod(ctx, routerID, domain.BillingPeriod(first.AddDate(0, -1, 0))); err != nil || len(done) > 0 {
		return err
	}
	stations := box.Stations()
	for back := demoHistoryMonths; back >= 1; back-- {
		month := first.AddDate(0, -back, 0)
		// Three readings a month, each device less than the one before it,
		// a month busier than another so that the curve has a shape.
		for _, day := range []int{4, 14, 24} {
			deltas := map[domain.MAC]domain.UsageDelta{}
			for i, st := range stations {
				mac, err := domain.ParseMAC(st.MAC)
				if err != nil {
					continue
				}
				mo := 1_000_000_000 * uint64(3+(back*7+i*3)%5) / uint64(i+1) // 1 to 7 Go a reading
				deltas[mac] = domain.UsageDelta{Sent: mo / 10, Received: mo}
			}
			if err := c.usage.RecordSamples(ctx, routerID, month.AddDate(0, 0, day-1), nil, deltas); err != nil {
				return err
			}
		}
	}
	return nil
}

// DemoURL is the demo router's URL while it runs, "" otherwise.
func (c *Core) DemoURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.demoURL
}

// StopDemo stops the demo router if it runs. The core stays configured on the
// demo URL until the next Configure.
func (c *Core) StopDemo() {
	c.mu.Lock()
	srv, stop, demoURL := c.demo, c.demoStop, c.demoURL
	c.demo, c.demoURL, c.demoStop, c.demoBox = nil, "", nil, nil
	c.mu.Unlock()
	c.self.setDemo("", domain.IP{})
	if demoURL != "" {
		c.owners.drop(usecase.RouterID(demoURL)) // the demo's port is new each time
	}
	if stop != nil {
		close(stop)
	}
	if srv != nil {
		_ = srv.Close()
	}
}

// DemoPhoneMAC is « ce téléphone » on the demo box (#86): the core marks it
// Self, as it does the real phone on a household's box. A private MAC, as
// Android gives per Wi-Fi, sorted after the other demo devices.
const DemoPhoneMAC = "fa:4b:47:00:00:86"

// DemoOwnerPhoneMAC is the demo owner's other phone (#87): the core marks it
// Owner, so the Devices tab's hidden line counts it.
const DemoOwnerPhoneMAC = "fa:4b:47:00:00:87"

const (
	demoPhoneName      = "Mon-Telephone"
	demoPhoneIP        = "192.168.1.86"
	demoOwnerPhoneName = "Telephone-Proprio"
	demoOwnerPhoneIP   = "192.168.1.87"
)

// demoTrafficEvery is how often the demo devices use the Wi-Fi.
const demoTrafficEvery = 2 * time.Second

// demoTraffic makes the demo devices use the Wi-Fi, so the Conso tab has
// something to measure: each tick, every associated device downloads and
// uploads a little, the first ones more (a laptop more than a phone). It runs
// until stop is closed.
func demoTraffic(box *mockrouter.F6600P, stop <-chan struct{}, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			for i, st := range box.Stations() {
				if !st.Associated {
					continue
				}
				// rx is what the device sent, tx what it received.
				down := uint64(400_000 / (i + 1))
				box.AddTraffic(st.MAC, down/8, down)
			}
		}
	}
}

// Close stops the demo router and closes the database.
func (c *Core) Close() error {
	c.RelayStop()
	c.StopDemo()
	c.mu.Lock()
	c.handler = nil
	c.mu.Unlock()
	return c.db.Close()
}

// recorder is the in-memory http.ResponseWriter behind Request.
type recorder struct {
	header      http.Header
	body        strings.Builder
	status      int
	wroteHeader bool
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status, r.wroteHeader = code, true
	}
}

func (r *recorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	return r.body.Write(b)
}

// teeLogger writes to both the journal and the platform's log.
type teeLogger struct{ a, b port.Logger }

func (t teeLogger) Debugf(f string, v ...any) { t.a.Debugf(f, v...); t.b.Debugf(f, v...) }
func (t teeLogger) Infof(f string, v ...any)  { t.a.Infof(f, v...); t.b.Infof(f, v...) }
func (t teeLogger) Warnf(f string, v ...any)  { t.a.Warnf(f, v...); t.b.Warnf(f, v...) }
func (t teeLogger) Errorf(f string, v ...any) { t.a.Errorf(f, v...); t.b.Errorf(f, v...) }

type bridgeLogger struct{ l Logger }

func (b bridgeLogger) Debugf(f string, a ...any) { b.l.Log("debug", fmt.Sprintf(f, a...)) }
func (b bridgeLogger) Infof(f string, a ...any)  { b.l.Log("info", fmt.Sprintf(f, a...)) }
func (b bridgeLogger) Warnf(f string, a ...any)  { b.l.Log("warn", fmt.Sprintf(f, a...)) }
func (b bridgeLogger) Errorf(f string, a ...any) { b.l.Log("error", fmt.Sprintf(f, a...)) }

type nopLogger struct{}

func (nopLogger) Debugf(string, ...any) {}
func (nopLogger) Infof(string, ...any)  {}
func (nopLogger) Warnf(string, ...any)  {}
func (nopLogger) Errorf(string, ...any) {}
