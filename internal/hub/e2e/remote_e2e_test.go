package e2e

// Remote mode (issue #46): the owner's app goes through the hub, even on the
// home Wi-Fi (RFC-005 design rule 1). The owner is a real mobilecore.Core:
// it reads the device list the PC relay signed, signs a block and an unblock
// with the owner's key after its own paid-action gate, and believes the
// outcome only once the relay's signature over it checks. The box is the
// mock F6600P; the relay is the one `kergui serve --hub` runs. With the
// relay gone, the app is told it is offline and the owner's core never logs
// in to the box in its place.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/httpkit"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/logging"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// mockBoxSerial is the serial the captured F6600P fixture reports.
const mockBoxSerial = "ZTEG00000001"

// relayState is GET router/relay.
func relayState(t *testing.T, w *world) map[string]any {
	t.Helper()
	code, body := w.owner.call("GET", w.owner.tenantPath("router/relay"), nil)
	if code != http.StatusOK {
		t.Fatalf("GET router/relay = %d %v", code, body)
	}
	return body
}

func waitForRelayStatus(t *testing.T, w *world, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		if last = relayState(t, w); str(last, "state") == want {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the relay never became %s: last %v", want, last)
	return nil
}

// remoteDevices is the app's device list in remote mode: the hub's answer,
// opened by the core against the owner's bundle.
func (p *phoneOwnerApp) remoteDevices() (asOf time.Time, devices []map[string]any) {
	p.t.Helper()
	code, body := p.call("GET", p.tenantPath("router/devices"), nil)
	if code != http.StatusOK {
		p.t.Fatalf("GET router/devices = %d %v", code, body)
	}
	snap, _ := json.Marshal(body["snapshot"])
	raw, err := p.core.OpenDeviceSnapshot(p.tenantID, p.currentBundle(), string(snap))
	if err != nil {
		p.t.Fatalf("OpenDeviceSnapshot: %v", err)
	}
	var out struct {
		AsOf    time.Time        `json:"asOf"`
		Devices []map[string]any `json:"devices"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		p.t.Fatal(err)
	}
	return out.AsOf, out.Devices
}

// remoteCommand is the app's block / unblock in remote mode: signed in the
// core, POSTed to the hub. It returns the hub's status, its answer and the
// command the core signed.
func (p *phoneOwnerApp) remoteCommand(mac, action string) (int, map[string]any, string, error) {
	p.t.Helper()
	raw, err := p.core.SignDeviceCommand(p.tenantID, mac, action, mockBoxSerial)
	if err != nil {
		return 0, nil, "", err
	}
	var signed struct {
		Payload []byte `json:"payload"`
		Sig     []byte `json:"sig"`
		CmdID   string `json:"cmdId"`
		MAC     string `json:"mac"`
	}
	if err := json.Unmarshal([]byte(raw), &signed); err != nil {
		p.t.Fatal(err)
	}
	code, body := p.call("POST", p.tenantPath("router/devices/"+signed.MAC+"/"+action),
		map[string]any{"payload": signed.Payload, "sig": signed.Sig})
	return code, body, signed.CmdID, nil
}

// verified is the ✅: the relay's signature over the result, checked by the
// core against the owner's bundle. It returns what the box answered.
func (p *phoneOwnerApp) verified(cmdID string, body map[string]any) int {
	p.t.Helper()
	res, _ := json.Marshal(body["result"])
	raw, err := p.core.VerifyCommandResult(p.tenantID, cmdID, p.currentBundle(), string(res))
	if err != nil {
		p.t.Fatalf("VerifyCommandResult: %v", err)
	}
	var out struct {
		Status int `json:"status"`
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out.Status
}

func deviceBlocked(devices []map[string]any, mac string) (blocked, found bool) {
	for _, d := range devices {
		if strings.EqualFold(str(d, "MAC"), mac) {
			b, _ := d["Blocked"].(bool)
			return b, true
		}
	}
	return false, false
}

func TestE2E_RemoteModeBlockFromTheOwnersCore(t *testing.T) {
	w := newWriteWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The owner's phone, logged in to the box once (the password it seals
	// for the relay), then pairing the PC relay as #43 and #44 do.
	owner := newPhoneOwnerApp(t, w.owner)
	if err := owner.core.Configure(w.boxURL, "zte_f6600p", w.boxPin); err != nil {
		t.Fatal(err)
	}
	owner.loginToBox(boxPass)
	w.owner.registerNetwork(w.boxPin)

	if st := str(relayState(t, w), "state"); st != "none" {
		t.Fatalf("before pairing, the relay is %q, want none", st)
	}

	owner.signAndUpload(map[string]any{"routerPin": w.boxPin})
	inv, err := trust.ParseInvite(owner.showQR("pc"))
	if err != nil {
		t.Fatal(err)
	}
	state, res, err := agent.Enroll(ctx, w.hub.URL, inv, "PC du salon", "pc", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	listed := owner.agent(res.AgentID)
	if owner.ownerCode(listed) != res.Code {
		t.Fatal("the two pairing codes differ")
	}
	owner.confirm(listed)
	owner.uploadSealed()

	// Remote mode is on for this phone: the bundle its key signed names a
	// relay.
	var mode struct {
		Owner  bool `json:"owner"`
		Relays []struct {
			AgentID string `json:"agentId"`
		} `json:"relays"`
	}
	raw, err := owner.core.RemoteRelays(w.owner.tenantID, owner.currentBundle())
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(raw), &mode)
	if !mode.Owner || len(mode.Relays) != 1 || mode.Relays[0].AgentID != res.AgentID {
		t.Fatalf("remote mode: %s", raw)
	}

	handler, vlt := newRouterAPI(t, w.boxURL, w.boxPin)
	relayCtx, stopRelay := context.WithCancel(ctx)
	ag, err := agent.New(agent.Config{
		HubURL:  w.hub.URL,
		Handler: handler,
		State:   &agent.MemoryState{Data: state},
		Router: func(ctx context.Context) (string, error) {
			return httpkit.ProbeFingerprint(ctx, w.boxURL, 5*time.Second)
		},
		WiFi:    func(context.Context) (string, string) { return "MonWiFi-2G", "" },
		LANIP:   "192.168.1.50",
		Version: "e2e",
		Credentials: func(ctx context.Context, c domain.Credentials) error {
			return vlt.Store(ctx, usecase.RouterID(w.boxURL), c)
		},
		Log:     logging.New(relayLogWriter(), true),
		Timings: w.timings,
	})
	if err != nil {
		t.Fatal(err)
	}
	relayDone := make(chan struct{})
	go func() { defer close(relayDone); _ = ag.Run(relayCtx) }()

	// The device list, as the relay signed it, with its as_of.
	waitForDevices(t, w)
	active := waitForRelayStatus(t, w, "active")
	if str(active, "agentId") != res.AgentID {
		t.Fatalf("active relay = %v", active)
	}
	asOf, devices := owner.remoteDevices()
	if age := time.Since(asOf); age < 0 || age > time.Minute {
		t.Fatalf("as_of %s is %s old", asOf, age)
	}
	if blocked, found := deviceBlocked(devices, targetMAC); !found || blocked {
		t.Fatalf("%s in the remote list: found=%v blocked=%v", targetMAC, found, blocked)
	}

	// Blocking is Basic: without a pass the owner's core signs nothing, and
	// nothing reaches the hub.
	if _, _, _, err := owner.remoteCommand(targetMAC, "block"); err == nil || !strings.HasPrefix(err.Error(), "not_entitled") {
		t.Fatalf("block without a pass: err = %v", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.core.SetPublicKeys(base64.StdEncoding.EncodeToString(pub)); err != nil {
		t.Fatal(err)
	}
	tok, err := entitlement.Sign(priv, entitlement.Claims{
		Account: "acc-remote", BoxID: mockBoxSerial, Tier: entitlement.TierBasic,
		IssuedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	owner.core.SetEntitlement(tok)

	// Block: signed in the core, carried by the hub, run by the relay on
	// the mock, its result verified against the relay's key.
	code, body, cmdID, err := owner.remoteCommand(targetMAC, "block")
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK || str(body, "status") != "done" {
		t.Fatalf("POST block = %d %v", code, body)
	}
	if status := owner.verified(cmdID, body); status != http.StatusOK {
		t.Fatalf("the relay's signed result says %d", status)
	}
	if !w.box.Blocked(targetMAC) {
		t.Fatal("the device is not blocked on the box")
	}
	// The relay pushes the list again after a write: the app sees it blocked.
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, devices = owner.remoteDevices()
		if blocked, _ := deviceBlocked(devices, targetMAC); blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the remote list never showed the device blocked")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// A result the hub made up for that command does not verify.
	forger, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fake, _ := trust.NewResult(forger, w.owner.tenantID, cmdID, 1, http.StatusOK, nil, time.Now())
	fakeRaw, _ := json.Marshal(fake)
	if _, err := owner.core.VerifyCommandResult(w.owner.tenantID, cmdID, owner.currentBundle(), string(fakeRaw)); !errors.Is(err, trust.ErrUnknownKey) {
		t.Fatalf("a result the hub signed: err = %v", err)
	}

	// Unblock (free), followed through the command status like a slow relay.
	code, body, cmdID, err = owner.remoteCommand(targetMAC, "unblock")
	if err != nil {
		t.Fatal(err)
	}
	if code == http.StatusAccepted {
		if waitForCommandDone(t, w, cmdID) != http.StatusOK {
			t.Fatal("the unblock was never answered")
		}
		_, body = w.owner.call("GET", w.owner.tenantPath("router/commands/"+cmdID), nil)
	} else if code != http.StatusOK {
		t.Fatalf("POST unblock = %d %v", code, body)
	}
	if status := owner.verified(cmdID, body); status != http.StatusOK {
		t.Fatalf("unblock: the relay's signed result says %d", status)
	}
	if w.box.Blocked(targetMAC) {
		t.Fatal("the device is still blocked after unblock")
	}

	// The relay goes away: the app is told, with since when, and the owner's
	// core does not log in to the box in its place.
	stopRelay()
	<-relayDone
	offline := waitForRelayStatus(t, w, "offline")
	if str(offline, "lastSeenAt") == "" || offline["ageSeconds"] == nil {
		t.Fatalf("offline without a date: %v", offline)
	}
	logins := w.box.Logins()
	code, body, cmdID, err = owner.remoteCommand(targetMAC, "block")
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusAccepted || str(body, "status") != "queued" {
		t.Fatalf("block with the relay offline = %d %v, want 202 queued", code, body)
	}
	if _, body := w.owner.call("GET", w.owner.tenantPath("router/commands/"+cmdID), nil); str(body, "status") == "done" {
		t.Fatalf("a command was answered with no relay: %v", body)
	}
	if n := w.box.Logins(); n != logins {
		t.Fatalf("someone logged in to the box with the relay offline (%d -> %d logins)", logins, n)
	}
	if w.box.Blocked(targetMAC) {
		t.Fatal("the device was blocked with no relay")
	}
}
