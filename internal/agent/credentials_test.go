package agent

import (
	"context"
	"crypto/rand"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// The relay's side of the sealed box password (#44): it opens only what the
// owner sealed for it, and takes a new password when credential_version
// moves.

type credWorld struct {
	t      *testing.T
	owner  *trust.Identity
	relay  *trust.Identity
	hub    *fakeHub
	state  *MemoryState
	agent  *Agent
	mu     sync.Mutex
	stored []domain.Credentials
}

func newCredWorld(t *testing.T) *credWorld {
	t.Helper()
	ar := newARWorld(t) // the owner, the relay and the hub serving the bundle
	w := &credWorld{t: t, owner: ar.owner, hub: ar.hub}
	st := ar.agent.cfg.State.(*MemoryState)
	relay, err := trust.ParseIdentity(st.Data.Identity)
	if err != nil {
		t.Fatal(err)
	}
	w.relay = relay
	w.state = &MemoryState{Data: st.Data}
	ag, err := New(Config{
		HubURL:  st.Data.HubURL,
		Handler: devicesAPI(),
		State:   w.state,
		LANIP:   arLANIP,
		Credentials: func(_ context.Context, c domain.Credentials) error {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.stored = append(w.stored, c)
			return nil
		},
		Timings: Timings{Heartbeat: time.Second, LongPoll: time.Millisecond, SnapshotEvery: time.Minute, HubTimeout: 5 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.agent = ag
	return w
}

// put is the hub storing a blob sealed by `by` at version.
func (w *credWorld) put(by *trust.Identity, password string, sealedVersion, hubVersion uint64) {
	w.t.Helper()
	blob, err := trust.SealCredentials(rand.Reader, by, w.relay.Public().X25519, trust.Credentials{
		TenantID: arTenant, AgentID: arAgent, Username: "admin", Password: trust.Secret(password), Version: sealedVersion,
	})
	if err != nil {
		w.t.Fatal(err)
	}
	w.hub.mu.Lock()
	w.hub.creds, w.hub.credsVer = blob, hubVersion
	w.hub.mu.Unlock()
}

func (w *credWorld) tick() { w.agent.tick(context.Background()) }

func (w *credWorld) passwords() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, c := range w.stored {
		out = append(out, c.Password)
	}
	return out
}

func TestAgentCredentials_OpensTheOwnersAndFollowsTheVersion(t *testing.T) {
	w := newCredWorld(t)
	w.put(w.owner, "first-pass", 1, 1)
	w.tick()
	if got := w.passwords(); len(got) != 1 || got[0] != "first-pass" {
		t.Fatalf("the relay did not take the owner's password: %v", got)
	}
	if w.state.Data.CredentialVersion != 1 {
		t.Fatalf("credential version = %d, want 1", w.state.Data.CredentialVersion)
	}

	// Same version: nothing is fetched again.
	w.tick()
	if got := w.passwords(); len(got) != 1 {
		t.Fatalf("an unchanged version was pulled again: %v", got)
	}

	// The owner changed the password: a higher credential_version.
	w.put(w.owner, "second-pass", 2, 2)
	w.tick()
	if got := w.passwords(); len(got) != 2 || got[1] != "second-pass" {
		t.Fatalf("the new password did not reach the relay: %v", got)
	}
	if w.state.Data.CredentialVersion != 2 {
		t.Fatalf("credential version = %d, want 2", w.state.Data.CredentialVersion)
	}
}

func TestAgentCredentials_RefusesABlobNotSealedByTheOwner(t *testing.T) {
	w := newCredWorld(t)
	w.put(w.owner, "owner-pass", 1, 1)
	w.tick()

	// The hub (or whoever holds its database) seals its own password for
	// the relay: valid nacl/box, the wrong sender.
	rogue, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	w.put(rogue, "hub-knows-best", 2, 2)
	gets := w.hub.credsGets
	w.tick()
	w.tick()
	w.tick()
	// Refused once, not fetched again at every heartbeat until the
	// version moves.
	if n := w.hub.credsGets - gets; n != 1 {
		t.Fatalf("the refused blob was fetched %d times", n)
	}
	if got := w.passwords(); len(got) != 1 || got[0] != "owner-pass" {
		t.Fatalf("a blob the owner did not seal was installed: %v", got)
	}
	if w.state.Data.CredentialVersion != 1 {
		t.Fatalf("credential version moved to %d on a refused blob", w.state.Data.CredentialVersion)
	}

	// The owner seals again: a new version, taken.
	w.put(w.owner, "owner-new", 3, 3)
	w.tick()
	if got := w.passwords(); got[len(got)-1] != "owner-new" {
		t.Fatalf("the owner's next blob was not taken after a refusal: %v", got)
	}
}

// A hub that replays an older blob of the owner's under a higher version
// cannot roll the password back: the version that counts is the sealed one.
func TestAgentCredentials_RefusesAnOlderBlobReplayedUnderANewVersion(t *testing.T) {
	w := newCredWorld(t)
	w.put(w.owner, "old-pass", 1, 1)
	old := append([]byte(nil), w.hub.creds...)
	w.tick()
	w.put(w.owner, "new-pass", 2, 2)
	w.tick()

	w.hub.mu.Lock()
	w.hub.creds, w.hub.credsVer = old, 3
	w.hub.mu.Unlock()
	w.tick()
	if got := w.passwords(); got[len(got)-1] != "new-pass" {
		t.Fatalf("a replayed blob rolled the password back: %v", got)
	}
}
