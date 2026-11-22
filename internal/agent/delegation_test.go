package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Member delegations (issue #47) as the relay enforces them: a member's key
// runs what the owner delegated it, for as long as the owner's app keeps the
// delegation alive, and nothing once it has expired, been revoked, or the
// household is owner-only — whatever the hub lets through. The relay never
// asks the hub: the bundle the owner signed is all it reads.

const memberTarget = "ac:bb:cc:00:00:22"

type memberWorld struct {
	t      *testing.T
	owner  *trust.Identity
	member *trust.Identity
	relay  *trust.Identity
	clock  *arClock
	hub    *fakeHub
	agent  *Agent
	ran    []string // what reached the box
	bundle trust.Bundle
	state  *MemoryState
}

func newMemberWorld(t *testing.T, scope string) *memberWorld {
	t.Helper()
	gen := func() *trust.Identity {
		id, err := trust.Generate(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	w := &memberWorld{t: t, owner: gen(), member: gen(), relay: gen(), clock: &arClock{t: arT0}, hub: &fakeHub{}}
	w.bundle = trust.Bundle{
		TenantID: arTenant, Version: 1, IssuedAt: arT0, Owner: w.owner.Public(),
		Relays: []trust.Relay{{AgentID: arAgent, Kind: "pc", Keys: w.relay.Public()}},
	}
	if err := w.bundle.Delegate(w.member.Public(), scope, "Awa", arT0); err != nil {
		t.Fatal(err)
	}
	w.publish()
	srv := httptest.NewServer(w.hub)
	t.Cleanup(srv.Close)

	box := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.ran = append(w.ran, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/devices" {
			_ = json.NewEncoder(rw).Encode([]map[string]string{{"MAC": arOwnMAC, "IP": arLANIP}, {"MAC": memberTarget, "IP": "192.168.1.22"}})
			return
		}
		_, _ = rw.Write([]byte(`{"ok":true}`))
	})
	w.state = &MemoryState{Data: StateData{
		HubURL: srv.URL, AgentID: arAgent, TenantID: arTenant, Token: "kga_test",
		Identity: w.relay.Marshal(), OwnerKey: w.owner.Public().SigningKey(),
	}}
	ag, err := New(Config{
		HubURL: srv.URL, Handler: box,
		State: w.state,
		LANIP: arLANIP, Clock: w.clock.now,
		Timings: Timings{Heartbeat: time.Second, LongPoll: time.Millisecond, SnapshotEvery: time.Hour, HubTimeout: 5 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.agent = ag
	w.agent.tick(context.Background()) // takes the lease and the bundle
	return w
}

// publish is the owner's app signing the bundle as it now stands.
func (w *memberWorld) publish() {
	w.t.Helper()
	s, err := trust.Sign(w.owner, trust.DomainBundle, w.bundle)
	if err != nil {
		w.t.Fatal(err)
	}
	w.hub.mu.Lock()
	w.hub.bundle = s
	w.hub.bundleVer = w.bundle.Version
	w.hub.mu.Unlock()
}

// change has the owner sign a new bundle, which the relay picks up on its
// next heartbeat.
func (w *memberWorld) change(f func(b *trust.Bundle)) {
	w.t.Helper()
	f(&w.bundle)
	w.bundle.Version++
	w.publish()
	w.agent.tick(context.Background())
	if got := w.agent.bundleVersion(); got != w.bundle.Version {
		w.t.Fatalf("the relay holds bundle v%d, want v%d", got, w.bundle.Version)
	}
}

// send is the hub handing the relay a command the member signed, now.
func (w *memberWorld) send(method, path string) (int, string) {
	w.t.Helper()
	at := w.clock.now()
	sc, err := trust.NewCommand(w.member, arTenant, method, path, nil, at, 30*time.Second)
	if err != nil {
		w.t.Fatal(err)
	}
	cmd, _ := sc.Unverified()
	before := len(w.ran)
	status, body := w.agent.run(context.Background(), &queuedCommand{ID: cmd.CmdID, Epoch: 1, Envelope: sc})
	for _, r := range w.ran[before:] {
		if r == method+" "+path {
			return status, string(body)
		}
	}
	return status, "not run: " + string(body)
}

func refusedWith(t *testing.T, what string, status int, body, want string) {
	t.Helper()
	if status != http.StatusForbidden || !strings.Contains(body, want) || !strings.HasPrefix(body, "not run") {
		t.Fatalf("%s = %d %s, want 403 %q and nothing on the box", what, status, body, want)
	}
}

func TestRelay_ControlDelegationRunsUntilItExpires(t *testing.T) {
	w := newMemberWorld(t, trust.ScopeControl)
	block := "/api/devices/" + memberTarget + "/block"

	if st, body := w.send("POST", block); st != http.StatusOK || strings.HasPrefix(body, "not run") {
		t.Fatalf("a control member's block = %d %s", st, body)
	}
	// Thirty days later, with no renewal, it is refused.
	w.clock.set(arT0.Add(trust.DelegationTTL))
	st, body := w.send("POST", block)
	refusedWith(t, "a block on an expired delegation", st, body, "delegation has expired")

	// The owner's app comes back and renews it: the member acts again.
	w.change(func(b *trust.Bundle) { b.RenewDelegations(w.clock.now()) })
	if st, body := w.send("POST", block); st != http.StatusOK || strings.HasPrefix(body, "not run") {
		t.Fatalf("after renewal = %d %s", st, body)
	}
}

func TestRelay_RevokedDelegationIsRefused(t *testing.T) {
	w := newMemberWorld(t, trust.ScopeControl)
	w.change(func(b *trust.Bundle) { b.Revoke(w.member.KeyID()) })
	st, body := w.send("POST", "/api/devices/"+memberTarget+"/block")
	refusedWith(t, "a revoked member's block", st, body, "revoked")
	st, body = w.send("GET", "/api/devices")
	refusedWith(t, "a revoked member's read", st, body, "revoked")
}

func TestRelay_OwnerOnlyBlocksEveryMember(t *testing.T) {
	w := newMemberWorld(t, trust.ScopeControl)
	w.change(func(b *trust.Bundle) { b.OwnerOnly = true })
	st, body := w.send("POST", "/api/devices/"+memberTarget+"/unblock")
	refusedWith(t, "a member's unblock in an owner-only household", st, body, "owner-only")

	// Turning it off gives the delegation back as it was.
	w.change(func(b *trust.Bundle) { b.OwnerOnly = false })
	if st, body := w.send("POST", "/api/devices/"+memberTarget+"/unblock"); st != http.StatusOK || strings.HasPrefix(body, "not run") {
		t.Fatalf("owner-only off = %d %s", st, body)
	}
}

func TestRelay_ViewDelegationNeverWrites(t *testing.T) {
	w := newMemberWorld(t, trust.ScopeView)
	if st, body := w.send("GET", "/api/devices"); st != http.StatusOK || strings.HasPrefix(body, "not run") {
		t.Fatalf("a view member's read = %d %s", st, body)
	}
	st, body := w.send("POST", "/api/devices/"+memberTarget+"/block")
	refusedWith(t, "a view member's block", st, body, "not delegated")
}
