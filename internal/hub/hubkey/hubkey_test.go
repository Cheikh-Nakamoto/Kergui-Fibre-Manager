package hubkey

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	tenantB = "22222222-2222-2222-2222-222222222222"
)

func seed(b byte) []byte { return bytes.Repeat([]byte{b}, MinSeedLen) }

// TestFor_IsDeterministic: a hub that restarts, or a second instance, must
// derive the same key, or the bundle the owner signed stops matching.
func TestFor_IsDeterministic(t *testing.T) {
	d1, err := New(seed('s'))
	if err != nil {
		t.Fatal(err)
	}
	d2, err := New(seed('s'))
	if err != nil {
		t.Fatal(err)
	}
	a1, err := d1.For(tenantA)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := d2.For(tenantA)
	if err != nil {
		t.Fatal(err)
	}
	if a1.KeyID() != a2.KeyID() {
		t.Errorf("two derivations of one household gave %s and %s", a1.KeyID(), a2.KeyID())
	}
	if !bytes.Equal(a1.Public().X25519, a2.Public().X25519) {
		t.Error("the sealing halves differ")
	}
}

// TestFor_IsPerHousehold: one household's key must not act on another's, so
// the derivation has to separate them.
func TestFor_IsPerHousehold(t *testing.T) {
	d, err := New(seed('s'))
	if err != nil {
		t.Fatal(err)
	}
	a, err := d.For(tenantA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.For(tenantB)
	if err != nil {
		t.Fatal(err)
	}
	if a.KeyID() == b.KeyID() {
		t.Error("two households share one hub key")
	}
}

// TestFor_DependsOnTheSeed: another deployment's seed gives another key, so
// a hub cannot act on a household it was never given.
func TestFor_DependsOnTheSeed(t *testing.T) {
	d1, _ := New(seed('s'))
	d2, _ := New(seed('t'))
	a1, err := d1.For(tenantA)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := d2.For(tenantA)
	if err != nil {
		t.Fatal(err)
	}
	if a1.KeyID() == a2.KeyID() {
		t.Error("the key does not depend on the seed")
	}
}

func TestNew_Refusals(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, ErrNoSeed) {
		t.Errorf("New(nil) = %v, want ErrNoSeed: a hub may run without WhatsApp blocking", err)
	}
	if _, err := New([]byte("too short")); err == nil {
		t.Error("a short seed was accepted")
	}
}

// TestKeyID_MatchesTheIdentity: the id the app reads is the id the relay
// will check, so one must come from the other.
func TestKeyID_MatchesTheIdentity(t *testing.T) {
	d, _ := New(seed('s'))
	id, err := d.For(tenantA)
	if err != nil {
		t.Fatal(err)
	}
	keyID, err := d.KeyID(tenantA)
	if err != nil {
		t.Fatal(err)
	}
	if keyID != id.KeyID() {
		t.Errorf("KeyID() = %s, identity = %s", keyID, id.KeyID())
	}
	if !strings.HasPrefix(keyID, "k1_") && keyID == "" {
		t.Errorf("KeyID() = %q", keyID)
	}
}

func TestFor_NeedsAHousehold(t *testing.T) {
	d, _ := New(seed('s'))
	if _, err := d.For(""); err == nil {
		t.Error("a key was derived for no household")
	}
}
