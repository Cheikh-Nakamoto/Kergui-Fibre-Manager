package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Recovery of a lost owner phone at home (issue #48): the new phone proves
// the box password to the relay on the Wi-Fi, the relay signs the handover,
// the new phone signs the next bundle, and the relay follows it — the old
// owner key is refused from then on. The hub carries the new bundle like any
// other; it can neither start nor finish the exchange.

const boxPassword = "Sene-Gal-2026"

type recoveryWorld struct {
	*memberWorld
	rec      *Recovery
	lan      *httptest.Server
	newOwner *trust.Identity
}

func newRecoveryWorld(t *testing.T) *recoveryWorld {
	t.Helper()
	w := newMemberWorld(t, trust.ScopeControl)
	newOwner, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rec := NewRecovery(RecoveryConfig{
		State:    w.state,
		Password: func(context.Context) (string, error) { return boxPassword, nil },
		Clock:    w.clock.now,
	})
	lan := httptest.NewServer(rec) // 127.0.0.1: this machine, on the LAN side
	t.Cleanup(lan.Close)
	return &recoveryWorld{memberWorld: w, rec: rec, lan: lan, newOwner: newOwner}
}

func post(t *testing.T, url string, body any) (int, []byte) {
	t.Helper()
	raw, _ := json.Marshal(body)
	res, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(res.Body)
	return res.StatusCode, buf.Bytes()
}

func (w *recoveryWorld) challenge() RecoveryChallenge {
	w.t.Helper()
	st, body := post(w.t, w.lan.URL+"/recovery/v1/challenge", nil)
	if st != http.StatusOK {
		w.t.Fatalf("challenge = %d %s", st, body)
	}
	var c RecoveryChallenge
	if err := json.Unmarshal(body, &c); err != nil {
		w.t.Fatal(err)
	}
	return c
}

func (w *recoveryWorld) claimFor(c RecoveryChallenge, password string, keys trust.PublicKeys) RecoveryClaim {
	w.t.Helper()
	proof, err := trust.RecoveryProof(c.TenantID, password, c.Nonce, keys.SigningKey())
	if err != nil {
		w.t.Fatal(err)
	}
	return RecoveryClaim{Nonce: c.Nonce, Ed25519: keys.Ed25519, X25519: keys.X25519, Proof: proof}
}

// upload is the new phone signing the successor bundle and the hub storing it.
func (w *recoveryWorld) upload(g RecoveryGrant) trust.Bundle {
	w.t.Helper()
	held, err := trust.Signed[trust.Bundle]{Payload: g.Bundle.Payload, Sig: g.Bundle.Sig}.Unverified()
	if err != nil {
		w.t.Fatal(err)
	}
	next, err := trust.Succeed(held, trust.SignedHandover{Payload: g.Handover.Payload, Sig: g.Handover.Sig}, 0, w.clock.now())
	if err != nil {
		w.t.Fatal(err)
	}
	signed, err := trust.Sign(w.newOwner, trust.DomainBundle, next)
	if err != nil {
		w.t.Fatal(err)
	}
	w.hub.mu.Lock()
	w.hub.bundle, w.hub.bundleVer = signed, next.Version
	w.hub.mu.Unlock()
	return next
}

func (w *recoveryWorld) sendAs(id *trust.Identity, method, path string) (int, string) {
	w.t.Helper()
	sc, err := trust.NewCommand(id, arTenant, method, path, nil, w.clock.now(), 30*time.Second)
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

func TestRecovery_OnTheLANTheBoxPasswordHandsTheHouseholdOver(t *testing.T) {
	w := newRecoveryWorld(t)
	block := "/api/devices/" + memberTarget + "/block"

	c := w.challenge()
	if c.TenantID != arTenant || len(c.Nonce) != trust.RecoveryNonceSize || len(c.Bundle.Payload) == 0 {
		t.Fatalf("challenge = %+v", c)
	}
	st, body := post(t, w.lan.URL+"/recovery/v1/claim", w.claimFor(c, boxPassword, w.newOwner.Public()))
	if st != http.StatusOK {
		t.Fatalf("claim = %d %s", st, body)
	}
	var g RecoveryGrant
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatal(err)
	}
	next := w.upload(g)

	w.agent.tick(context.Background())
	if got := w.agent.bundleVersion(); got != next.Version {
		t.Fatalf("the relay holds v%d, want the new owner's v%d", got, next.Version)
	}
	if !w.state.Data.OwnerKey.Equal(w.newOwner.Public().SigningKey()) {
		t.Fatal("the relay did not keep the new owner key in its state")
	}

	if st, body := w.sendAs(w.newOwner, "POST", block); st != http.StatusOK || strings.HasPrefix(body, "not run") {
		t.Fatalf("the new owner's block = %d %s", st, body)
	}
	st, body2 := w.sendAs(w.owner, "POST", block)
	refusedWith(t, "the old owner's block", st, body2, "revoked")
	// The member keeps the delegation the old owner gave.
	if st, body := w.sendAs(w.member, "POST", "/api/devices/"+memberTarget+"/unblock"); st != http.StatusOK || strings.HasPrefix(body, "not run") {
		t.Fatalf("the member after recovery = %d %s", st, body)
	}

	// A restart keeps the new owner: the stored bundle opens with the stored key.
	again, err := New(Config{HubURL: w.agent.cfg.HubURL, Handler: w.agent.cfg.Handler, State: w.state, Clock: w.clock.now})
	if err != nil {
		t.Fatal(err)
	}
	if again.verifier.OwnerKeyID() != w.newOwner.KeyID() || again.bundleVersion() != next.Version {
		t.Fatalf("after a restart: owner %s, v%d", again.verifier.OwnerKeyID(), again.bundleVersion())
	}
}

// What a hub can do with the exchange: nothing.
func TestRecovery_NeverThroughTheHub(t *testing.T) {
	t.Run("a caller off the home network is refused before anything", func(t *testing.T) {
		w := newRecoveryWorld(t)
		for _, path := range []string{"/recovery/v1/challenge", "/recovery/v1/claim"} {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
			req.RemoteAddr = "203.0.113.7:41000" // the hub, or anything on the Internet
			rr := httptest.NewRecorder()
			w.rec.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "not_lan") {
				t.Fatalf("%s from the Internet = %d %s", path, rr.Code, rr.Body)
			}
		}
		if len(w.rec.nonces) != 0 {
			t.Fatal("a challenge was handed out off the home network")
		}
	})

	t.Run("the hub cannot ask the relay through a command", func(t *testing.T) {
		w := newRecoveryWorld(t)
		// Even signed by the owner's key, /recovery is on no allow-list, and
		// the commands run against the box's API, not the recovery listener.
		for _, path := range []string{"/recovery/v1/challenge", "/recovery/v1/claim"} {
			st, body := w.sendAs(w.owner, "POST", path)
			if st == http.StatusOK || !strings.HasPrefix(body, "not run") || !strings.Contains(body, "allow-list") {
				t.Fatalf("a command to %s = %d %s", path, st, body)
			}
		}
	})

	t.Run("a replayed claim is refused: the nonce is spent", func(t *testing.T) {
		w := newRecoveryWorld(t)
		claim := w.claimFor(w.challenge(), boxPassword, w.newOwner.Public())
		if st, body := post(t, w.lan.URL+"/recovery/v1/claim", claim); st != http.StatusOK {
			t.Fatalf("first claim = %d %s", st, body)
		}
		if st, body := post(t, w.lan.URL+"/recovery/v1/claim", claim); st != http.StatusGone {
			t.Fatalf("replayed claim = %d %s, want 410", st, body)
		}
	})

	t.Run("a relayed proof cannot be redirected to another key", func(t *testing.T) {
		w := newRecoveryWorld(t)
		hubKey, _ := trust.Generate(rand.Reader)
		claim := w.claimFor(w.challenge(), boxPassword, w.newOwner.Public())
		claim.Ed25519, claim.X25519 = hubKey.Public().Ed25519, hubKey.Public().X25519
		if st, body := post(t, w.lan.URL+"/recovery/v1/claim", claim); st != http.StatusForbidden || !strings.Contains(string(body), "proof") {
			t.Fatalf("a proof for another key = %d %s", st, body)
		}
	})

	t.Run("an expired challenge, a wrong password, then the rate limit", func(t *testing.T) {
		w := newRecoveryWorld(t)
		c := w.challenge()
		w.clock.set(w.clock.now().Add(RecoveryNonceTTL))
		if st, _ := post(t, w.lan.URL+"/recovery/v1/claim", w.claimFor(c, boxPassword, w.newOwner.Public())); st != http.StatusGone {
			t.Fatalf("expired challenge = %d, want 410", st)
		}
		for i := 0; i < RecoveryMaxClaims; i++ {
			if st, _ := post(t, w.lan.URL+"/recovery/v1/claim", w.claimFor(w.challenge(), "guess", w.newOwner.Public())); st != http.StatusForbidden {
				t.Fatalf("wrong password #%d = %d, want 403", i, st)
			}
		}
		if st, _ := post(t, w.lan.URL+"/recovery/v1/claim", w.claimFor(w.challenge(), boxPassword, w.newOwner.Public())); st != http.StatusTooManyRequests {
			t.Fatalf("after %d attempts = %d, want 429 even with the right password", RecoveryMaxClaims, st)
		}
		w.clock.set(w.clock.now().Add(RecoveryClaimWindow))
		if st, body := post(t, w.lan.URL+"/recovery/v1/claim", w.claimFor(w.challenge(), boxPassword, w.newOwner.Public())); st != http.StatusOK {
			t.Fatalf("after the window = %d %s", st, body)
		}
	})

	t.Run("a bundle the hub made up with its own handover is refused", func(t *testing.T) {
		w := newRecoveryWorld(t)
		hubKey, _ := trust.Generate(rand.Reader)
		held := w.bundle
		// The hub signs a handover as if it were the relay, and a bundle for itself.
		ho, err := trust.NewHandover(hubKey, held, hubKey.Public(), trust.ViaRelay, nil, w.clock.now())
		if err != nil {
			t.Fatal(err)
		}
		next, _ := trust.Succeed(held, ho, 0, w.clock.now())
		signed, _ := trust.Sign(hubKey, trust.DomainBundle, next)
		w.hub.mu.Lock()
		w.hub.bundle, w.hub.bundleVer = signed, next.Version
		w.hub.mu.Unlock()
		w.agent.tick(context.Background())
		if w.agent.verifier.OwnerKeyID() != w.owner.KeyID() || w.agent.bundleVersion() != held.Version {
			t.Fatal("the relay followed a hub's handover")
		}
		if err := w.agent.pullBundle(context.Background()); !errors.Is(err, trust.ErrSuccession) {
			t.Fatalf("pullBundle = %v, want ErrSuccession", err)
		}
	})

	t.Run("a relay not yet signed in, or without the password, endorses nobody", func(t *testing.T) {
		w := newRecoveryWorld(t)
		empty := NewRecovery(RecoveryConfig{State: &MemoryState{}, Password: func(context.Context) (string, error) { return boxPassword, nil }})
		if _, err := empty.Challenge(context.Background()); !errors.Is(err, ErrRecoveryNotReady) {
			t.Fatalf("unenrolled relay = %v", err)
		}
		nopass := NewRecovery(RecoveryConfig{State: w.state, Password: func(context.Context) (string, error) { return "", nil }, Clock: w.clock.now})
		c, err := nopass.Challenge(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := nopass.Claim(context.Background(), w.claimFor(c, "", w.newOwner.Public())); !errors.Is(err, ErrRecoveryNoPassword) {
			t.Fatalf("no password = %v", err)
		}
	})
}

func TestOnLAN(t *testing.T) {
	for addr, want := range map[string]bool{
		"192.168.1.10:5000": true, "10.0.0.2:1": true, "172.16.4.4:1": true, "127.0.0.1:1": true,
		"[fe80::1]:1": true, "[fd00::5]:1": true,
		"203.0.113.7:1": false, "8.8.8.8:53": false, "[2001:db8::1]:1": false, "garbage": false,
	} {
		if got := onLAN(addr); got != want {
			t.Errorf("onLAN(%s) = %t, want %t", addr, got, want)
		}
	}
}

// The other path (issue #48): a co-owner the owner signed in advance hands
// the household to a new key without the box password. The relay follows
// it, and the old owner key is refused from then on.
func TestRecovery_ACoOwnerHandsTheHouseholdOver(t *testing.T) {
	w := newRecoveryWorld(t)
	block := "/api/devices/" + memberTarget + "/block"
	coOwner, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	w.change(func(b *trust.Bundle) {
		if err := b.AddCoOwner(coOwner.Public(), "Fatou"); err != nil {
			t.Fatal(err)
		}
	})

	held := w.bundle
	ho, err := trust.NewHandover(coOwner, held, w.newOwner.Public(), trust.ViaCoOwner, nil, w.clock.now())
	if err != nil {
		t.Fatal(err)
	}
	next, err := trust.Succeed(held, ho, 0, w.clock.now())
	if err != nil {
		t.Fatal(err)
	}
	signed, err := trust.Sign(w.newOwner, trust.DomainBundle, next)
	if err != nil {
		t.Fatal(err)
	}
	w.hub.mu.Lock()
	w.hub.bundle, w.hub.bundleVer = signed, next.Version
	w.hub.mu.Unlock()
	w.agent.tick(context.Background())

	if w.agent.verifier.OwnerKeyID() != w.newOwner.KeyID() || w.agent.bundleVersion() != next.Version {
		t.Fatalf("the relay holds owner %s v%d, want %s v%d", w.agent.verifier.OwnerKeyID(), w.agent.bundleVersion(), w.newOwner.KeyID(), next.Version)
	}
	if st, body := w.sendAs(w.newOwner, "POST", block); st != http.StatusOK || strings.HasPrefix(body, "not run") {
		t.Fatalf("the new owner's block = %d %s", st, body)
	}
	st, body := w.sendAs(w.owner, "POST", block)
	refusedWith(t, "the old owner's block", st, body, "revoked")

	// A co-owner the owner removed cannot hand anything over.
	w2 := newRecoveryWorld(t)
	w2.change(func(b *trust.Bundle) { _ = b.AddCoOwner(coOwner.Public(), "Fatou") })
	w2.change(func(b *trust.Bundle) { b.RemoveCoOwner(coOwner.KeyID()) })
	ho2, _ := trust.NewHandover(coOwner, w2.bundle, w2.newOwner.Public(), trust.ViaCoOwner, nil, w2.clock.now())
	next2, _ := trust.Succeed(w2.bundle, ho2, 0, w2.clock.now())
	signed2, _ := trust.Sign(w2.newOwner, trust.DomainBundle, next2)
	w2.hub.mu.Lock()
	w2.hub.bundle, w2.hub.bundleVer = signed2, next2.Version
	w2.hub.mu.Unlock()
	if err := w2.agent.pullBundle(context.Background()); !errors.Is(err, trust.ErrSuccession) {
		t.Fatalf("a removed co-owner's handover = %v, want ErrSuccession", err)
	}
	if w2.agent.verifier.OwnerKeyID() != w2.owner.KeyID() {
		t.Fatal("the relay followed a removed co-owner")
	}
}
