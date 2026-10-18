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
// not.
//
// Every exported signature sticks to the types gomobile can bind: string, int,
// bool, error, pointers to structs of those, and interfaces.
package mobilecore

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
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
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/clock"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
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

// requestTimeout bounds one Request, router round trips included.
const requestTimeout = 60 * time.Second

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
	"POST /api/login",
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
	demo    *http.Server
	demoURL string
	log     port.Logger
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
	log := port.Logger(nopLogger{})
	if logger != nil {
		log = bridgeLogger{logger}
	}

	// One serialised session per router: the box allows a single admin login.
	factory := router.NewSharedFactory(catalog.New())
	clk := clock.Real{}
	disco := discovery.New(factory, clk)
	vlt := vault.New(sqlite.NewCredentialStore(db), vaultKey)
	devices := sqlite.NewDeviceRepo(db)

	c := &Core{db: db, log: log}
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
		Logger:   log,
	}
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
	opts := port.RouterOptions{}
	if u.Scheme == "https" {
		// Home routers ship self-signed certificates: accept it, but only the
		// one recorded on first use.
		opts.InsecureTLS = true
		opts.PinSHA256 = pinSHA256
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handler = buildHandler(c.svc, httpapi.Config{
		BaseURL: u.String(), AdapterID: adapterID, Opts: opts, Version: version,
	})
	return nil
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
	h.ServeHTTP(rec, req)
	return &Response{Status: rec.status, Body: rec.body.String()}, nil
}

// CertificateFingerprint connects to an https router and returns the hex
// SHA-256 of the certificate it presents, without trusting it. The app shows
// it once, stores it, and passes it to Configure from then on.
func CertificateFingerprint(routerURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(routerURL))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", fmt.Errorf("mobilecore: not an https URL: %q", routerURL)
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "443")
	}
	d := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", host, &tls.Config{InsecureSkipVerify: true}) // #nosec G402 — we only read the cert to pin it
	if err != nil {
		return "", fmt.Errorf("mobilecore: TLS probe: %w", err)
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("mobilecore: router presented no certificate")
	}
	return httpkit.CertFingerprint(certs[0].Raw), nil
}

// StartDemo starts the bundled mock router on 127.0.0.1 and points the core at
// it. Log in with DemoUsername / DemoPassword. This is what store reviewers
// use: they have no ZTE box. (It returns only an error so that Swift imports
// it as `throws`.)
func (c *Core) StartDemo() error {
	c.StopDemo()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("mobilecore: demo listener: %w", err)
	}
	srv := &http.Server{Handler: mockrouter.New(DemoUsername, DemoPassword).Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	c.mu.Lock()
	c.demo = srv
	c.demoURL = "http://" + ln.Addr().String()
	c.mu.Unlock()
	if err := c.Configure(c.DemoURL(), "", ""); err != nil {
		c.StopDemo()
		return err
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
	srv := c.demo
	c.demo, c.demoURL = nil, ""
	c.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
}

// Close stops the demo router and closes the database.
func (c *Core) Close() error {
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
