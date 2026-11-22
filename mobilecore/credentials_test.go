package mobilecore

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// The box password sealed for the relays (#44): the core seals it from its
// vault for every relay the owner signed into the bundle, and only ever
// hands out ciphertext.

const credTenant = "11111111-2222-3333-4444-555555555555"

type sealedAnswer struct {
	Version     uint64 `json:"version"`
	BoxPassword bool   `json:"boxPassword"`
	Relays      []struct {
		AgentID string `json:"agentId"`
		Blob    []byte `json:"blob"`
		Version uint64 `json:"version"`
	} `json:"relays"`
}

// demoCore is a core logged in to the demo box, its password in the vault.
func demoCore(t *testing.T, log Logger) *Core {
	t.Helper()
	c := newCore(t, log)
	if err := c.StartDemo(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.StopDemo)
	if status, body := call(t, c, "POST", "/api/login", `{"username":"`+DemoUsername+`","password":"`+DemoPassword+`"}`); status != http.StatusOK {
		t.Fatalf("login = %d %s", status, body)
	}
	return c
}

// bundleWith signs relays into the household's bundle, as "same code" does.
func bundleWith(t *testing.T, c *Core, tenant string, relays ...*trust.Identity) string {
	t.Helper()
	current := ""
	for i, r := range relays {
		pub := r.Public()
		ch, _ := json.Marshal(map[string]any{"relay": map[string]any{
			"agentId": agentName(i), "kind": "pc", "ed25519": pub.Ed25519, "x25519": pub.X25519,
		}})
		out, err := c.SignTrustBundle(tenant, current, string(ch))
		if err != nil {
			t.Fatal(err)
		}
		current = out
	}
	return current
}

func agentName(i int) string { return "agent-" + string(rune('a'+i)) }

func newRelay(t *testing.T) *trust.Identity {
	t.Helper()
	id, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func seal(t *testing.T, c *Core, tenant, bundle, agentID string) (sealedAnswer, string) {
	t.Helper()
	raw, err := c.SealCredentials(tenant, bundle, agentID)
	if err != nil {
		t.Fatalf("SealCredentials: %v", err)
	}
	var a sealedAnswer
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	return a, raw
}

func ownerX(t *testing.T, c *Core) []byte {
	t.Helper()
	return ownerKeys(t, c).X25519
}

func TestSealCredentials_EveryRelayOfTheBundleOpensItsOwn(t *testing.T) {
	log := &captureLogger{}
	c := demoCore(t, log)
	// A password that cannot be mistaken for anything else in the output.
	const secret = "Tres-Secret-42"
	if err := c.vault.Store(context.Background(), usecase.RouterID(c.DemoURL()), domain.Credentials{Username: DemoUsername, Password: secret}); err != nil {
		t.Fatal(err)
	}
	r1, r2 := newRelay(t), newRelay(t)
	bundle := bundleWith(t, c, credTenant, r1, r2)

	got, raw := seal(t, c, credTenant, bundle, "")
	if !got.BoxPassword || got.Version != 1 || len(got.Relays) != 2 {
		t.Fatalf("sealed = %+v", got)
	}
	if strings.Contains(raw, secret) || strings.Contains(raw, `"password"`) {
		t.Fatalf("the answer carries the password in the clear: %s", raw)
	}
	for i, r := range []*trust.Identity{r1, r2} {
		s := got.Relays[i]
		if s.AgentID != agentName(i) || s.Version != 1 {
			t.Fatalf("relay %d = %s v%d", i, s.AgentID, s.Version)
		}
		creds, err := trust.OpenCredentials(r, ownerX(t, c), s.Blob, credTenant, s.AgentID)
		if err != nil {
			t.Fatalf("relay %d cannot open its blob: %v", i, err)
		}
		if creds.Username != DemoUsername || creds.Password.Reveal() != secret || creds.Version != 1 {
			t.Fatalf("relay %d opened %s v%d", i, creds.Username, creds.Version)
		}
	}
	// One relay's blob is not the other's.
	if _, err := trust.OpenCredentials(r2, ownerX(t, c), got.Relays[0].Blob, credTenant, agentName(0)); err == nil {
		t.Fatal("a relay opened the blob sealed for another")
	}
	journal, _ := c.Journal(0, 0)
	if strings.Contains(journal, secret) {
		t.Fatal("the journal carries the password")
	}
	for _, l := range log.lines {
		if strings.Contains(l, secret) {
			t.Fatalf("the journal carries the password: %q", l)
		}
	}
}

func TestSealCredentials_VersionMovesOnlyWhenThePasswordChanges(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(dir, "test-vault-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.StartDemo(); err != nil {
		t.Fatal(err)
	}
	if status, body := call(t, c, "POST", "/api/login", `{"username":"admin","password":"admin"}`); status != http.StatusOK {
		t.Fatalf("login = %d %s", status, body)
	}
	r := newRelay(t)
	bundle := bundleWith(t, c, credTenant, r)

	if a, _ := seal(t, c, credTenant, bundle, ""); a.Version != 1 {
		t.Fatalf("first seal v%d", a.Version)
	}
	if a, _ := seal(t, c, credTenant, bundle, ""); a.Version != 1 {
		t.Fatalf("an unchanged password moved the version to %d", a.Version)
	}

	// The owner changed the box password and logged in with the new one.
	routerID := usecase.RouterID(c.DemoURL())
	if err := c.vault.Store(context.Background(), routerID, domain.Credentials{Username: "admin", Password: "n0uveau"}); err != nil {
		t.Fatal(err)
	}
	a, _ := seal(t, c, credTenant, bundle, "")
	if a.Version != 2 {
		t.Fatalf("a new password sealed at v%d, want 2", a.Version)
	}
	creds, err := trust.OpenCredentials(r, ownerX(t, c), a.Relays[0].Blob, credTenant, agentName(0))
	if err != nil || creds.Password.Reveal() != "n0uveau" || creds.Version != 2 {
		t.Fatalf("the relay opened v%d (%v)", creds.Version, err)
	}

	// The version survives a restart: the next change goes to 3, not 1.
	demoURL := c.DemoURL()
	c.StopDemo()
	_ = c.Close()
	c2, err := Open(dir, "test-vault-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c2.Close() }()
	if err := c2.Configure(demoURL, "", ""); err != nil {
		t.Fatal(err)
	}
	if a, _ := seal(t, c2, credTenant, bundle, ""); a.Version != 2 {
		t.Fatalf("after a restart the same password sealed at v%d", a.Version)
	}
	if err := c2.vault.Store(context.Background(), routerID, domain.Credentials{Username: "admin", Password: "encore"}); err != nil {
		t.Fatal(err)
	}
	if a, _ := seal(t, c2, credTenant, bundle, ""); a.Version != 3 {
		t.Fatalf("after a restart a new password sealed at v%d, want 3", a.Version)
	}
}

func TestSealCredentials_OneRelay(t *testing.T) {
	c := demoCore(t, nil)
	bundle := bundleWith(t, c, credTenant, newRelay(t), newRelay(t))
	a, _ := seal(t, c, credTenant, bundle, agentName(1))
	if len(a.Relays) != 1 || a.Relays[0].AgentID != agentName(1) {
		t.Fatalf("sealed for %+v, want only %s", a.Relays, agentName(1))
	}
	if _, err := c.SealCredentials(credTenant, bundle, "agent-unknown"); err == nil {
		t.Fatal("sealed for a relay the owner never signed in")
	}
}

// The relays and their keys come from the bundle the owner signed, never
// from what the hub says: a bundle the hub made up is refused.
func TestSealCredentials_OnlyForTheOwnersBundle(t *testing.T) {
	c := demoCore(t, nil)
	r := newRelay(t)

	rogue := newRelay(t)
	forged, err := trust.Sign(rogue, trust.DomainBundle, trust.Bundle{
		TenantID: credTenant, Version: 9, Owner: rogue.Public(),
		Relays: []trust.Relay{{AgentID: "agent-a", Kind: "pc", Keys: r.Public()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(signedJSON{Payload: forged.Payload, Sig: forged.Sig, Version: 9})
	if _, err := c.SealCredentials(credTenant, string(raw), ""); !errors.Is(err, trust.ErrBadSignature) {
		t.Fatalf("a bundle the owner did not sign: %v", err)
	}

	bundle := bundleWith(t, c, credTenant, r)
	if _, err := c.SealCredentials("another-household", bundle, ""); !errors.Is(err, trust.ErrTenant) {
		t.Fatalf("another household's bundle: %v", err)
	}
	if _, err := c.SealCredentials(credTenant, "", ""); err == nil {
		t.Fatal("sealed without a bundle")
	}
}

// Not logged in to the box: nothing to seal, and the app is told so.
func TestSealCredentials_NoBoxPasswordYet(t *testing.T) {
	c := newCore(t, nil)
	if err := c.StartDemo(); err != nil {
		t.Fatal(err)
	}
	defer c.StopDemo()
	bundle := bundleWith(t, c, credTenant, newRelay(t))
	a, _ := seal(t, c, credTenant, bundle, "")
	if a.BoxPassword || len(a.Relays) != 0 {
		t.Fatalf("sealed without a password: %+v", a)
	}
}
