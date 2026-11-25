package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/ztef6600p"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Issue #94, « proprio prioritaire »: the owner's phone at home takes the
// box's single session; the relay yields without going offline, does not
// take the box back while the owner holds it, and takes it back once the
// owner is away. Against the F6600P mock in its parking mode, which names
// the session that holds the box exactly as the real one does.

// ownerPhone is how the box names the owner's phone at home. The relay is
// 127.0.0.1 here: the mock names a session by the client's address.
const (
	ownerPhone = "192.168.1.17(user)"
	yieldLANIP = "127.0.0.1"
)

type yieldWorld struct {
	t      *testing.T
	owner  *trust.Identity
	member *trust.Identity
	box    *mockrouter.F6600P
	clock  *arClock
	hub    *fakeHub
	agent  *Agent
}

// boxAPI stands in for the in-memory API: like it, each call builds the
// adapter afresh and logs in, with the relay's Preempt policy.
func boxAPI(boxURL string, y *Yield) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/devices" {
			http.Error(rw, "not implemented", http.StatusNotImplemented)
			return
		}
		gw, err := ztef6600p.New(boxURL, port.RouterOptions{Preempt: y.AllowPreempt})
		if err == nil {
			err = gw.Login(r.Context(), domain.Credentials{Username: "admin", Password: "admin"})
		}
		var devices []domain.Device
		if err == nil {
			devices, err = gw.Devices(r.Context())
		}
		switch {
		case errors.Is(err, domain.ErrSessionHeld):
			http.Error(rw, err.Error(), http.StatusServiceUnavailable)
		case err != nil:
			http.Error(rw, err.Error(), http.StatusBadGateway)
		default:
			_ = json.NewEncoder(rw).Encode(devices)
		}
	})
}

func newYieldWorld(t *testing.T) *yieldWorld {
	t.Helper()
	gen := func() *trust.Identity {
		id, err := trust.Generate(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	relay := gen()
	w := &yieldWorld{t: t, owner: gen(), member: gen(), box: mockrouter.NewF6600P("admin", "admin"),
		clock: &arClock{t: arT0}, hub: &fakeHub{}}
	w.box.EnableParking()
	boxSrv := httptest.NewServer(w.box.Handler())
	t.Cleanup(boxSrv.Close)

	b := trust.Bundle{
		TenantID: arTenant, Version: 1, IssuedAt: arT0, Owner: w.owner.Public(),
		Relays: []trust.Relay{{AgentID: arAgent, Kind: "phone", Keys: relay.Public()}},
	}
	if err := b.Delegate(w.member.Public(), trust.ScopeControl, "Awa", arT0); err != nil {
		t.Fatal(err)
	}
	s, err := trust.Sign(w.owner, trust.DomainBundle, b)
	if err != nil {
		t.Fatal(err)
	}
	w.hub.bundle = s
	hubSrv := httptest.NewServer(w.hub)
	t.Cleanup(hubSrv.Close)

	y := NewYield()
	ag, err := New(Config{
		HubURL: hubSrv.URL, Handler: boxAPI(boxSrv.URL, y), Yield: y,
		State: &MemoryState{Data: StateData{
			HubURL: hubSrv.URL, AgentID: arAgent, TenantID: arTenant, Token: "kga_test",
			Identity: relay.Marshal(), OwnerKey: w.owner.Public().SigningKey(),
		}},
		LANIP: yieldLANIP,
		Clock: w.clock.now,
		Timings: Timings{Heartbeat: time.Second, LongPoll: time.Millisecond, SnapshotEvery: time.Second,
			HubTimeout: 5 * time.Second, YieldBackoff: time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.agent = ag
	w.round() // takes the lease and logs in for its first snapshot
	if got := w.box.Holder(); got != yieldLANIP+"(user)" {
		t.Fatalf("after the first round the box is held by %q, want the relay", got)
	}
	return w
}

// round lets a snapshot come due and runs one turn of the relay's loop.
func (w *yieldWorld) round() {
	w.clock.set(w.clock.now().Add(2 * time.Second))
	w.agent.tick(context.Background())
}

func (w *yieldWorld) lastPaused() bool {
	w.hub.mu.Lock()
	defer w.hub.mu.Unlock()
	return w.hub.paused[len(w.hub.paused)-1]
}

// send is the hub handing the relay a command signed by who.
func (w *yieldWorld) send(who *trust.Identity, method, path string) (int, string) {
	w.t.Helper()
	sc, err := trust.NewCommand(who, arTenant, method, path, nil, w.clock.now(), 30*time.Second)
	if err != nil {
		w.t.Fatal(err)
	}
	cmd, _ := sc.Unverified()
	status, body := w.agent.run(context.Background(), &queuedCommand{ID: cmd.CmdID, Epoch: 1, Envelope: sc})
	return status, string(body)
}

func TestYield_PreemptedRelayPausesInsteadOfGoingOffline(t *testing.T) {
	w := newYieldWorld(t)

	// The owner comes home: their app takes the box.
	w.box.HoldSession(ownerPhone)
	preempts := w.box.Preempts()
	w.round()

	st := w.agent.Status()
	if st.State != StatusPaused || !strings.Contains(st.Reason, ownerPhone) {
		t.Fatalf("status = %+v, want paused, naming the owner's session", st)
	}
	if w.box.Holder() != ownerPhone || w.box.Preempts() != preempts {
		t.Fatalf("box held by %q after %d preempts: the relay took the owner's session", w.box.Holder(), w.box.Preempts()-preempts)
	}

	// While the owner holds it, the relay keeps beating (paused, not
	// offline), and never logs in beside them again, let alone preempts.
	logins := w.box.Logins()
	for range 10 {
		w.round()
		if st := w.agent.Status(); st.State != StatusPaused {
			t.Fatalf("status = %+v during the owner's session, want paused", st)
		}
		if !w.lastPaused() {
			t.Fatal("the heartbeat does not tell the hub the relay is paused")
		}
	}
	if w.box.Logins() != logins || w.box.Preempts() != preempts || w.box.Holder() != ownerPhone {
		t.Fatalf("the paused relay touched the box: %d logins, %d preempts, holder %q",
			w.box.Logins()-logins, w.box.Preempts()-preempts, w.box.Holder())
	}
	if !w.agent.active {
		t.Fatal("the paused relay gave up its lease: another relay would take the box from the owner")
	}
}

func TestYield_MemberCommandWaitsWhileTheOwnerIsHome(t *testing.T) {
	w := newYieldWorld(t)
	w.box.HoldSession(ownerPhone)
	w.round()

	logins := w.box.Logins()
	status, body := w.send(w.member, "GET", "/api/devices")
	if status != http.StatusServiceUnavailable || !strings.Contains(body, "owner_at_home") {
		t.Fatalf("member command while paused = %d %s, want 503 owner_at_home", status, body)
	}
	if w.box.Logins() != logins || w.box.Holder() != ownerPhone {
		t.Fatal("a member's command reached the box while the owner holds it")
	}
}

func TestYield_OwnerCommandFromAwayTakesTheBoxBack(t *testing.T) {
	w := newYieldWorld(t)
	w.box.HoldSession(ownerPhone)
	w.round()

	// The owner left: their app goes through the hub, and the box still
	// holds the session their phone left open.
	before := w.box.Preempts()
	status, body := w.send(w.owner, "GET", "/api/devices")
	if status != http.StatusOK {
		t.Fatalf("owner command = %d %s, want 200", status, body)
	}
	if n := w.box.Preempts() - before; w.box.Holder() != yieldLANIP+"(user)" || n != 1 {
		t.Fatalf("box held by %q after %d preempts, want the relay after one", w.box.Holder(), n)
	}
	w.round()
	if st := w.agent.Status(); st.State != StatusActive {
		t.Fatalf("status after the owner's command = %+v, want active", st)
	}
	if w.lastPaused() {
		t.Fatal("the heartbeat still says paused")
	}
}

func TestYield_TakesTheBoxBackAfterTheBackoffOnceTheOwnerIsGone(t *testing.T) {
	w := newYieldWorld(t)
	w.box.HoldSession(ownerPhone)
	preempts := w.box.Preempts()
	w.round()

	// The owner is still there when the backoff runs out: one login to
	// look, no preempt, and the relay pauses again.
	w.clock.set(w.clock.now().Add(time.Minute))
	logins := w.box.Logins()
	w.round()
	if st := w.agent.Status(); st.State != StatusPaused {
		t.Fatalf("status = %+v with the owner still home, want paused", st)
	}
	if n := w.box.Logins() - logins; n != 1 || w.box.Preempts() != preempts || w.box.Holder() != ownerPhone {
		t.Fatalf("looking again cost %d logins and %d preempts, holder %q", n, w.box.Preempts()-preempts, w.box.Holder())
	}

	// The owner's session ended on the box (they left, it timed out): after
	// the next backoff the relay is back on the box.
	w.box.DropSession()
	w.clock.set(w.clock.now().Add(time.Minute))
	w.round()
	if st := w.agent.Status(); st.State != StatusActive {
		t.Fatalf("status = %+v once the owner is gone, want active", st)
	}
	if w.box.Holder() != yieldLANIP+"(user)" {
		t.Fatalf("box held by %q, want the relay back on it", w.box.Holder())
	}
}

func TestYield_RelayStillEvictsItsOwnStaleSession(t *testing.T) {
	w := newYieldWorld(t)
	// Every call logs in again and nothing logs out: the box parks each new
	// login behind the relay's previous one, which the relay must evict.
	before := w.box.Preempts()
	for range 3 {
		w.round()
	}
	if w.box.Preempts() <= before {
		t.Fatal("the relay did not evict its own previous session")
	}
	if st := w.agent.Status(); st.State != StatusActive {
		t.Fatalf("status = %+v, want active", st)
	}
}

func TestYield_PolicyOnLabels(t *testing.T) {
	clock := &arClock{t: arT0}
	y := NewYield()
	y.setup("192.168.1.5", clock.now, time.Minute, nil)
	for _, c := range []struct {
		holder string
		want   bool
	}{
		{"192.168.1.5(user)", true},   // its own stale session
		{"unknown", true},             // nothing to go by: the old behaviour
		{"", true},                    //
		{"192.168.1.17(user)", false}, // somebody else: the owner at home
	} {
		if got := y.AllowPreempt(c.holder); got != c.want {
			t.Errorf("AllowPreempt(%q) = %v, want %v", c.holder, got, c.want)
		}
	}
	if paused, holder := y.Paused(); !paused || holder != "192.168.1.17(user)" {
		t.Fatalf("Paused() = %v %q after yielding", paused, holder)
	}
	clock.set(arT0.Add(time.Minute))
	if paused, _ := y.Paused(); paused {
		t.Fatal("still paused after the backoff")
	}

	// A relay that does not know its own address never yields: it could not
	// tell its own session from the owner's and would lock itself out.
	blind := NewYield()
	blind.setup("", clock.now, time.Minute, nil)
	if !blind.AllowPreempt("192.168.1.17(user)") {
		t.Fatal("a relay without its own address yielded")
	}
	// Nil (no relay running) is the owner's app: it always takes the box.
	var none *Yield
	if !none.AllowPreempt("192.168.1.5(user)") {
		t.Fatal("a nil policy yielded")
	}
}
