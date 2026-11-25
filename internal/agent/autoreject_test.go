package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// The relay's side of auto-reject (#36): it gets the rule and the pass from
// the owner through the hub, and enforces it only with a valid Max token for
// the box it is in front of.

const (
	arTenant = "11111111-1111-1111-1111-111111111111"
	arAgent  = "agent-1"
	arBox    = "ZTEG00000001"
	arOwnMAC = "02:00:00:00:00:99" // the relay itself, at arLANIP
	arLANIP  = "192.168.1.50"
)

var arT0 = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

type arClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *arClock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *arClock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

// fakeAutoReject records what the relay asks of the rule.
type fakeAutoReject struct {
	mu      sync.Mutex
	adopted []usecase.AutoRejectPolicy
	rounds  int
	refuse  []domain.Device
}

func (f *fakeAutoReject) Adopt(_ context.Context, p usecase.AutoRejectPolicy) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adopted = append(f.adopted, p)
	return nil
}

func (f *fakeAutoReject) Enforce(context.Context) (usecase.AutoRejectRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rounds++
	run := usecase.AutoRejectRun{Enabled: true, Refused: f.refuse}
	f.refuse = nil
	return run, nil
}

func (f *fakeAutoReject) counts() (adopted, rounds int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.adopted), f.rounds
}

func (f *fakeAutoReject) last() usecase.AutoRejectPolicy {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.adopted[len(f.adopted)-1]
}

// devicesAPI is the relay's in-memory API, reduced to the device list.
func devicesAPI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/devices" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"MAC": arOwnMAC, "IP": arLANIP},
			{"MAC": "ac:bb:cc:00:00:22", "IP": "192.168.1.22"},
		})
	})
}

// fakeHub is the /relay/v1 surface: it grants the lease, serves the owner's
// bundle and policy, and remembers what the heartbeats said.
type fakeHub struct {
	mu         sync.Mutex
	bundle     trust.Signed[trust.Bundle]
	policy     trust.Signed[trust.Policy]
	policyVer  uint64
	autoReject []bool
	// creds is the sealed box password (credentials_test.go).
	creds     []byte
	credsVer  uint64
	credsGets int
	// bundleVer is the bundle version the heartbeat announces (1 when 0).
	bundleVer uint64
	// paused is what each heartbeat said of the pause (yield_test.go).
	paused []bool
}

func (h *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch r.Method + " " + r.URL.Path {
	case "POST /relay/v1/heartbeat":
		var req heartbeatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		h.autoReject = append(h.autoReject, req.AutoReject)
		h.paused = append(h.paused, req.Paused)
		_ = json.NewEncoder(w).Encode(heartbeatResponse{Lease: leaseActive, Epoch: 1, TTLSeconds: 60,
			BundleVersion: max(h.bundleVer, 1), PolicyVersion: h.policyVer, CredentialVersion: h.credsVer})
	case "GET /relay/v1/trust":
		_ = json.NewEncoder(w).Encode(h.bundle)
	case "GET /relay/v1/policy":
		_ = json.NewEncoder(w).Encode(h.policy)
	case "GET /relay/v1/credentials":
		h.credsGets++
		_ = json.NewEncoder(w).Encode(map[string]any{"blob": h.creds, "version": h.credsVer})
	case "GET /relay/v1/commands":
		w.WriteHeader(http.StatusNoContent)
	case "POST /relay/v1/snapshots":
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (h *fakeHub) beats() []bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]bool(nil), h.autoReject...)
}

type arWorld struct {
	t        *testing.T
	owner    *trust.Identity
	licPriv  ed25519.PrivateKey
	licKeys  entitlement.KeySet
	clock    *arClock
	rule     *fakeAutoReject
	hub      *fakeHub
	agent    *Agent
	serials  int
	policyV  uint64
	protects []string
}

func newARWorld(t *testing.T) *arWorld {
	t.Helper()
	owner, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	relay, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := entitlement.NewKeySet(pub)
	if err != nil {
		t.Fatal(err)
	}
	w := &arWorld{t: t, owner: owner, licPriv: priv, licKeys: keys,
		clock: &arClock{t: arT0}, rule: &fakeAutoReject{}, hub: &fakeHub{}}
	w.hub.bundle = w.sign(trust.DomainBundle, trust.Bundle{
		TenantID: arTenant, Version: 1, IssuedAt: arT0, Owner: owner.Public(),
		Relays:        []trust.Relay{{AgentID: arAgent, Kind: "pc", Keys: relay.Public(), MACs: []string{"02:00:00:00:00:98"}}},
		ProtectedMACs: []string{"02:00:00:00:00:97"},
	})
	srv := httptest.NewServer(w.hub)
	t.Cleanup(srv.Close)

	ag, err := New(Config{
		HubURL:  srv.URL,
		Handler: devicesAPI(),
		State: &MemoryState{Data: StateData{
			HubURL: srv.URL, AgentID: arAgent, TenantID: arTenant, Token: "kga_test",
			Identity: relay.Marshal(), OwnerKey: owner.Public().SigningKey(),
		}},
		LANIP:      arLANIP,
		Clock:      w.clock.now,
		AutoReject: w.rule,
		Licensing:  keys,
		BoxSerial: func(context.Context) (string, error) {
			w.serials++
			return arBox, nil
		},
		Timings: Timings{Heartbeat: time.Second, LongPoll: time.Millisecond, SnapshotEvery: time.Minute, HubTimeout: 5 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.agent = ag
	return w
}

func (w *arWorld) sign(domainName string, v any) trust.Signed[trust.Bundle] {
	w.t.Helper()
	s, err := trust.Sign(w.owner, domainName, v)
	if err != nil {
		w.t.Fatal(err)
	}
	return trust.Signed[trust.Bundle]{Payload: s.Payload, Sig: s.Sig}
}

func (w *arWorld) token(tier entitlement.Tier, box string, expires time.Time) string {
	w.t.Helper()
	tok, err := entitlement.Sign(w.licPriv, entitlement.Claims{Account: "acc-1", BoxID: box, Tier: tier,
		IssuedAt: expires.Add(-10 * 24 * time.Hour), ExpiresAt: expires})
	if err != nil {
		w.t.Fatal(err)
	}
	return tok
}

// publish is the owner's app sending a new policy through the hub.
func (w *arWorld) publish(enabled bool, token string) {
	w.t.Helper()
	w.policyV++
	s := w.sign(trust.DomainPolicy, trust.Policy{
		TenantID: arTenant, Version: w.policyV, IssuedAt: w.clock.now(), Entitlement: token,
		AutoReject: trust.AutoRejectPolicy{Enabled: enabled, Since: arT0.Add(-time.Hour), Accepted: []string{"ac:bb:cc:00:00:22"}},
	})
	w.hub.mu.Lock()
	w.hub.policy = trust.Signed[trust.Policy]{Payload: s.Payload, Sig: s.Sig}
	w.hub.policyVer = w.policyV
	w.hub.mu.Unlock()
}

func (w *arWorld) tick() { w.agent.tick(context.Background()) }

// TestAgentAutoReject_GetsThePolicyAndEnforcesWithMax: the policy and the
// token arrive through the relay path, the relay adopts the phone's accepted
// list plus everything it must never cut, enforces, and tells the hub it does.
func TestAgentAutoReject_GetsThePolicyAndEnforcesWithMax(t *testing.T) {
	w := newARWorld(t)
	w.publish(true, w.token(entitlement.TierMax, arBox, arT0.Add(10*24*time.Hour)))

	w.tick() // heartbeat: pulls the bundle and the policy, then enforces
	adopted, rounds := w.rule.counts()
	if adopted != 1 || rounds != 1 {
		t.Fatalf("after the first round: adopted %d, rounds %d, want 1 and 1", adopted, rounds)
	}
	p := w.rule.last()
	if !p.Enabled || !p.Since.Equal(arT0.Add(-time.Hour)) {
		t.Fatalf("adopted %+v", p)
	}
	want := map[string]bool{"ac:bb:cc:00:00:22": true, arOwnMAC: true, "02:00:00:00:00:98": true, "02:00:00:00:00:97": true}
	for _, m := range p.Accepted {
		delete(want, m.String())
	}
	if len(want) != 0 {
		t.Fatalf("accepted %v; missing %v (the phone's list, the relay itself, the relays, the protected)", p.Accepted, want)
	}
	if w.serials != 1 {
		t.Fatalf("box serial read %d times, want once", w.serials)
	}

	// Next heartbeat: the hub learns this relay enforces the rule.
	w.clock.set(arT0.Add(time.Minute))
	w.tick()
	if beats := w.hub.beats(); len(beats) != 2 || beats[0] || !beats[1] {
		t.Fatalf("heartbeat autoReject flags = %v, want [false true]", beats)
	}
	if _, rounds := w.rule.counts(); rounds != 2 {
		t.Fatalf("rounds = %d, want 2", rounds)
	}
	if adopted, _ := w.rule.counts(); adopted != 1 {
		t.Fatalf("the same policy was adopted %d times", adopted)
	}

	// The rounds follow the snapshot cadence, not the long poll.
	w.clock.set(arT0.Add(time.Minute + time.Second))
	w.tick()
	if _, rounds := w.rule.counts(); rounds != 2 {
		t.Fatalf("a round ran a second after the last one (%d rounds)", rounds)
	}

	// The owner turns it off: the relay adopts that and stops.
	w.publish(false, w.token(entitlement.TierMax, arBox, arT0.Add(10*24*time.Hour)))
	w.clock.set(arT0.Add(3 * time.Minute))
	w.tick()
	if p := w.rule.last(); p.Enabled {
		t.Fatalf("the relay did not adopt the rule turned off: %+v", p)
	}
	if _, rounds := w.rule.counts(); rounds != 2 {
		t.Fatalf("a round ran with the rule off (%d rounds)", rounds)
	}
	w.tick()
	if beats := w.hub.beats(); beats[len(beats)-1] {
		t.Fatal("the relay still tells the hub it enforces a rule that is off")
	}
}

// TestAgentAutoReject_RefusesWithoutAValidMaxToken: whatever the owner's
// policy says, the relay touches nothing without a Max pass for this box,
// and does not tell the hub it enforces — so the phone keeps doing it.
func TestAgentAutoReject_RefusesWithoutAValidMaxToken(t *testing.T) {
	cases := []struct {
		name  string
		token func(w *arWorld) string
	}{
		{"no token", func(*arWorld) string { return "" }},
		{"Basic", func(w *arWorld) string { return w.token(entitlement.TierBasic, arBox, arT0.Add(10*24*time.Hour)) }},
		{"another box", func(w *arWorld) string {
			return w.token(entitlement.TierMax, "ZTEG99999999", arT0.Add(10*24*time.Hour))
		}},
		{"expired past the grace", func(w *arWorld) string {
			return w.token(entitlement.TierMax, arBox, arT0.Add(-entitlement.OfflineGrace-time.Hour))
		}},
		{"signed by an unknown key", func(w *arWorld) string {
			_, other, _ := ed25519.GenerateKey(rand.Reader)
			tok, _ := entitlement.Sign(other, entitlement.Claims{Account: "acc-1", BoxID: arBox, Tier: entitlement.TierMax,
				IssuedAt: arT0, ExpiresAt: arT0.Add(10 * 24 * time.Hour)})
			return tok
		}},
		{"tampered", func(w *arWorld) string {
			tok := w.token(entitlement.TierBasic, arBox, arT0.Add(10*24*time.Hour))
			p, s, _ := strings.Cut(tok, ".")
			claims := strings.Replace(decodeB64(t, p), `"basic"`, `"max"`, 1)
			return encodeB64(claims) + "." + s
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newARWorld(t)
			w.publish(true, c.token(w))
			w.tick()
			w.clock.set(arT0.Add(2 * time.Minute))
			w.tick()
			if adopted, rounds := w.rule.counts(); adopted != 0 || rounds != 0 {
				t.Fatalf("adopted %d, rounds %d: the relay acted without a valid Max token", adopted, rounds)
			}
			for _, b := range w.hub.beats() {
				if b {
					t.Fatal("the relay told the hub it enforces the rule")
				}
			}
		})
	}
}

// A pass that lapses while the relay runs stops the rule at the next round.
func TestAgentAutoReject_StopsWhenThePassLapses(t *testing.T) {
	w := newARWorld(t)
	expires := arT0.Add(time.Hour)
	w.publish(true, w.token(entitlement.TierMax, arBox, expires))
	w.tick()
	if _, rounds := w.rule.counts(); rounds != 1 {
		t.Fatalf("rounds = %d with a valid pass", rounds)
	}
	w.clock.set(expires.Add(entitlement.OfflineGrace))
	w.tick()
	if _, rounds := w.rule.counts(); rounds != 1 {
		t.Fatalf("a round ran after the pass and its grace ran out (%d)", rounds)
	}
	if beats := w.hub.beats(); beats[len(beats)-1] {
		t.Fatal("the relay still claims the rule after its pass lapsed")
	}
}

// A policy the hub made up — not signed by the owner — is refused.
func TestAgentAutoReject_RefusesAPolicyTheHubForged(t *testing.T) {
	w := newARWorld(t)
	rogue, _ := trust.Generate(rand.Reader)
	s, _ := trust.Sign(rogue, trust.DomainPolicy, trust.Policy{
		TenantID: arTenant, Version: 1, IssuedAt: arT0,
		Entitlement: w.token(entitlement.TierMax, arBox, arT0.Add(24*time.Hour)),
		AutoReject:  trust.AutoRejectPolicy{Enabled: true, Since: arT0},
	})
	w.hub.policy, w.hub.policyVer = s, 1
	w.tick()
	w.tick()
	if adopted, rounds := w.rule.counts(); adopted != 0 || rounds != 0 {
		t.Fatalf("a forged policy was acted on: adopted %d, rounds %d", adopted, rounds)
	}
}

func decodeB64(t *testing.T, s string) string {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func encodeB64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
