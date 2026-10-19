package trust

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

func TestPairingCode_MatchesOnBothSidesAndCatchesASwap(t *testing.T) {
	owner, relay, attacker := mustID(t), mustID(t), mustID(t)

	// What each screen computes: the owner knows its own keys and the ones
	// the hub relayed; the relay knows its own and the pinned owner key.
	ownerSide := PairingCode(owner.Public(), relay.Public())
	relaySide := PairingCode(owner.Public(), relay.Public())
	if ownerSide != relaySide {
		t.Fatalf("the two screens disagree: %s vs %s", ownerSide, relaySide)
	}
	if len(ownerSide) != 6 {
		t.Fatalf("code = %q, want 6 digits", ownerSide)
	}

	// A hub that put its own key in the middle makes the codes differ,
	// which is the whole point of showing them.
	if PairingCode(owner.Public(), attacker.Public()) == ownerSide {
		t.Error("swapping the relay key left the code unchanged")
	}
	if PairingCode(attacker.Public(), relay.Public()) == ownerSide {
		t.Error("swapping the owner key left the code unchanged")
	}

	// Both halves of each identity are covered, not just the signing one.
	mixed := owner.Public()
	mixed.X25519 = attacker.Public().X25519
	if PairingCode(mixed, relay.Public()) == ownerSide {
		t.Error("swapping only the sealing key left the code unchanged")
	}
}

func TestInvite_RoundTrip(t *testing.T) {
	owner := mustID(t)
	in := Invite{Code: "WSX2-9QJ4-KD7M-PL3B", OwnerFingerprint: owner.KeyID()}
	out, err := ParseInvite(in.String())
	if err != nil {
		t.Fatalf("ParseInvite: %v", err)
	}
	if out != in {
		t.Errorf("round trip = %+v, want %+v", out, in)
	}
}

func TestParseInvite_Refusals(t *testing.T) {
	for _, s := range []string{
		"",
		"nonsense",
		"kgp1.onlytwo",
		"kgp2.AAAA.fingerprint", // unknown version
		"kgp1.!!!!.fingerprint", // not base32
		"kgp1..fingerprint",     // empty code
		"kgp1.MFSG22LO.",        // empty fingerprint
	} {
		if _, err := ParseInvite(s); !errors.Is(err, ErrMalformed) {
			t.Errorf("ParseInvite(%q) err = %v, want ErrMalformed", s, err)
		}
	}
}

func TestRecoveryProof_OnlyTheBoxPasswordWorks(t *testing.T) {
	const password = "the-box-password"
	newOwner := mustID(t)
	newKey := ed25519.PublicKey(newOwner.Public().Ed25519)

	nonce := make([]byte, RecoveryNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	proof, err := RecoveryProof(tenant, password, nonce, newKey)
	if err != nil {
		t.Fatalf("RecoveryProof: %v", err)
	}
	if !VerifyRecovery(proof, tenant, password, nonce, newKey) {
		t.Fatal("the honest proof did not verify")
	}

	other := mustID(t)
	for _, tc := range []struct {
		name             string
		tenant, password string
		nonce            []byte
		key              ed25519.PublicKey
	}{
		{"a wrong password", tenant, "guess", nonce, newKey},
		{"another household", "other-tenant", password, nonce, newKey},
		{"a stale challenge", tenant, password, make([]byte, RecoveryNonceSize), newKey},
		{"a different new key", tenant, password, nonce, ed25519.PublicKey(other.Public().Ed25519)},
	} {
		if VerifyRecovery(proof, tc.tenant, tc.password, tc.nonce, tc.key) {
			t.Errorf("%s was accepted", tc.name)
		}
	}
}

func TestRecoveryProof_RefusesBadInputs(t *testing.T) {
	if _, err := RecoveryProof(tenant, "p", make([]byte, 8), ed25519.PublicKey(mustID(t).Public().Ed25519)); !errors.Is(err, ErrMalformed) {
		t.Error("a short nonce must be refused: it is what makes the challenge fresh")
	}
	if _, err := RecoveryProof(tenant, "p", make([]byte, RecoveryNonceSize), nil); !errors.Is(err, ErrMalformed) {
		t.Error("a missing new key must be refused")
	}
}
