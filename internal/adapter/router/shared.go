package router

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// SharedFactory hands out one long-lived session per router and runs every call
// on it one at a time.
//
// Home routers allow a single admin session: each new login silently ends the
// previous one. With one gateway per request, concurrent requests (dashboard,
// metering, billing, captive refresh) kept logging each other out, and rapid
// logins risk the router's lockout. Sharing and serialising fixes both.
type SharedFactory struct {
	inner    port.RouterFactory
	mu       sync.Mutex
	sessions map[string]*SharedGateway
}

var _ port.RouterFactory = (*SharedFactory)(nil)

// NewSharedFactory wraps a factory.
func NewSharedFactory(inner port.RouterFactory) *SharedFactory {
	return &SharedFactory{inner: inner, sessions: map[string]*SharedGateway{}}
}

// New returns the shared session for this router, creating it on first use.
func (f *SharedFactory) New(adapterID, baseURL string, opts port.RouterOptions) (port.RouterPort, error) {
	// The pin belongs in the key: it is enforced by the session's TLS
	// transport, so reusing a session built for another pin would silently
	// keep checking the old one.
	key := fmt.Sprintf("%s|%s|%t|%t|%s|%s", adapterID, baseURL, opts.InsecureTLS, opts.SkipWriteVerify, opts.Timeout, opts.PinSHA256)
	f.mu.Lock()
	defer f.mu.Unlock()
	if g, ok := f.sessions[key]; ok {
		return g, nil
	}
	gw, err := f.inner.New(adapterID, baseURL, opts)
	if err != nil {
		return nil, err
	}
	g := &SharedGateway{inner: gw, sem: make(chan struct{}, 1)}
	f.sessions[key] = g
	return g, nil
}

// Available implements port.RouterFactory.
func (f *SharedFactory) Available() []domain.AdapterMeta { return f.inner.Available() }

// Lookup implements port.RouterFactory.
func (f *SharedFactory) Lookup(id string) (domain.AdapterMeta, bool) { return f.inner.Lookup(id) }

// SharedGateway serialises access to one router session and logs in only when
// needed: on first use, with new credentials, or after the router ended the
// session. It exposes every optional capability; those the adapter lacks
// answer domain.ErrNotImplemented.
type SharedGateway struct {
	inner    port.RouterPort
	sem      chan struct{}
	creds    domain.Credentials
	loggedIn bool
}

var (
	_ port.RouterPort           = (*SharedGateway)(nil)
	_ port.MACFilterSwitch      = (*SharedGateway)(nil)
	_ port.MACFilterModeReader  = (*SharedGateway)(nil)
	_ port.WiFiAccessControl    = (*SharedGateway)(nil)
	_ port.WiFiRuleReader       = (*SharedGateway)(nil)
	_ port.WiFiSSIDManager      = (*SharedGateway)(nil)
	_ port.TrafficMeter         = (*SharedGateway)(nil)
	_ port.HostObserver         = (*SharedGateway)(nil)
	_ port.AdminPasswordChanger = (*SharedGateway)(nil)
)

func (g *SharedGateway) lock(ctx context.Context) error {
	select {
	case g.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *SharedGateway) unlock() { <-g.sem }

// Login implements port.RouterPort. It is a no-op while the session is valid
// for the same credentials.
func (g *SharedGateway) Login(ctx context.Context, c domain.Credentials) error {
	if err := g.lock(ctx); err != nil {
		return err
	}
	defer g.unlock()
	if g.loggedIn && g.creds == c {
		return nil
	}
	if err := g.inner.Login(ctx, c); err != nil {
		g.loggedIn = false
		return err
	}
	g.creds, g.loggedIn = c, true
	return nil
}

// do runs op alone on the session, logging in again once if the router ended it.
func (g *SharedGateway) do(ctx context.Context, op func() error) error {
	if err := g.lock(ctx); err != nil {
		return err
	}
	defer g.unlock()
	err := op()
	if errors.Is(err, domain.ErrSessionExpired) && g.loggedIn {
		if lerr := g.inner.Login(ctx, g.creds); lerr != nil {
			g.loggedIn = false
			return fmt.Errorf("%w (re-login failed: %v)", err, lerr)
		}
		err = op()
	}
	if errors.Is(err, domain.ErrAuthFailed) {
		g.loggedIn = false
	}
	return err
}

// ChangeAdminPassword implements port.AdminPasswordChanger. The adapter ends
// logged in with the new password, so the session is kept under it.
func (g *SharedGateway) ChangeAdminPassword(ctx context.Context, current domain.Credentials, newPassword string) error {
	ch, ok := g.inner.(port.AdminPasswordChanger)
	if !ok {
		return g.unsupported("admin password change")
	}
	if err := g.lock(ctx); err != nil {
		return err
	}
	defer g.unlock()
	if err := ch.ChangeAdminPassword(ctx, current, newPassword); err != nil {
		g.loggedIn = false
		return err
	}
	g.creds = domain.Credentials{Username: current.Username, Password: newPassword}
	g.loggedIn = true
	return nil
}

// Meta implements port.RouterPort.
func (g *SharedGateway) Meta() domain.AdapterMeta { return g.inner.Meta() }

// RouterInfo implements port.RouterPort.
func (g *SharedGateway) RouterInfo(ctx context.Context) (out *domain.RouterInfo, err error) {
	err = g.do(ctx, func() (e error) { out, e = g.inner.RouterInfo(ctx); return })
	return
}

// Devices implements port.RouterPort.
func (g *SharedGateway) Devices(ctx context.Context) (out []domain.Device, err error) {
	err = g.do(ctx, func() (e error) { out, e = g.inner.Devices(ctx); return })
	return
}

// AccessRules implements port.RouterPort.
func (g *SharedGateway) AccessRules(ctx context.Context) (out []domain.AccessRule, err error) {
	err = g.do(ctx, func() (e error) { out, e = g.inner.AccessRules(ctx); return })
	return
}

// Block implements port.RouterPort.
func (g *SharedGateway) Block(ctx context.Context, mac domain.MAC) error {
	return g.do(ctx, func() error { return g.inner.Block(ctx, mac) })
}

// Unblock implements port.RouterPort.
func (g *SharedGateway) Unblock(ctx context.Context, mac domain.MAC) error {
	return g.do(ctx, func() error { return g.inner.Unblock(ctx, mac) })
}

func (g *SharedGateway) unsupported(what string) error {
	return fmt.Errorf("%w: adapter %s has no %s", domain.ErrNotImplemented, g.inner.Meta().ID, what)
}

// MACFilterEnabled implements port.MACFilterSwitch.
func (g *SharedGateway) MACFilterEnabled(ctx context.Context) (on bool, err error) {
	sw, ok := g.inner.(port.MACFilterSwitch)
	if !ok {
		return false, g.unsupported("MAC filter switch")
	}
	err = g.do(ctx, func() (e error) { on, e = sw.MACFilterEnabled(ctx); return })
	return
}

// MACFilterMode implements port.MACFilterModeReader. A box that only has the
// switch reports its state with no mode.
func (g *SharedGateway) MACFilterMode(ctx context.Context) (on bool, mode string, err error) {
	if mr, ok := g.inner.(port.MACFilterModeReader); ok {
		err = g.do(ctx, func() (e error) { on, mode, e = mr.MACFilterMode(ctx); return })
		return
	}
	on, err = g.MACFilterEnabled(ctx)
	return
}

// SetMACFilterEnabled implements port.MACFilterSwitch.
func (g *SharedGateway) SetMACFilterEnabled(ctx context.Context, enabled bool) error {
	sw, ok := g.inner.(port.MACFilterSwitch)
	if !ok {
		return g.unsupported("MAC filter switch")
	}
	return g.do(ctx, func() error { return sw.SetMACFilterEnabled(ctx, enabled) })
}

// WiFiBlockedMACs implements port.WiFiAccessControl.
func (g *SharedGateway) WiFiBlockedMACs(ctx context.Context) (out []domain.MAC, err error) {
	w, ok := g.inner.(port.WiFiAccessControl)
	if !ok {
		return nil, g.unsupported("Wi-Fi access control")
	}
	err = g.do(ctx, func() (e error) { out, e = w.WiFiBlockedMACs(ctx); return })
	return
}

// WiFiBlock implements port.WiFiAccessControl.
func (g *SharedGateway) WiFiBlock(ctx context.Context, mac domain.MAC) error {
	w, ok := g.inner.(port.WiFiAccessControl)
	if !ok {
		return g.unsupported("Wi-Fi access control")
	}
	return g.do(ctx, func() error { return w.WiFiBlock(ctx, mac) })
}

// WiFiUnblock implements port.WiFiAccessControl.
func (g *SharedGateway) WiFiUnblock(ctx context.Context, mac domain.MAC) error {
	w, ok := g.inner.(port.WiFiAccessControl)
	if !ok {
		return g.unsupported("Wi-Fi access control")
	}
	return g.do(ctx, func() error { return w.WiFiUnblock(ctx, mac) })
}

// WiFiRules implements port.WiFiRuleReader.
func (g *SharedGateway) WiFiRules(ctx context.Context) (out []domain.AccessRule, err error) {
	r, ok := g.inner.(port.WiFiRuleReader)
	if !ok {
		return nil, g.unsupported("Wi-Fi rule list")
	}
	err = g.do(ctx, func() (e error) { out, e = r.WiFiRules(ctx); return })
	return
}

// WiFiSSIDs implements port.WiFiSSIDManager.
func (g *SharedGateway) WiFiSSIDs(ctx context.Context) (out []domain.WiFiSSID, err error) {
	m, ok := g.inner.(port.WiFiSSIDManager)
	if !ok {
		return nil, g.unsupported("Wi-Fi networks")
	}
	err = g.do(ctx, func() (e error) { out, e = m.WiFiSSIDs(ctx); return })
	return
}

// SetWiFiSSID implements port.WiFiSSIDManager.
func (g *SharedGateway) SetWiFiSSID(ctx context.Context, id string, change domain.WiFiSSIDChange) error {
	m, ok := g.inner.(port.WiFiSSIDManager)
	if !ok {
		return g.unsupported("Wi-Fi networks")
	}
	return g.do(ctx, func() error { return m.SetWiFiSSID(ctx, id, change) })
}

// Observe implements port.HostObserver. It is the per-station view device
// recognition works on, and it costs three reads of the router, so it goes
// through the same serialised session as everything else.
func (g *SharedGateway) Observe(ctx context.Context) (out []domain.Observation, err error) {
	o, ok := g.inner.(port.HostObserver)
	if !ok {
		return nil, g.unsupported("per-station observations")
	}
	err = g.do(ctx, func() (e error) { out, e = o.Observe(ctx); return })
	return
}

// SerialNumber implements port.BoxIdentifier. An adapter that cannot read its
// serial answers ErrNotImplemented: the model is not supported for Pro.
func (g *SharedGateway) SerialNumber(ctx context.Context) (serial string, err error) {
	b, ok := g.inner.(port.BoxIdentifier)
	if !ok {
		return "", g.unsupported("box serial number")
	}
	err = g.do(ctx, func() (e error) { serial, e = b.SerialNumber(ctx); return })
	return
}

// TrafficCounters implements port.TrafficMeter.
func (g *SharedGateway) TrafficCounters(ctx context.Context) (out map[domain.MAC]domain.TrafficCounters, err error) {
	m, ok := g.inner.(port.TrafficMeter)
	if !ok {
		return nil, g.unsupported("traffic counters")
	}
	err = g.do(ctx, func() (e error) { out, e = m.TrafficCounters(ctx); return })
	return
}
