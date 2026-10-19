// Package agent is the relay: the piece that runs on the home Wi-Fi and is
// the only one that ever talks to the box.
//
// It dials out and holds a long poll open, because nothing can reach the box
// from outside (RFC-005 C1: the F6600P is on DS-Lite). It runs a command only
// when the hub has granted it the lease and, above all, only when
// internal/trust says the owner signed that exact command for this household.
// The hub is a postman here: it can delay or drop a command, never widen one.
//
// The package has no kernel or pgx dependency: the same code runs in the PC
// relay (`kergui serve --hub`) and, from H3, inside mobilecore on a phone.
package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Timings mirror RFC-005. They are a struct so tests can run a failover in a
// second instead of a minute, keeping the ratios.
type Timings struct {
	Heartbeat     time.Duration
	LongPoll      time.Duration
	SnapshotEvery time.Duration
	// SSIDEvery paces the SSID snapshot, which changes far less often than
	// the devices do (RFC-005: 60 s and 5 min). Zero keeps that ratio.
	SSIDEvery time.Duration
	// HubTimeout bounds one call to the hub, long poll excluded.
	HubTimeout time.Duration
}

// DefaultTimings are the production values.
func DefaultTimings() Timings {
	return Timings{
		Heartbeat:     20 * time.Second,
		LongPoll:      25 * time.Second,
		SnapshotEvery: 60 * time.Second,
		SSIDEvery:     5 * time.Minute,
		HubTimeout:    15 * time.Second,
	}
}

// RouterProbe reports the box's current certificate fingerprint. The relay
// sends it on every heartbeat, and the hub refuses a relay standing in front
// of another box.
type RouterProbe func(ctx context.Context) (string, error)

// WiFiProbe reports the network the relay is on. Both values may be empty:
// a PC on Ethernet has no Wi-Fi, and on Android reading them needs the
// location permission, which the household may refuse.
type WiFiProbe func(ctx context.Context) (ssid, bssid string)

// CredentialSink receives the box password once the relay has opened what
// the owner sealed for it. It stores it in the vault; it does not log in.
type CredentialSink func(ctx context.Context, creds domain.Credentials) error

// Config wires a relay.
type Config struct {
	HubURL string
	// Handler is the in-memory API the command runs against: the same
	// httpapi handler `kergui serve` exposes and mobilecore.Core.Request
	// calls. No new RPC surface (RFC-005).
	Handler http.Handler
	State   State
	Router  RouterProbe
	WiFi    WiFiProbe
	LANIP   string
	Version string
	// Credentials receives the sealed box password; optional, but without
	// it the relay can only run commands that need no router login.
	Credentials CredentialSink
	Clock       func() time.Time
	Log         port.Logger
	Timings     Timings
	HTTP        *http.Client
}

// Agent is a running relay.
type Agent struct {
	cfg      Config
	hub      *hubClient
	state    StateData
	verifier *trust.Verifier
	now      func() time.Time
	log      port.Logger
	timings  Timings

	// epoch fences this relay's work: it travels with every result, so a
	// result from a relay that lost the lease is refused by the hub.
	epoch  int64
	active bool
	// snapshots is the state the relay pushes on its own, each kind on its
	// own schedule.
	snapshots []*snapshotJob
	// ownMAC is this relay's own MAC, found by matching its LAN IP in the
	// device list. Cutting it would end the household's remote access.
	ownMAC string
}

// New builds a relay from stored state. The state must come from Enroll.
func New(cfg Config) (*Agent, error) {
	if cfg.Handler == nil {
		return nil, errors.New("agent: no API handler to run commands against")
	}
	if cfg.State == nil {
		return nil, errors.New("agent: no state store")
	}
	data, err := cfg.State.Load()
	if err != nil {
		return nil, err
	}
	if data.Token == "" || data.AgentID == "" || data.TenantID == "" {
		return nil, ErrNotEnrolled
	}
	id, err := trust.ParseIdentity(data.Identity)
	if err != nil {
		return nil, err
	}
	data.identity = id

	now := cfg.Clock
	if now == nil {
		now = time.Now
	}
	timings := cfg.Timings
	if timings.Heartbeat == 0 {
		timings = DefaultTimings()
	}
	if timings.SSIDEvery == 0 {
		// Keep RFC-005's ratio whatever the caller shortened the rest to, so
		// a test does not accidentally poll the SSIDs every round.
		timings.SSIDEvery = 5 * timings.SnapshotEvery
	}
	a := &Agent{
		cfg:     cfg,
		hub:     newHubClient(cfg.HubURL, data.Token, cfg.HTTP, timings),
		state:   data,
		now:     now,
		log:     cfg.Log,
		timings: timings,
	}
	if a.log == nil {
		a.log = nopLogger{}
	}
	a.snapshots = newSnapshotJobs(timings)
	a.verifier = trust.NewVerifier(data.TenantID, data.OwnerKey, now())
	if len(data.Bundle.Payload) > 0 {
		// A bundle kept from the last run means the relay can work before
		// the hub answers, and a hub that serves an older one is refused.
		if err := a.verifier.AcceptBundle(data.Bundle); err != nil {
			a.log.Warnf("agent: stored trust bundle refused (%v), waiting for a fresh one", err)
		}
	}
	return a, nil
}

// Run drives the relay until ctx ends: heartbeat, trust refresh, and — only
// while it holds the lease — the long poll and the snapshots.
//
// Every loss of the lease stops the router work at once. A relay that cannot
// reach the hub stops too: it has no way to know it is still the holder, and
// the box allows one session.
func (a *Agent) Run(ctx context.Context) error {
	defer a.releaseLease()
	for {
		wait := a.tick(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

// tick runs one round and returns how long to wait before the next one.
func (a *Agent) tick(ctx context.Context) time.Duration {
	hb, err := a.heartbeat(ctx, false)
	if err != nil {
		// The hub is unreachable: stand down rather than keep the box.
		a.active = false
		a.log.Warnf("agent: heartbeat failed (%v), standing by", err)
		return a.timings.Heartbeat
	}
	a.refreshTrust(ctx, hb)

	if hb.Lease != leaseActive {
		if a.active {
			a.log.Infof("agent: lease lost (%s), standing by", hb.Reason)
		}
		a.active = false
		return retryAfter(hb, a.timings.Heartbeat)
	}
	if !a.active {
		a.log.Infof("agent: holding the router lease (epoch %d)", hb.Epoch)
	}
	a.active, a.epoch = true, hb.Epoch

	a.maybeSnapshot(ctx, false)
	// While active, the long poll paces the loop: it comes back as soon as
	// there is work and at the latest after LongPoll, which is shorter than
	// the lease, so the next heartbeat still lands in time.
	switch a.serveOne(ctx) {
	case pollFailed:
		return a.timings.Heartbeat
	default:
		return 0
	}
}

// retryAfter honours the hub's pacing, with a floor so a buggy hub cannot
// make a phone spin.
func retryAfter(hb heartbeatResponse, fallback time.Duration) time.Duration {
	if hb.RetryAfterSeconds > 0 {
		return time.Duration(hb.RetryAfterSeconds) * time.Second
	}
	return fallback
}

// heartbeat reports where the relay is and what it sees.
func (a *Agent) heartbeat(ctx context.Context, released bool) (heartbeatResponse, error) {
	req := heartbeatRequest{LANIP: a.cfg.LANIP, Version: a.cfg.Version, Released: released}
	if a.cfg.Router != nil {
		pin, err := a.cfg.Router(ctx)
		if err != nil {
			// No pin means the box is unreachable or presenting another
			// certificate: report nothing rather than something false.
			a.log.Warnf("agent: cannot read the router certificate: %v", err)
		}
		req.RouterIdentity.Pin = pin
	}
	if a.cfg.WiFi != nil {
		req.WiFi.SSID, req.WiFi.BSSID = a.cfg.WiFi(ctx)
	}
	return a.hub.heartbeat(ctx, req)
}

// releaseLease tells the hub the relay is going away, so the next one takes
// over in a heartbeat instead of waiting out the lease.
func (a *Agent) releaseLease() {
	if !a.active {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.timings.HubTimeout)
	defer cancel()
	if _, err := a.heartbeat(ctx, true); err != nil {
		a.log.Warnf("agent: could not release the lease: %v", err)
	}
	a.active = false
}

// refreshTrust pulls a new bundle or new credentials when the hub says the
// version moved. The relay never polls for them.
func (a *Agent) refreshTrust(ctx context.Context, hb heartbeatResponse) {
	if hb.BundleVersion > a.bundleVersion() {
		if err := a.pullBundle(ctx); err != nil {
			a.log.Warnf("agent: refusing the hub's trust bundle: %v", err)
		}
	}
	if a.cfg.Credentials != nil && hb.CredentialVersion > a.state.CredentialVersion {
		if err := a.pullCredentials(ctx); err != nil {
			a.log.Warnf("agent: refusing the sealed credentials: %v", err)
		}
	}
}

func (a *Agent) bundleVersion() uint64 {
	if b := a.verifier.Bundle(); b != nil {
		return b.Version
	}
	return 0
}

// pullBundle installs a bundle only if the owner signed it. A hub that
// serves anything else is simply refused.
func (a *Agent) pullBundle(ctx context.Context) error {
	signed, err := a.hub.bundle(ctx)
	if err != nil {
		return err
	}
	if err := a.verifier.AcceptBundle(signed); err != nil {
		return err
	}
	a.state.Bundle = signed
	if err := a.cfg.State.Save(a.state); err != nil {
		a.log.Warnf("agent: could not store the trust bundle: %v", err)
	}
	a.log.Infof("agent: trust bundle updated to version %d", a.bundleVersion())
	return nil
}

// pullCredentials opens what the owner sealed for this relay and hands it to
// the vault. box authenticates the sender, so a blob from the hub fails here.
func (a *Agent) pullCredentials(ctx context.Context) error {
	blob, version, err := a.hub.credentials(ctx)
	if err != nil {
		return err
	}
	b := a.verifier.Bundle()
	if b == nil {
		return trust.ErrNoBundle
	}
	creds, err := trust.OpenCredentials(a.state.identity, b.Owner.X25519, blob, a.state.TenantID, a.state.AgentID)
	if err != nil {
		return err
	}
	if err := a.cfg.Credentials(ctx, domain.Credentials{
		Username: creds.Username, Password: creds.Password.Reveal(),
	}); err != nil {
		return fmt.Errorf("agent: storing the box credentials: %w", err)
	}
	a.state.CredentialVersion = version
	if err := a.cfg.State.Save(a.state); err != nil {
		a.log.Warnf("agent: could not record the credentials version: %v", err)
	}
	// Never the password, never the username: only that it changed.
	a.log.Infof("agent: box credentials updated (version %d)", version)
	return nil
}

// nopLogger is the default when no logger is wired.
type nopLogger struct{}

func (nopLogger) Debugf(string, ...any) {}
func (nopLogger) Infof(string, ...any)  {}
func (nopLogger) Warnf(string, ...any)  {}
func (nopLogger) Errorf(string, ...any) {}
