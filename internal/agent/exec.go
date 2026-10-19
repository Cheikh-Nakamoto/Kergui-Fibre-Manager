package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// allowed is the relay's allow-list (RFC-005, design rule 4). It is a list
// of what may run, not a list of what may not: a route added to the API
// later is unreachable from the hub until someone puts it here on purpose.
//
// /api/login and /api/discover are absent by design. Login would let a
// command change which box the relay talks to, and discover would turn the
// relay into an SSRF probe on the home LAN.
var allowed = []route{
	{http.MethodGet, "/api/devices", false},
	{http.MethodGet, "/api/wifi/ssids", false},
	{http.MethodGet, "/api/wifi/blocked", false},
	{http.MethodGet, "/api/filter", false},
	{http.MethodGet, "/api/usage", false},
	{http.MethodPost, "/api/filter/toggle", false},
	{http.MethodPost, "/api/devices/+/block", true},
	{http.MethodPost, "/api/devices/+/unblock", true},
	{http.MethodPost, "/api/devices/+/wifi-block", true},
	{http.MethodPost, "/api/devices/+/wifi-unblock", true},
}

// route is one allowed call. "+" matches exactly one path segment, and
// targetsDevice says that segment is the MAC the call acts on.
type route struct {
	method        string
	pattern       string
	targetsDevice bool
}

// Refusals the relay produces on its own, before the box is touched.
var (
	ErrNotAllowed = errors.New("agent: this call is not on the relay's allow-list")
	ErrSelfHarm   = errors.New("agent: refusing to cut the relay itself")
	ErrProtected  = errors.New("agent: this device is protected")
	// ErrNotPresent refuses the hub's narrow key a device the relay cannot
	// see on the network right now.
	ErrNotPresent = errors.New("agent: this device is not on the network")
)

// checkAllowed matches a command against the allow-list and returns the
// device it acts on, if any.
func checkAllowed(method, rawPath string) (target string, err error) {
	// Normalise before matching: "/api/devices/../login" and a trailing
	// slash must not be able to reach something the list does not name.
	if strings.ContainsAny(rawPath, "?#") {
		return "", fmt.Errorf("%w: %s %s", ErrNotAllowed, method, rawPath)
	}
	clean := path.Clean("/" + strings.TrimPrefix(rawPath, "/"))
	segments := strings.Split(strings.TrimPrefix(clean, "/"), "/")

	for _, r := range allowed {
		if !strings.EqualFold(r.method, method) {
			continue
		}
		want := strings.Split(strings.TrimPrefix(r.pattern, "/"), "/")
		if len(want) != len(segments) {
			continue
		}
		match, got := true, ""
		for i, w := range want {
			switch {
			case w == "+":
				if segments[i] == "" {
					match = false
				}
				got = segments[i]
			case w != segments[i]:
				match = false
			}
			if !match {
				break
			}
		}
		if !match {
			continue
		}
		if r.targetsDevice {
			return got, nil
		}
		return "", nil
	}
	return "", fmt.Errorf("%w: %s %s", ErrNotAllowed, method, clean)
}

// pollOutcome says what one turn of the long poll produced.
type pollOutcome int

const (
	// pollServed ran a command; there may be another waiting.
	pollServed pollOutcome = iota
	// pollIdle waited and found nothing: time to heartbeat again.
	pollIdle
	// pollFailed lost the hub or the lease: stand down and back off.
	pollFailed
)

// serveOne waits for one command and runs it.
func (a *Agent) serveOne(ctx context.Context) pollOutcome {
	cmd, err := a.hub.next(ctx, a.timings.LongPoll)
	switch {
	case errors.Is(err, errLeaseLost):
		a.active = false
		return pollFailed
	case err != nil:
		if ctx.Err() == nil {
			a.log.Warnf("agent: long poll: %v", err)
		}
		a.active = false // we no longer know whether we hold the box
		return pollFailed
	case cmd == nil:
		return pollIdle
	}

	status, body := a.run(ctx, cmd)
	res, err := trust.NewResult(a.state.identity, a.state.TenantID, cmd.ID, cmd.Epoch, status, body, a.now())
	if err != nil {
		a.log.Errorf("agent: signing the result: %v", err)
		return pollServed
	}
	if err := a.hub.postResult(ctx, cmd.ID, res); err != nil {
		a.log.Warnf("agent: posting the result: %v", err)
	}
	// A write changes what the household sees, so refresh the snapshot
	// rather than leave the app showing the old state for a minute.
	if status < 300 && !isRead(cmd) {
		a.maybeSnapshot(ctx, true)
	}
	return pollServed
}

// run verifies a command and, if it passes, executes it on the in-memory
// API. Every refusal is answered — signed — so the app sees why rather than
// waiting for a timeout.
func (a *Agent) run(ctx context.Context, q *queuedCommand) (int, []byte) {
	cmd, err := a.verifier.VerifyCommand(q.Envelope, q.Body, a.now())
	if err != nil {
		a.log.Warnf("agent: refusing a command from the hub: %v", err)
		return http.StatusForbidden, refusal(err)
	}
	target, err := checkAllowed(cmd.Method, cmd.Path)
	if err != nil {
		a.log.Warnf("agent: %v", err)
		return http.StatusForbidden, refusal(err)
	}
	if target != "" {
		if err := a.guardDevice(ctx, cmd, target); err != nil {
			a.log.Warnf("agent: %v", err)
			return http.StatusForbidden, refusal(err)
		}
	}
	return a.call(ctx, cmd.Method, cmd.Path, q.Body)
}

// guardDevice applies RFC-005's design rule 5: never cut the relay, and
// never a protected device. Reads are untouched.
func (a *Agent) guardDevice(ctx context.Context, cmd trust.Command, mac string) error {
	if cmd.Method == http.MethodGet {
		return nil
	}
	if b := a.verifier.Bundle(); b != nil && b.IsProtected(mac) {
		return fmt.Errorf("%w: %s", ErrProtected, mac)
	}
	// The hub's narrow WhatsApp key may only act on a device this relay has
	// itself seen on the network. The hub composes the alert the household
	// answered, so without this it could name an address that was never
	// there — and the relay would dutifully block it.
	if a.verifier.IsHubBlock(cmd.KeyID) {
		if err := a.requirePresent(ctx, mac); err != nil {
			return err
		}
	}
	own, err := a.ownDeviceMAC(ctx)
	if err != nil {
		a.log.Warnf("agent: cannot tell which device I am: %v", err)
		return nil // do not block the household because a read failed
	}
	if own != "" && sameMAC(own, mac) {
		return fmt.Errorf("%w: %s carries this relay", ErrSelfHarm, mac)
	}
	return nil
}

// requirePresent refuses a MAC the box does not currently list. The read is
// fresh on purpose: this runs on the rare narrow-key path, and a cached list
// is exactly what a hub holding a command back would exploit.
//
// A device list that cannot be read is a refusal here, not a shrug: the
// narrow key's whole justification is that the relay checked.
func (a *Agent) requirePresent(ctx context.Context, mac string) error {
	macs, err := a.deviceMACs(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotPresent, err)
	}
	for _, m := range macs {
		if sameMAC(m, mac) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrNotPresent, mac)
}

// deviceMACs reads the addresses the box currently reports.
func (a *Agent) deviceMACs(ctx context.Context) ([]string, error) {
	body, err := a.read(ctx, "/api/devices")
	if err != nil {
		return nil, err
	}
	var devices []struct {
		MAC domain.MAC `json:"MAC"`
	}
	if err := json.Unmarshal(body, &devices); err != nil {
		return nil, fmt.Errorf("agent: decoding devices: %w", err)
	}
	out := make([]string, 0, len(devices))
	for _, d := range devices {
		out = append(out, d.MAC.String())
	}
	return out, nil
}

// ownDeviceMAC finds this relay's own MAC by matching its LAN IP in the
// device list, and caches it: it does not change while the process runs.
func (a *Agent) ownDeviceMAC(ctx context.Context) (string, error) {
	if a.ownMAC != "" || a.cfg.LANIP == "" {
		return a.ownMAC, nil
	}
	status, body := a.call(ctx, http.MethodGet, "/api/devices", nil)
	if status != http.StatusOK {
		return "", fmt.Errorf("agent: reading devices returned %d", status)
	}
	var devices []struct {
		MAC domain.MAC `json:"MAC"`
		IP  domain.IP  `json:"IP"`
	}
	if err := json.Unmarshal(body, &devices); err != nil {
		return "", fmt.Errorf("agent: decoding devices: %w", err)
	}
	for _, d := range devices {
		if d.IP.String() == a.cfg.LANIP {
			a.ownMAC = d.MAC.String()
			a.verifier.Protect(a.ownMAC) // narrow keys are refused it too
			return a.ownMAC, nil
		}
	}
	return "", nil
}

// call runs one request against the in-memory API, exactly as
// mobilecore.Core.Request does.
func (a *Agent) call(ctx context.Context, method, target string, body []byte) (int, []byte) {
	var rdr *bytes.Reader
	if len(body) > 0 {
		rdr = bytes.NewReader(body)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://relay.local"+target, rdr)
	if err != nil {
		return http.StatusBadRequest, refusal(err)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	a.cfg.Handler.ServeHTTP(rec, req)
	return rec.status, rec.body.Bytes()
}

// refusal is the body of a refused command: a code the app can show,
// without any detail about the household's configuration.
func refusal(err error) []byte {
	b, _ := json.Marshal(map[string]string{"error": err.Error()})
	return b
}

func isRead(q *queuedCommand) bool {
	cmd, err := q.Envelope.Unverified()
	return err == nil && cmd.Method == http.MethodGet
}

func sameMAC(a, b string) bool {
	norm := func(s string) string {
		return strings.ToLower(strings.NewReplacer(":", "", "-", "", ".", "").Replace(strings.TrimSpace(s)))
	}
	return norm(a) == norm(b)
}

// recorder is the in-memory http.ResponseWriter the API writes into.
type recorder struct {
	header      http.Header
	body        bytes.Buffer
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
