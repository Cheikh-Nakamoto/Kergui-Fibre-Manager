package mobilecore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/controller/httpapi"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/httpkit"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// Running the phone relay (#45). The Android foreground service (RelayService)
// calls RelayStart once the phone is paired; the loop is internal/agent's,
// the same as `kergui serve --hub`: heartbeat every 20 s, the lease, the long
// poll of signed commands, signed results and snapshots. What is the phone's
// own is around it:
//
//   - RelayNetwork, implemented in Kotlin, reports the Wi-Fi (SSID / BSSID,
//     "" without the location permission) and binds every socket the core
//     opens to the home Wi-Fi: Go's sockets do not follow Android's
//     bindProcessToNetwork, so the box and the hub are dialled through it.
//   - the agent checks the network the owner signed and stands down on its
//     own on another Wi-Fi or in front of another box; without the Wi-Fi
//     name it checks the box identity alone, and RelayStatus says so.
//   - one box session: the relay's calls and the app's (Request, the watch
//     service) take the same lock, and they share the core's single
//     session, so they never log each other out.

// RelayNetwork is the phone's view of its network. Implemented on the
// Kotlin side; every method must answer quickly.
type RelayNetwork interface {
	// SSID of the Wi-Fi the phone is on, "" when unknown (Android needs the
	// location permission to read it).
	SSID() string
	// BSSID of the access point, "" when unknown.
	BSSID() string
	// LANIP is the phone's address on the Wi-Fi, "" when unknown.
	LANIP() string
	// BindSocket binds a socket (a file descriptor, not yet connected) to
	// the home Wi-Fi. false refuses the connection: the phone is not on it.
	BindSocket(fd int) bool
}

// relayTimings pace the phone relay: RFC-005's production values.
var relayTimings = agent.DefaultTimings()

// relayRun is the relay loop while it runs.
type relayRun struct {
	agent  *agent.Agent
	cancel context.CancelFunc
	done   chan struct{}
}

// relayState keeps the agent's state in the core's vault.
type relayState struct{ c *Core }

func (s relayState) Load() (agent.StateData, error) { return s.c.loadRelay(context.Background()) }

func (s relayState) Save(d agent.StateData) error { return s.c.saveRelay(context.Background(), d) }

// errRelayDemo is RelayStart in the demo: it has no hub.
var errRelayDemo = errors.New("mobilecore: the demo has no hub to relay for")

// RelayStart runs this phone as the household's relay until RelayStop. It
// needs the pairing (RelayEnroll) and the box the core is pointed at
// (Configure). network may be nil (tests, a phone without the bridge): no
// Wi-Fi is reported then, and the sockets are not bound.
func (c *Core) RelayStart(network RelayNetwork) error {
	c.relayMu.Lock()
	defer c.relayMu.Unlock()
	if c.relay != nil {
		return nil // already running: the service may be started twice
	}
	c.mu.RLock()
	cfg, demo := c.routerCfg, c.demoURL != ""
	c.mu.RUnlock()
	if demo {
		return errRelayDemo
	}
	if cfg.BaseURL == "" {
		return errors.New("mobilecore: not configured (connect to the box first)")
	}
	state, err := c.loadRelay(context.Background())
	if err != nil {
		return err
	}
	if network != nil {
		c.bindNet.Store(&network)
	}

	lanIP := ""
	if network != nil {
		lanIP = network.LANIP()
	}
	if lanIP == "" {
		if ip := c.self.SelfIP(context.Background(), cfg.BaseURL); !ip.IsZero() {
			lanIP = ip.String()
		}
	}
	c.gate.mu.Lock()
	licensing := c.gate.keys
	c.gate.mu.Unlock()
	opts := cfg.Opts
	target := usecase.MACFilterInput{BaseURL: cfg.BaseURL, AdapterID: cfg.AdapterID, Opts: opts}
	ag, err := agent.New(agent.Config{
		HubURL:  state.HubURL,
		Handler: c.relayHandler(cfg),
		State:   relayState{c},
		Router: func(ctx context.Context) (string, error) {
			return httpkit.ProbeFingerprintVia(ctx, cfg.BaseURL, fingerprintTimeout, c.dialControl)
		},
		WiFi: func(context.Context) (string, string) {
			if network == nil {
				return "", ""
			}
			return network.SSID(), network.BSSID()
		},
		LANIP:   lanIP,
		Version: version,
		Credentials: func(ctx context.Context, creds domain.Credentials) error {
			return c.vault.Store(ctx, usecase.RouterID(cfg.BaseURL), creds)
		},
		AutoReject: lockedRejecter{c: c, inner: agent.NewAutoRejecter(c.svc.AutoReject, target)},
		Licensing:  licensing,
		BoxSerial: func(ctx context.Context) (string, error) {
			c.io.Lock()
			defer c.io.Unlock()
			return agent.InspectSerial(c.svc.Inspect, usecase.InspectInput{BaseURL: cfg.BaseURL, AdapterID: cfg.AdapterID, Opts: opts})(ctx)
		},
		Endpoints: func(ctx context.Context) ([]domain.EndpointDoc, error) {
			c.io.Lock()
			defer c.io.Unlock()
			return agent.InspectEndpoints(c.svc.Inspect, usecase.InspectInput{BaseURL: cfg.BaseURL, AdapterID: cfg.AdapterID, Opts: opts})(ctx)
		},
		Log:     c.log,
		Timings: relayTimings,
		HTTP:    &http.Client{Transport: c.hubTransport()},
	})
	if err != nil {
		c.bindNet.Store(nil)
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &relayRun{agent: ag, cancel: cancel, done: make(chan struct{})}
	c.relay = run
	go func() {
		defer close(run.done)
		if err := ag.Run(ctx); err != nil {
			c.log.Errorf("relay: stopped: %v", err)
		}
	}()
	// Recovery of a lost owner phone (#48): answered on the home Wi-Fi
	// only, on the phone's own address, never through the hub.
	if lanIP != "" {
		rec := agent.NewRecovery(agent.RecoveryConfig{
			State: relayState{c},
			Password: func(ctx context.Context) (string, error) {
				creds, ok, err := c.vault.Load(ctx, usecase.RouterID(cfg.BaseURL))
				if err != nil || !ok {
					return "", err
				}
				return creds.Password, nil
			},
			Log: c.log,
		})
		go func() {
			if err := agent.ServeRecovery(ctx, agent.RecoveryAddr(lanIP), rec, c.log); err != nil {
				c.log.Warnf("relay: recovery listener: %v", err)
			}
		}()
	}
	c.log.Infof("relay: started for household %s (Wi-Fi bound: %t)", state.TenantID, network != nil)
	return nil
}

// RelayStop stands the relay down: it hands the lease back to the hub and
// unbinds the sockets. It waits for the loop to end, a few seconds at most.
func (c *Core) RelayStop() {
	c.relayMu.Lock()
	run := c.relay
	c.relay = nil
	c.relayMu.Unlock()
	if run == nil {
		return
	}
	run.cancel()
	select {
	case <-run.done:
	case <-time.After(relayTimings.HubTimeout + time.Second):
		c.log.Warnf("relay: the loop did not end in time")
	}
	c.bindNet.Store(nil)
	c.log.Infof("relay: stopped")
}

// RelayRunning reports whether the relay loop runs.
func (c *Core) RelayRunning() bool {
	c.relayMu.Lock()
	defer c.relayMu.Unlock()
	return c.relay != nil
}

// relayStatusJSON is what RelayStatus answers.
type relayStatusJSON struct {
	Running bool `json:"running"`
	// State is stopped, starting, active, standby, offline or wrong-network.
	State string `json:"state"`
	// Reason says why it stands by or down, or that only the box identity
	// was checked. English, for the journal; the app words it.
	Reason string `json:"reason"`
	// Check is signature, box-identity or "".
	Check string `json:"check"`
	// AutoReject is whether this relay enforces auto-reject: the phone's
	// own watch service then leaves the rule to it.
	AutoReject bool   `json:"autoReject"`
	At         string `json:"at,omitempty"`
}

// RelayStatus returns the relay's last round as JSON {running, state,
// reason, check, autoReject, at}. It never fails and carries no secret.
func (c *Core) RelayStatus() string {
	c.relayMu.Lock()
	run := c.relay
	c.relayMu.Unlock()
	out := relayStatusJSON{State: "stopped"}
	if run != nil {
		st := run.agent.Status()
		out = relayStatusJSON{Running: true, State: st.State, Reason: st.Reason, Check: st.Check, AutoReject: st.AutoReject}
		if !st.At.IsZero() {
			out.At = st.At.UTC().Format(time.RFC3339)
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// relayHandler is the API the relay runs the owner's signed commands on: the
// same httpapi handler, without the core's paid-action gate. The owner's app
// gates what it signs, the relay's allow-list bounds what runs, and the PC
// relay has no gate either; a dedicated phone holds no pass of its own.
func (c *Core) relayHandler(cfg httpapi.Config) http.Handler {
	svc := c.svc
	svc.Gate = nil
	inner := httpapi.New(svc, cfg).Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.io.Lock()
		defer c.io.Unlock()
		inner.ServeHTTP(w, r)
	})
}

// lockedRejecter runs the relay's auto-reject under the core's I/O lock.
type lockedRejecter struct {
	c     *Core
	inner agent.AutoRejecter
}

func (l lockedRejecter) Adopt(ctx context.Context, p usecase.AutoRejectPolicy) error {
	l.c.io.Lock()
	defer l.c.io.Unlock()
	return l.inner.Adopt(ctx, p)
}

func (l lockedRejecter) Enforce(ctx context.Context) (usecase.AutoRejectRun, error) {
	l.c.io.Lock()
	defer l.c.io.Unlock()
	return l.inner.Enforce(ctx)
}

// errNotOnHomeWiFi is a socket the phone refused to bind: it is not on the
// home Wi-Fi, and the box must not be reached over mobile data.
var errNotOnHomeWiFi = errors.New("mobilecore: not on the home Wi-Fi")

// dialControl binds every socket the core opens to the home Wi-Fi while the
// relay runs (RouterOptions.DialControl). Without a relay it does nothing.
func (c *Core) dialControl(_, _ string, rc syscall.RawConn) error {
	p := c.bindNet.Load()
	if p == nil || *p == nil {
		return nil
	}
	network := *p
	ok := false
	if err := rc.Control(func(fd uintptr) { ok = network.BindSocket(int(fd)) }); err != nil {
		return fmt.Errorf("mobilecore: bind socket: %w", err)
	}
	if !ok {
		return errNotOnHomeWiFi
	}
	return nil
}

// hubTransport dials the hub through dialControl too: the relay speaks to
// the hub over the home Wi-Fi it relays for.
func (c *Core) hubTransport() http.RoundTripper {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: c.dialControl}).DialContext
	return tr
}

// relayFields are the Core's relay state, embedded to keep core.go short.
type relayFields struct {
	relayMu sync.Mutex
	relay   *relayRun
	// bindNet is the network the sockets are bound to while the relay runs.
	bindNet atomic.Pointer[RelayNetwork]
	// io serialises every call that reaches the box: the box keeps a single
	// admin session and guards each page with the view loaded just before
	// it, so two calls at once log each other out.
	io sync.Mutex
}

// RelayFindBox points the core at the household's box, for a relay phone
// that never went through the connection screen (the password comes sealed
// from the owner). candidates are comma-separated https URLs the service
// suggests (the Wi-Fi's gateway first); the box is the one whose certificate
// is the pin in the bundle the owner signed. It returns the URL chosen.
func (c *Core) RelayFindBox(candidates string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	state, err := c.loadRelay(ctx)
	if err != nil {
		return "", err
	}
	v := trust.NewVerifier(state.TenantID, state.OwnerKey, time.Now())
	if len(state.Bundle.Payload) == 0 {
		return "", trust.ErrNoBundle
	}
	if err := v.AcceptBundle(state.Bundle); err != nil {
		return "", err
	}
	want := strings.TrimSpace(v.Bundle().Network.RouterPin)
	if want == "" {
		return "", errors.New("mobilecore: the owner has not registered the box yet")
	}
	for _, raw := range strings.Split(candidates, ",") {
		u := strings.TrimSpace(raw)
		if u == "" {
			continue
		}
		pin, err := httpkit.ProbeFingerprintVia(ctx, u, fingerprintTimeout/4, c.dialControl)
		if err != nil || !strings.EqualFold(pin, want) {
			continue
		}
		if err := c.Configure(u, "", pin); err != nil {
			return "", err
		}
		c.log.Infof("relay: found the household's box at %s", u)
		return u, nil
	}
	return "", errors.New("mobilecore: the household's box does not answer on this Wi-Fi")
}
