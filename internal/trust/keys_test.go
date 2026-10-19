package trust

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestIdentity_RoundTripsThroughTheVault(t *testing.T) {
	id := mustID(t)
	back, err := ParseIdentity(id.Marshal())
	if err != nil {
		t.Fatalf("ParseIdentity: %v", err)
	}
	if back.KeyID() != id.KeyID() {
		t.Errorf("KeyID = %s, want %s", back.KeyID(), id.KeyID())
	}
	if got, want := back.Public(), id.Public(); string(got.X25519) != string(want.X25519) {
		t.Error("the sealing key did not survive the round trip")
	}
	// A sealed blob must open with the restored identity: the private half
	// is what the vault actually has to preserve.
	owner := mustID(t)
	blob, err := SealCredentials(rand.Reader, owner, id.Public().X25519, Credentials{
		TenantID: tenant, AgentID: "a1", Username: "admin", Password: "box-pass",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenCredentials(back, owner.Public().X25519, blob, tenant, "a1"); err != nil {
		t.Errorf("restored identity cannot open its own blob: %v", err)
	}
}

func TestParseIdentity_RejectsJunk(t *testing.T) {
	for _, blob := range [][]byte{nil, {}, {1, 2, 3}, make([]byte, identityBlobSize)} {
		if _, err := ParseIdentity(blob); err == nil && len(blob) != identityBlobSize {
			t.Errorf("ParseIdentity(%d bytes) accepted junk", len(blob))
		}
	}
	bad := make([]byte, identityBlobSize)
	bad[0] = 9 // unknown version
	if _, err := ParseIdentity(bad); !errors.Is(err, ErrMalformed) {
		t.Errorf("err = %v, want ErrMalformed", err)
	}
}

func TestKeyID_IsStableAndDistinct(t *testing.T) {
	a, b := mustID(t), mustID(t)
	if a.KeyID() != a.Public().KeyID() {
		t.Error("KeyID differs between the identity and its public keys")
	}
	if a.KeyID() == b.KeyID() {
		t.Error("two identities share a key id")
	}
	if len(a.KeyID()) != 32 {
		t.Errorf("KeyID length = %d, want 32 hex chars", len(a.KeyID()))
	}
	if KeyID(nil) != "" {
		t.Error("a missing key must have no id, so a lookup on it never matches")
	}
}

// TestSecret_NeverPrints guards the core rule: credentials never reach a log.
func TestSecret_NeverPrints(t *testing.T) {
	s := Secret("hunter2-the-box-password")
	for _, got := range []string{
		s.String(),
		fmt.Sprint(s),
		fmt.Sprintf("%v", s),
		fmt.Sprintf("%s", s),
		fmt.Sprintf("%#v", s),
		fmt.Sprintf("%+v", struct{ P Secret }{s}),
	} {
		if strings.Contains(got, "hunter2") {
			t.Errorf("a formatted secret leaked: %q", got)
		}
	}
	if s.Reveal() != "hunter2-the-box-password" {
		t.Error("Reveal must return the real value")
	}
}
