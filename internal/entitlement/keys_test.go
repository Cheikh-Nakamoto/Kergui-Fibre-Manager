package entitlement

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func published(pub ed25519.PublicKey, st KeyStatus) PublishedKey {
	return PublishedKey{KeyID: KeyID(pub), PublicKey: pub, Status: st}
}

func mustSignKeyList(t *testing.T, priv ed25519.PrivateKey, l KeyList) string {
	t.Helper()
	doc, err := SignKeyList(priv, l)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// TestRotationOverlap: during a rotation the verifier trusts the active key
// and the retiring one, so tokens signed by either verify.
func TestRotationOverlap(t *testing.T) {
	oldPub, oldPriv := testKey(1)
	newPub, newPriv := testKey(2)
	ks := keySet(t, oldPub, newPub)
	for name, priv := range map[string]ed25519.PrivateKey{"retiring": oldPriv, "active": newPriv} {
		if _, _, err := Verify(ks, mustSign(t, priv, testClaims()), t0); err != nil {
			t.Errorf("token signed by the %s key: %v", name, err)
		}
	}
}

// TestRevokedKeyIsRejected: a revoked key id is refused, with its own error,
// even when the key itself was trusted before; an unknown one stays
// ErrUnknownKey.
func TestRevokedKeyIsRejected(t *testing.T) {
	oldPub, oldPriv := testKey(1)
	newPub, newPriv := testKey(2)
	_, strangerPriv := testKey(3)
	ks, err := keySet(t, oldPub, newPub).Revoke(KeyID(oldPub))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Verify(ks, mustSign(t, oldPriv, testClaims()), t0); !errors.Is(err, ErrRevokedKey) {
		t.Errorf("token of a revoked key: %v, want ErrRevokedKey", err)
	}
	if _, _, err := Verify(ks, mustSign(t, strangerPriv, testClaims()), t0); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("token of an unknown key: %v, want ErrUnknownKey", err)
	}
	if _, _, err := Verify(ks, mustSign(t, newPriv, testClaims()), t0); err != nil {
		t.Errorf("token of the remaining key: %v", err)
	}
	if got := ks.IDs(); !slices.Equal(got, []string{KeyID(newPub)}) {
		t.Errorf("IDs = %v, want only the remaining key", got)
	}
	if got := ks.Revoked(); !slices.Equal(got, []string{KeyID(oldPub)}) {
		t.Errorf("Revoked = %v", got)
	}
	if _, err := ks.Revoke("nope"); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("Revoke(bad id) = %v", err)
	}
}

// TestParseKeySetRevocations: the core's key list can carry revocations
// ("revoked:<key id>"), whatever order they come in.
func TestParseKeySetRevocations(t *testing.T) {
	pub1, priv1 := testKey(1)
	pub2, _ := testKey(2)
	enc := base64.StdEncoding.EncodeToString
	for _, s := range []string{
		enc(pub1) + "," + enc(pub2) + ",revoked:" + KeyID(pub1),
		"revoked:" + KeyID(pub1) + " " + enc(pub1) + " " + enc(pub2),
	} {
		ks, err := ParseKeySet(s)
		if err != nil {
			t.Fatalf("ParseKeySet(%q): %v", s, err)
		}
		if _, _, err := Verify(ks, mustSign(t, priv1, testClaims()), t0); !errors.Is(err, ErrRevokedKey) {
			t.Errorf("ParseKeySet(%q): revoked key verifies (%v)", s, err)
		}
		if ks.Len() != 1 {
			t.Errorf("ParseKeySet(%q) trusts %v", s, ks.IDs())
		}
	}
	if _, err := ParseKeySet("revoked:XYZ"); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("a bad revoked id = %v", err)
	}
}

// TestKeyListRoundTrip: the hub's signed key list verifies with a key the
// verifier already trusts and reads back as signed.
func TestKeyListRoundTrip(t *testing.T) {
	oldPub, oldPriv := testKey(1)
	newPub, _ := testKey(2)
	_, revokedPriv := testKey(4)
	revokedID := KeyID(revokedPriv.Public().(ed25519.PublicKey))
	l := KeyList{
		Keys:     []PublishedKey{published(oldPub, KeyActive), published(newPub, KeyNext)},
		Revoked:  []string{revokedID},
		IssuedAt: t0,
	}
	doc := mustSignKeyList(t, oldPriv, l)
	got, err := VerifyKeyList(keySet(t, oldPub), doc)
	if err != nil {
		t.Fatal(err)
	}
	if got.KeyID != KeyID(oldPub) || !got.IssuedAt.Equal(t0) || len(got.Keys) != 2 ||
		got.Keys[1].Status != KeyNext || !got.Keys[1].PublicKey.Equal(newPub) || !slices.Equal(got.Revoked, []string{revokedID}) {
		t.Errorf("VerifyKeyList = %+v", got)
	}
}

// TestLearnFromKeyList: an app that embeds only the old key learns the new
// one from a list the old key signed, and the revocations with it. What it
// learns is trusted to sign the next list.
func TestLearnFromKeyList(t *testing.T) {
	oldPub, oldPriv := testKey(1)
	newPub, newPriv := testKey(2)
	nextPub, _ := testKey(3)
	embedded := keySet(t, oldPub)

	// 1. the new key is published as next, signed by the old (active) key.
	l1, err := VerifyKeyList(embedded, mustSignKeyList(t, oldPriv, KeyList{
		Keys: []PublishedKey{published(oldPub, KeyActive), published(newPub, KeyNext)}, IssuedAt: t0,
	}))
	if err != nil {
		t.Fatal(err)
	}
	ks := embedded.Learn(l1)
	if _, _, err := Verify(ks, mustSign(t, newPriv, testClaims()), t0); err != nil {
		t.Fatalf("token of the learned key: %v", err)
	}

	// 2. the new key is active and signs the list that revokes the old one.
	l2, err := VerifyKeyList(ks, mustSignKeyList(t, newPriv, KeyList{
		Keys: []PublishedKey{published(newPub, KeyActive), published(nextPub, KeyNext)}, Revoked: []string{KeyID(oldPub)}, IssuedAt: t0,
	}))
	if err != nil {
		t.Fatalf("a list signed by a learned key: %v", err)
	}
	ks = ks.Learn(l2)
	if _, _, err := Verify(ks, mustSign(t, oldPriv, testClaims()), t0); !errors.Is(err, ErrRevokedKey) {
		t.Errorf("the old key after its revocation: %v", err)
	}
	// 3. a replay of the first list does not bring the revoked key back.
	if ks = ks.Learn(l1); slices.Contains(ks.IDs(), KeyID(oldPub)) {
		t.Errorf("a replayed list un-revoked a key: %v", ks.IDs())
	}
	if _, err := VerifyKeyList(ks, mustSignKeyList(t, oldPriv, KeyList{Keys: []PublishedKey{published(oldPub, KeyActive)}, IssuedAt: t0})); !errors.Is(err, ErrRevokedKey) {
		t.Errorf("a list signed by a revoked key: %v", err)
	}
}

// TestKeyListRejects: a list is learned only when a trusted key signed it,
// and only when the hub could have signed it.
func TestKeyListRejects(t *testing.T) {
	oldPub, oldPriv := testKey(1)
	newPub, newPriv := testKey(2)
	trusted := keySet(t, oldPub)
	good := KeyList{Keys: []PublishedKey{published(oldPub, KeyActive), published(newPub, KeyNext)}, IssuedAt: t0}

	// Signed by a key the verifier does not trust: a stranger cannot
	// introduce keys.
	if _, err := VerifyKeyList(trusted, mustSignKeyList(t, newPriv, KeyList{Keys: []PublishedKey{published(newPub, KeyActive)}, IssuedAt: t0})); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("list of an untrusted key: %v", err)
	}
	doc := mustSignKeyList(t, oldPriv, good)
	p, s, _ := strings.Cut(doc, ".")
	payload, _ := b64.DecodeString(p)
	tampered := b64.EncodeToString([]byte(strings.Replace(string(payload), `"next"`, `"active"`, 1))) + "." + s
	if _, err := VerifyKeyList(trusted, tampered); !errors.Is(err, ErrBadSignature) {
		t.Errorf("tampered list: %v", err)
	}
	for _, bad := range []string{"", "x", doc + ".x", p + "."} {
		if _, err := VerifyKeyList(trusted, bad); !errors.Is(err, ErrMalformedKeyList) {
			t.Errorf("VerifyKeyList(%q) = %v", bad, err)
		}
	}

	wrongID := published(newPub, KeyNext)
	wrongID.KeyID = KeyID(oldPub)[:15] + "0"
	for name, l := range map[string]KeyList{
		"signer not active":     {Keys: []PublishedKey{published(oldPub, KeyRetiring), published(newPub, KeyNext)}, IssuedAt: t0},
		"two active keys":       {Keys: []PublishedKey{published(oldPub, KeyActive), published(newPub, KeyActive)}, IssuedAt: t0},
		"unknown status":        {Keys: []PublishedKey{published(oldPub, KeyActive), published(newPub, "revoked")}, IssuedAt: t0},
		"key id not its key's":  {Keys: []PublishedKey{published(oldPub, KeyActive), wrongID}, IssuedAt: t0},
		"listed twice":          {Keys: []PublishedKey{published(oldPub, KeyActive), published(oldPub, KeyNext)}, IssuedAt: t0},
		"listed and revoked":    {Keys: []PublishedKey{published(oldPub, KeyActive), published(newPub, KeyNext)}, Revoked: []string{KeyID(newPub)}, IssuedAt: t0},
		"signer revoked":        {Keys: []PublishedKey{published(oldPub, KeyActive)}, Revoked: []string{KeyID(oldPub)}, IssuedAt: t0},
		"bad revoked id":        {Keys: []PublishedKey{published(oldPub, KeyActive)}, Revoked: []string{"XYZ"}, IssuedAt: t0},
		"no issuance time":      {Keys: []PublishedKey{published(oldPub, KeyActive)}},
		"short key in the list": {Keys: []PublishedKey{published(oldPub, KeyActive), {KeyID: "0123456789abcdef", PublicKey: newPub[:16], Status: KeyNext}}, IssuedAt: t0},
	} {
		if _, err := SignKeyList(oldPriv, l); !errors.Is(err, ErrMalformedKeyList) {
			t.Errorf("SignKeyList(%s) = %v, want ErrMalformedKeyList", name, err)
		}
	}
}

// TestAppKeyListFixture is the key list pinned in
// mobile/src/__tests__/keyring.test.ts: the app fixture's key (seed bytes
// 1..32) active, the key with seed bytes 33..64 next, one key id revoked. If
// this fails, the wire format changed and the app's fixture must be
// regenerated with it.
func TestAppKeyListFixture(t *testing.T) {
	seed := func(from byte) ed25519.PrivateKey {
		s := make([]byte, ed25519.SeedSize)
		for i := range s {
			s[i] = from + byte(i)
		}
		return ed25519.NewKeyFromSeed(s)
	}
	active, next := seed(1), seed(33)
	activePub, nextPub := active.Public().(ed25519.PublicKey), next.Public().(ed25519.PublicKey)
	doc := mustSignKeyList(t, active, KeyList{
		Keys:     []PublishedKey{published(activePub, KeyActive), published(nextPub, KeyNext)},
		Revoked:  []string{"0123456789abcdef"},
		IssuedAt: time.Unix(1795000000, 0),
	})
	const want = "eyJrZXlzIjpbeyJrZXlfaWQiOiI2NWI2MDY3M2Q2ZWQ4ODRiIiwicHVibGljX2tleSI6ImViVldMby9tVlBsQWVMRVM2S21McDVBZmhUcm1sYjdYNE9PUkM2MEVsbVE9Iiwic3RhdHVzIjoiYWN0aXZlIn0seyJrZXlfaWQiOiJjOTQ1Y2JmMmE1NjAyMDAyIiwicHVibGljX2tleSI6IjUvRmlvUXZzVlpyK29aWGszT2hMYVZhTlhTeXdsajYwUnNCb1hpc1g4dkE9Iiwic3RhdHVzIjoibmV4dCJ9XSwicmV2b2tlZCI6WyIwMTIzNDU2Nzg5YWJjZGVmIl0sImlzc3VlZF9hdCI6MTc5NTAwMDAwMCwia2V5X2lkIjoiNjViNjA2NzNkNmVkODg0YiJ9.DnTfNZdBZNDJox2Mb43qC9IG952Q8eBRc4lMKCyilNEZO5NTnIK9Zb-w8lsQiTTCAKZmneqku-Cmj248CHkUDA"
	if doc != want {
		t.Errorf("app fixture key list changed (next key %s, id %s):\n%s",
			base64.StdEncoding.EncodeToString(nextPub), KeyID(nextPub), doc)
	}
}
