package trust

import (
	"crypto/rand"
	"errors"
	"testing"
)

func sealed(t *testing.T, owner, relay *Identity) []byte {
	t.Helper()
	blob, err := SealCredentials(rand.Reader, owner, relay.Public().X25519, Credentials{
		TenantID: tenant, AgentID: "agent-1", Username: "admin", Password: "box-pass", Version: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func TestSealCredentials_OnlyTheRelayOpensIt(t *testing.T) {
	owner, relay, other := mustID(t), mustID(t), mustID(t)
	blob := sealed(t, owner, relay)

	got, err := OpenCredentials(relay, owner.Public().X25519, blob, tenant, "agent-1")
	if err != nil {
		t.Fatalf("OpenCredentials: %v", err)
	}
	if got.Password.Reveal() != "box-pass" || got.Username != "admin" {
		t.Errorf("credentials = %+v", got)
	}

	// Another relay of the same household holds a different key.
	if _, err := OpenCredentials(other, owner.Public().X25519, blob, tenant, "agent-1"); !errors.Is(err, ErrBadSignature) {
		t.Errorf("another relay opened the blob: %v", err)
	}
	// The ciphertext is opaque to whoever stores it.
	if string(blob) == "" || contains(blob, "box-pass") {
		t.Error("the sealed blob leaks the password in clear")
	}
}

func TestOpenCredentials_ChecksTheSender(t *testing.T) {
	owner, relay, hub := mustID(t), mustID(t), mustID(t)

	// The hub seals its own credentials and claims they came from the owner.
	forged, err := SealCredentials(rand.Reader, hub, relay.Public().X25519, Credentials{
		TenantID: tenant, AgentID: "agent-1", Username: "admin", Password: "attacker-pass",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenCredentials(relay, owner.Public().X25519, forged, tenant, "agent-1"); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("a blob sealed by the hub was accepted: %v", err)
	}
}

func TestOpenCredentials_ChecksTheContext(t *testing.T) {
	owner, relay := mustID(t), mustID(t)
	blob := sealed(t, owner, relay)

	if _, err := OpenCredentials(relay, owner.Public().X25519, blob, "another-tenant", "agent-1"); !errors.Is(err, ErrTenant) {
		t.Error("a blob was accepted for another household")
	}
	if _, err := OpenCredentials(relay, owner.Public().X25519, blob, tenant, "agent-2"); !errors.Is(err, ErrMalformed) {
		t.Error("a blob was accepted for another agent of the same household")
	}
}

func TestOpenCredentials_RefusesTruncatedBlobs(t *testing.T) {
	owner, relay := mustID(t), mustID(t)
	blob := sealed(t, owner, relay)
	for _, bad := range [][]byte{nil, blob[:10], blob[:nonceSize]} {
		if _, err := OpenCredentials(relay, owner.Public().X25519, bad, tenant, "agent-1"); err == nil {
			t.Errorf("a %d-byte blob was accepted", len(bad))
		}
	}
	// A flipped byte inside the box breaks its authentication tag.
	tampered := append([]byte(nil), blob...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := OpenCredentials(relay, owner.Public().X25519, tampered, tenant, "agent-1"); !errors.Is(err, ErrBadSignature) {
		t.Error("a tampered blob was accepted")
	}
}

func contains(haystack []byte, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack []byte, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return i
		}
	}
	return -1
}
