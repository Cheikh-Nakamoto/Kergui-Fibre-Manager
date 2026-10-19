package trust

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestNewCommand_RefusesAnOversizedLifetime(t *testing.T) {
	owner := mustID(t)
	for _, ttl := range []time.Duration{0, -time.Second, MaxCommandTTL + time.Second, time.Hour} {
		if _, err := NewCommand(owner, tenant, "GET", "/api/devices", nil, now, ttl); !errors.Is(err, ErrTooLongLived) {
			t.Errorf("ttl %s: err = %v, want ErrTooLongLived", ttl, err)
		}
	}
	if _, err := NewCommand(owner, tenant, "GET", "/api/devices", nil, now, MaxCommandTTL); err != nil {
		t.Errorf("the maximum lifetime itself must be allowed: %v", err)
	}
}

func TestNewCommand_NormalisesAndCommits(t *testing.T) {
	owner := mustID(t)
	body := []byte(`{"enabled":true}`)
	sc, err := NewCommand(owner, tenant, "post", "/api/filter/toggle", body, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	c, err := sc.Unverified()
	if err != nil {
		t.Fatal(err)
	}
	if c.Method != "POST" {
		t.Errorf("Method = %q, want it upper-cased", c.Method)
	}
	if c.BodySHA256 != BodyDigest(body) || c.BodySHA256 == BodyDigest(nil) {
		t.Error("the body digest does not commit to the body")
	}
	if c.CmdID == "" {
		t.Error("a command needs an id, or replay cannot be detected")
	}
}

// TestResult_CarriesANonJSONBody: the box answers an HTML error page often
// enough that a signed result must be able to carry one.
func TestResult_CarriesANonJSONBody(t *testing.T) {
	relay := mustID(t)
	sr, err := NewResult(relay, tenant, "cmd-1", 1, 502, []byte("<html>router unreachable</html>"), now)
	if err != nil {
		t.Fatal(err)
	}
	res, err := sr.Open(DomainResult, relay.Public().signPub())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var s string
	if err := json.Unmarshal(res.Body, &s); err != nil {
		t.Fatalf("a non-JSON body was not wrapped as a JSON string: %v", err)
	}
	if s != "<html>router unreachable</html>" {
		t.Errorf("body = %q", s)
	}
}

func TestSign_DomainsDoNotCross(t *testing.T) {
	owner := mustID(t)
	sc, err := NewCommand(owner, tenant, "GET", "/api/devices", nil, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// The same bytes, verified as another kind of message, must not pass:
	// otherwise a signed command could be replayed as a signed result.
	as := Signed[Result]{Payload: sc.Payload, Sig: sc.Sig}
	if _, err := as.Open(DomainResult, owner.Public().signPub()); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("a command verified as a result: err = %v", err)
	}
}

func TestAcceptBundle_RefusesAnUnknownScope(t *testing.T) {
	owner := mustID(t)
	member := mustID(t)
	v := NewVerifier(tenant, owner.Public().signPub(), now)
	sb, err := Sign(owner, DomainBundle, Bundle{
		TenantID:    tenant,
		Version:     1,
		Owner:       owner.Public(),
		Delegations: []Delegation{{Keys: member.Public(), Scope: "admin"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.AcceptBundle(sb); !errors.Is(err, ErrScope) {
		t.Fatalf("err = %v, want ErrScope: an unknown scope must not be installed", err)
	}
}

func TestBundle_LookupsAndProtection(t *testing.T) {
	relay := mustID(t)
	b := Bundle{
		Relays:        []Relay{{AgentID: "agent-1", Kind: "pc", Keys: relay.Public()}},
		ProtectedMACs: []string{"AA-BB-CC-00-00-01"},
	}
	if r, ok := b.Relay("agent-1"); !ok || r.KeyID() != relay.KeyID() {
		t.Error("Relay did not find the paired agent")
	}
	if _, ok := b.Relay("agent-2"); ok {
		t.Error("Relay found an agent that was never paired")
	}
	if r, ok := b.RelayByKeyID(relay.KeyID()); !ok || r.AgentID != "agent-1" {
		t.Error("RelayByKeyID did not find the paired agent")
	}
	if _, ok := b.RelayByKeyID(""); ok {
		t.Error("an empty key id matched a relay")
	}
	// Separators and case differ between the box, the app and the owner's
	// own typing, so protection must not depend on them.
	for _, mac := range []string{"aa:bb:cc:00:00:01", "AA:BB:CC:00:00:01", "aabbcc000001"} {
		if !b.IsProtected(mac) {
			t.Errorf("IsProtected(%q) = false", mac)
		}
	}
	if b.IsProtected("ac:bb:cc:00:00:22") || b.IsProtected("") {
		t.Error("IsProtected matched a device that is not on the list")
	}
}

func TestVerifier_OwnerKeyID(t *testing.T) {
	h := newHousehold(t)
	if h.v.OwnerKeyID() != h.owner.KeyID() {
		t.Errorf("OwnerKeyID = %s, want %s", h.v.OwnerKeyID(), h.owner.KeyID())
	}
}
