// Package e2e drives the whole H1 path in one process: the hub on Postgres,
// a relay from internal/agent, the in-memory httpapi handler it runs
// commands on, and the mock F6600P behind TLS.
//
// It lives under internal/hub because that is the only part of the module
// allowed to link the kernel (tests/architecture_test.go).
package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/controller/httpapi"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/discovery"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/vault"
	routeradapter "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/catalog"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/httpkit"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/vendor"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/app"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/config"
	relaydomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/clock"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/logging"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

const (
	testDatabaseEnv = "KERGUI_HUB_TEST_DATABASE_URL"
	boxUser         = "admin"
	boxPass         = "s3cret"
	// A device present in the captured F6600P fixture, not blocked.
	targetMAC = "ac:bb:cc:00:00:22"
)

// fastTimings keep the ratios of RFC-005 (lease = 3 heartbeats) while
// running a failover in seconds instead of a minute.
var fastTimings = agent.Timings{
	Heartbeat:     200 * time.Millisecond,
	LongPoll:      500 * time.Millisecond,
	SnapshotEvery: time.Second,
	HubTimeout:    5 * time.Second,
}

// --- the world ------------------------------------------------------------

type world struct {
	t      *testing.T
	hub    *httptest.Server
	box    *mockrouter.F6600P
	boxURL string
	boxPin string
	owner  *ownerApp
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dsn := os.Getenv(testDatabaseEnv)
	if dsn == "" {
		t.Skipf("%s not set: skipping the end-to-end test", testDatabaseEnv)
	}

	// The box: the captured F6600P protocol, behind TLS so the relay has a
	// certificate to pin, as it would at a household.
	box := mockrouter.NewF6600P(boxUser, boxPass)
	boxSrv := httptest.NewTLSServer(box.Handler())
	t.Cleanup(boxSrv.Close)
	pin := httpkit.CertFingerprint(boxSrv.Certificate().Raw)

	// The hub.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a, err := app.New(ctx, config.Config{
		DatabaseURL: dsn,
		JWTSecret:   strings.Repeat("k", 48),
		TokenTTL:    15 * time.Minute,
		RefreshTTL:  time.Hour,
		PublicURL:   "http://hub.test",
		MailFrom:    "Kergui <no-reply@hub.test>",
	}, "e2e", log,
		app.WithCallerWait(5*time.Second),
		// The same ratios as RFC-005 (a lease of three heartbeats), run
		// fast enough to watch a failover inside a test.
		app.WithLeaseTimings(relaydomain.Timings{
			LeaseTTL:  3 * fastTimings.Heartbeat,
			Heartbeat: fastTimings.Heartbeat,
			Presence:  2 * fastTimings.Heartbeat,
			LongPoll:  fastTimings.LongPoll,
		}))
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = a.Run(runCtx) }() // the outbox, so audit rows land
	hubSrv := httptest.NewServer(a.Handler())
	t.Cleanup(func() {
		hubSrv.Close()
		stop()
		<-done
		a.Close()
	})

	w := &world{t: t, hub: hubSrv, box: box, boxURL: boxSrv.URL, boxPin: pin}
	w.owner = signUpOwner(t, hubSrv)
	return w
}

// newRouterAPI builds the in-memory API a relay runs commands against: the
// same handler `kergui serve` exposes, pinned to the box's certificate.
func newRouterAPI(t *testing.T, boxURL, pin string) (http.Handler, *vault.AESGCMVault) {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	factory := routeradapter.NewSharedFactory(catalog.New())
	clk := clock.Real{}
	disco := discovery.New(factory, clk)
	vlt := vault.New(sqlite.NewCredentialStore(db), "e2e-master-key")
	logger := logging.New(io.Discard, false)
	devices := sqlite.NewDeviceRepo(db)

	api := httpapi.New(httpapi.Services{
		Discover: usecase.NewDiscoverRouter(disco),
		List:     usecase.NewListDevices(factory, vlt, devices, vendor.New(nil), disco, clk, logger),
		Block:    usecase.NewBlockDevice(factory, vlt, disco, clk, logger),
		Unblock:  usecase.NewUnblockDevice(factory, vlt, disco, clk, logger),
		Auth:     usecase.NewAuthenticate(factory, vlt, sqlite.NewRouterRepo(db), disco, clk, logger),
		Filter:   usecase.NewMACFilter(factory, vlt, disco, logger),
		Logger:   logger,
	}, httpapi.Config{
		BaseURL:   boxURL,
		AdapterID: "zte_f6600p",
		// The box is self-signed, so the pin is what identifies it.
		Opts:    port.RouterOptions{InsecureTLS: true, PinSHA256: pin, Timeout: 10 * time.Second},
		Version: "e2e",
	})
	return api.Handler(), vlt
}

// --- the owner's app ------------------------------------------------------

// ownerApp stands in for the phone: it holds the owner's keys and signs.
type ownerApp struct {
	t        *testing.T
	hub      *httptest.Server
	keys     *trust.Identity
	token    string
	tenantID string
	version  uint64
}

func signUpOwner(t *testing.T, hub *httptest.Server) *ownerApp {
	t.Helper()
	keys, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	o := &ownerApp{t: t, hub: hub, keys: keys}

	run := time.Now().UnixNano()
	email := fmt.Sprintf("owner-%d@hub.test", run)
	const password = "Correct-Horse-9-Battery"
	status, signup := o.call("POST", "/api/v1/auth/signup-owner", map[string]any{
		"email": email, "password": password,
		"tenantName": "Maison Diallo", "tenantSlug": fmt.Sprintf("diallo-%d", run),
	})
	if status != http.StatusCreated {
		t.Fatalf("signup = %d %v", status, signup)
	}
	o.tenantID = str(signup, "tenantId")
	o.token = str(signup, "accessToken") // signup already returns usable tokens
	return o
}

func (o *ownerApp) call(method, path string, body any) (int, map[string]any) {
	o.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, o.hub.URL+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if o.token != "" {
		req.Header.Set("Authorization", "Bearer "+o.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		o.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (o *ownerApp) tenantPath(suffix string) string {
	return "/api/v1/tenants/" + o.tenantID + "/" + suffix
}

// registerNetwork tells the hub which box the relays must be in front of.
func (o *ownerApp) registerNetwork(pin string) {
	o.t.Helper()
	if code, body := o.call("PUT", o.tenantPath("network"), map[string]any{
		"ssids": []string{"MonWiFi-2G"}, "bssids": []string{}, "routerPin": pin,
	}); code != http.StatusNoContent {
		o.t.Fatalf("PUT network = %d %v", code, body)
	}
}

// publishBundle signs the household's trust bundle and uploads it.
func (o *ownerApp) publishBundle(pin string, relays ...trust.Relay) {
	o.t.Helper()
	o.version++
	signed, err := trust.Sign(o.keys, trust.DomainBundle, trust.Bundle{
		TenantID: o.tenantID,
		Version:  o.version,
		IssuedAt: time.Now().UTC(),
		Owner:    o.keys.Public(),
		Network:  trust.Network{SSIDs: []string{"MonWiFi-2G"}, RouterPin: pin},
		Relays:   relays,
	})
	if err != nil {
		o.t.Fatal(err)
	}
	if code, body := o.call("PUT", o.tenantPath("trust-bundle"), map[string]any{
		"payload": signed.Payload, "sig": signed.Sig,
	}); code != http.StatusNoContent {
		o.t.Fatalf("PUT trust-bundle = %d %v", code, body)
	}
}

// invite is the QR the owner shows: a pairing code and their fingerprint.
func (o *ownerApp) invite(kind string) trust.Invite {
	o.t.Helper()
	code, body := o.call("POST", o.tenantPath("agents/pairing"), map[string]any{"kind": kind})
	if code != http.StatusCreated {
		o.t.Fatalf("POST agents/pairing = %d %v", code, body)
	}
	return trust.Invite{Code: str(body, "code"), OwnerFingerprint: o.keys.KeyID()}
}

// sealCredentials gives one relay the box password, sealed for it alone.
func (o *ownerApp) sealCredentials(agentID string, relayX []byte) {
	o.t.Helper()
	blob, err := trust.SealCredentials(rand.Reader, o.keys, relayX, trust.Credentials{
		TenantID: o.tenantID, AgentID: agentID, Username: boxUser, Password: boxPass, Version: 1,
	})
	if err != nil {
		o.t.Fatal(err)
	}
	if code, body := o.call("PUT", o.tenantPath("agents/"+agentID+"/credentials"), map[string]any{
		"blob": blob, "version": 1,
	}); code != http.StatusNoContent {
		o.t.Fatalf("PUT credentials = %d %v", code, body)
	}
}

// block signs a block command and sends it the way the app does.
func (o *ownerApp) block(mac string) (int, map[string]any) {
	return o.deviceCommand(mac, "block")
}

func (o *ownerApp) unblock(mac string) (int, map[string]any) {
	return o.deviceCommand(mac, "unblock")
}

func (o *ownerApp) deviceCommand(mac, action string) (int, map[string]any) {
	o.t.Helper()
	signed, err := trust.NewCommand(o.keys, o.tenantID, http.MethodPost,
		"/api/devices/"+mac+"/"+action, nil, time.Now(), 60*time.Second)
	if err != nil {
		o.t.Fatal(err)
	}
	return o.call("POST", o.tenantPath("router/devices/"+mac+"/"+action),
		map[string]any{"payload": signed.Payload, "sig": signed.Sig})
}

// --- relays ---------------------------------------------------------------

// startRelay enrolls a relay and runs it, as `kergui serve --hub` does.
func (w *world) startRelay(ctx context.Context, invite trust.Invite, kind, lanIP string) (*agent.Agent, agent.StateData, string) {
	w.t.Helper()
	state, res, err := agent.Enroll(ctx, w.hub.URL, invite, "banc de test", kind, http.DefaultClient)
	if err != nil {
		w.t.Fatalf("Enroll: %v", err)
	}
	// Both screens must show the same six digits before the owner signs
	// the relay in: that is what a swapped key would break.
	if res.Code != trust.PairingCode(w.owner.keys.Public(), relayPublic(w.t, state)) {
		w.t.Fatalf("the two pairing codes differ: %s", res.Code)
	}
	// Each relay runs its own API and its own vault: two relays are two
	// devices, and the box allows one admin session between them.
	handler, vlt := newRouterAPI(w.t, w.boxURL, w.boxPin)
	ag := w.newAgent(state, lanIP, handler, vlt)
	go func() { _ = ag.Run(ctx) }()
	return ag, state, res.Code
}

func (w *world) newAgent(state agent.StateData, lanIP string, handler http.Handler, vlt *vault.AESGCMVault) *agent.Agent {
	w.t.Helper()
	ag, err := agent.New(agent.Config{
		HubURL:  w.hub.URL,
		Handler: handler,
		State:   &agent.MemoryState{Data: state},
		Router: func(ctx context.Context) (string, error) {
			return httpkit.ProbeFingerprint(ctx, w.boxURL, 5*time.Second)
		},
		WiFi:    func(context.Context) (string, string) { return "MonWiFi-2G", "" },
		LANIP:   lanIP,
		Version: "e2e",
		Credentials: func(ctx context.Context, c domain.Credentials) error {
			return vlt.Store(ctx, usecase.RouterID(w.boxURL), c)
		},
		Log:     logging.New(io.Discard, false),
		Timings: fastTimings,
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return ag
}

// relayPublic recovers a relay's public keys from its stored state.
func relayPublic(t *testing.T, s agent.StateData) trust.PublicKeys {
	t.Helper()
	id, err := trust.ParseIdentity(s.Identity)
	if err != nil {
		t.Fatal(err)
	}
	return id.Public()
}

func trustRelay(s agent.StateData, kind string, t *testing.T) trust.Relay {
	return trust.Relay{AgentID: s.AgentID, Kind: kind, Keys: relayPublic(t, s)}
}

// --- tests ----------------------------------------------------------------

// TestE2E_BlockThroughTheHub is the acceptance test of H1: signup, pairing,
// a signed trust bundle, the device list, a block the mock really applies,
// and an unblock.
func TestE2E_BlockThroughTheHub(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.owner.registerNetwork(w.boxPin)
	w.owner.publishBundle(w.boxPin) // v1: the owner alone, so the relay can pin the key

	_, state, _ := w.startRelay(ctx, w.owner.invite("pc"), "pc", "192.168.1.50")
	w.owner.publishBundle(w.boxPin, trustRelay(state, "pc", t)) // v2: the relay is trusted
	w.owner.sealCredentials(state.AgentID, relayPublic(t, state).X25519)

	// The relay takes the lease, logs in to the box and pushes what it sees.
	devices := waitForDevices(t, w)
	if !strings.Contains(devices, targetMAC) {
		t.Fatalf("the snapshot does not list the household's devices: %s", truncate(devices))
	}
	if w.box.Logins() == 0 {
		t.Fatal("the relay never logged in to the box")
	}

	// Block: the owner signs, the hub carries, the relay runs it, the mock
	// shows it.
	if code, body := w.owner.block(targetMAC); code != http.StatusOK {
		t.Fatalf("POST block = %d %v", code, body)
	}
	if !w.box.Blocked(targetMAC) {
		t.Fatal("the device is not blocked on the box")
	}

	if code, body := w.owner.unblock(targetMAC); code != http.StatusOK {
		t.Fatalf("POST unblock = %d %v", code, body)
	}
	if w.box.Blocked(targetMAC) {
		t.Fatal("the device is still blocked after unblock")
	}
}

// TestE2E_ForgedCommandIsRefusedByTheRelay is the point of the whole trust
// model: a command the hub makes up, bypassing its own checks by writing
// straight to the queue, is refused where it matters.
func TestE2E_ForgedCommandIsRefusedByTheRelay(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.owner.registerNetwork(w.boxPin)
	w.owner.publishBundle(w.boxPin)
	_, state, _ := w.startRelay(ctx, w.owner.invite("pc"), "pc", "192.168.1.50")
	w.owner.publishBundle(w.boxPin, trustRelay(state, "pc", t))
	w.owner.sealCredentials(state.AgentID, relayPublic(t, state).X25519)
	waitForDevices(t, w)

	// A key the household never delegated — the hub's own, say.
	rogue, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	forged, err := trust.NewCommand(rogue, w.owner.tenantID, http.MethodPost,
		"/api/devices/"+targetMAC+"/block", nil, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	insertCommand(t, w, forged)

	// The relay picks it up, refuses it, and signs the refusal.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cmdStatus(t, w, forged) == "done" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if w.box.Blocked(targetMAC) {
		t.Fatal("a forged command reached the box")
	}
	if got := cmdResultBody(t, w, forged); !strings.Contains(got, "trust:") {
		t.Errorf("the refusal does not say why: %s", truncate(got))
	}
}

// TestE2E_Failover: the holder stops, the second relay takes over inside a
// lease, and only one session is ever open on the box.
func TestE2E_Failover(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.owner.registerNetwork(w.boxPin)
	w.owner.publishBundle(w.boxPin)

	firstCtx, stopFirst := context.WithCancel(ctx)
	_, first, _ := w.startRelay(firstCtx, w.owner.invite("pc"), "pc", "192.168.1.50")
	_, second, _ := w.startRelay(ctx, w.owner.invite("pc"), "pc", "192.168.1.51")
	w.owner.publishBundle(w.boxPin, trustRelay(first, "pc", t), trustRelay(second, "pc", t))
	w.owner.sealCredentials(first.AgentID, relayPublic(t, first).X25519)
	w.owner.sealCredentials(second.AgentID, relayPublic(t, second).X25519)

	waitForDevices(t, w)
	loginsBefore := w.box.Logins()
	sessionBefore := w.box.ActiveSession()

	// The holder dies without releasing, as a phone leaving the house does.
	stopFirst()

	// Within one lease, the other relay must be able to act.
	deadline := time.Now().Add(15 * time.Second)
	var code int
	for time.Now().Before(deadline) {
		if code, _ = w.owner.block(targetMAC); code == http.StatusOK {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if code != http.StatusOK {
		t.Fatalf("no relay took over: last block answered %d", code)
	}
	if !w.box.Blocked(targetMAC) {
		t.Fatal("the take-over relay did not reach the box")
	}
	// The box allows one admin session: the take-over costs exactly one
	// more login, and the session that remains is the new one.
	if got := w.box.Logins() - loginsBefore; got != 1 {
		t.Errorf("take-over cost %d logins, want exactly 1", got)
	}
	if w.box.ActiveSession() == sessionBefore {
		t.Error("the box still holds the dead relay's session")
	}
}

// TestE2E_StandbyRelayNeverTouchesTheBox: a relay whose Wi-Fi or box
// identity does not match stays in standby and never logs in.
func TestE2E_StandbyRelayNeverTouchesTheBox(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The household registered a different box than the one this relay
	// sees: the pin will not match.
	w.owner.registerNetwork("00000000000000000000000000000000000000000000000000000000deadbeef")
	w.owner.publishBundle(w.boxPin)

	_, state, _ := w.startRelay(ctx, w.owner.invite("pc"), "pc", "192.168.1.50")
	w.owner.publishBundle(w.boxPin, trustRelay(state, "pc", t))
	w.owner.sealCredentials(state.AgentID, relayPublic(t, state).X25519)

	time.Sleep(2 * time.Second) // several heartbeats

	if w.box.Logins() != 0 {
		t.Fatalf("a standby relay logged in to the box %d times", w.box.Logins())
	}
	if code, _ := w.owner.call("GET", w.owner.tenantPath("router/devices"), nil); code != http.StatusNotFound {
		t.Errorf("GET devices = %d, want 404: a standby relay must push nothing", code)
	}
}

// --- helpers --------------------------------------------------------------

// waitForDevices waits for the relay to take the lease and push a snapshot.
func waitForDevices(t *testing.T, w *world) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		code, body := w.owner.call("GET", w.owner.tenantPath("router/devices"), nil)
		if code == http.StatusOK {
			raw, _ := json.Marshal(body["snapshot"])
			var signed trust.SignedSnapshot
			if err := json.Unmarshal(raw, &signed); err != nil {
				t.Fatal(err)
			}
			snap, err := signed.Unverified()
			if err != nil {
				t.Fatal(err)
			}
			return string(snap.Body)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("no relay pushed a device snapshot in time")
	return ""
}

// insertCommand writes a command straight into the hub's queue, bypassing
// the hub's own checks — the worst case the relay must survive.
func insertCommand(t *testing.T, w *world, signed trust.SignedCommand) {
	t.Helper()
	cmd, err := signed.Unverified()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, `INSERT INTO hub_relay_commands (id, tenant_id, envelope, method, path, status, created_at, expires_at)
	            VALUES ($1, $2, $3, $4, $5, 'queued', now(), $6)`,
		cmd.CmdID, w.owner.tenantID, envelope, cmd.Method, cmd.Path, cmd.ExpiresAt)
}

func cmdStatus(t *testing.T, w *world, signed trust.SignedCommand) string {
	t.Helper()
	cmd, _ := signed.Unverified()
	code, body := w.owner.call("GET", w.owner.tenantPath("router/commands/"+cmd.CmdID), nil)
	if code != http.StatusOK {
		return ""
	}
	return str(body, "status")
}

func cmdResultBody(t *testing.T, w *world, signed trust.SignedCommand) string {
	t.Helper()
	cmd, _ := signed.Unverified()
	_, body := w.owner.call("GET", w.owner.tenantPath("router/commands/"+cmd.CmdID), nil)
	raw, _ := json.Marshal(body["result"])
	var res trust.SignedResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return ""
	}
	r, err := res.Unverified()
	if err != nil {
		return ""
	}
	return string(r.Body)
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func truncate(s string) string {
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
