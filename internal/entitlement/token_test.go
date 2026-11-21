package entitlement

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

func testKey(b byte) (ed25519.PublicKey, ed25519.PrivateKey) {
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{b}, ed25519.SeedSize))
	return priv.Public().(ed25519.PublicKey), priv
}

func keySet(t *testing.T, pubs ...ed25519.PublicKey) KeySet {
	t.Helper()
	ks, err := NewKeySet(pubs...)
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

func testClaims() Claims {
	return Claims{
		Account:   "acc-1",
		BoxID:     "ZTEG00000001",
		Tier:      TierBasic,
		IssuedAt:  t0,
		ExpiresAt: t0.Add(10 * day),
	}
}

func mustSign(t *testing.T, priv ed25519.PrivateKey, c Claims) string {
	t.Helper()
	tok, err := Sign(priv, c)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv := testKey(1)
	want := testClaims()
	tok := mustSign(t, priv, want)
	if strings.Count(tok, ".") != 1 || strings.ContainsAny(tok, "=+/") {
		t.Errorf("token is not compact base64url: %q", tok)
	}
	got, grace, err := Verify(keySet(t, pub), tok, t0.Add(day))
	if err != nil || grace {
		t.Fatalf("Verify = %v, grace %v", err, grace)
	}
	want.KeyID = KeyID(pub)
	if !got.ExpiresAt.Equal(want.ExpiresAt) || !got.IssuedAt.Equal(want.IssuedAt) ||
		got.Account != want.Account || got.BoxID != want.BoxID || got.Tier != want.Tier || got.KeyID != want.KeyID {
		t.Errorf("Verify = %+v, want %+v", got, want)
	}
}

// TestSignSetsTheSigningKeyID: the key id comes from the key, never from the
// caller, so a token cannot name a key that did not sign it.
func TestSignSetsTheSigningKeyID(t *testing.T) {
	pub, priv := testKey(1)
	c := testClaims()
	c.KeyID = "0000000000000000"
	got, _, err := Verify(keySet(t, pub), mustSign(t, priv, c), t0)
	if err != nil || got.KeyID != KeyID(pub) {
		t.Fatalf("KeyID = %q, %v; want %q", got.KeyID, err, KeyID(pub))
	}
}

// TestSignIsDeterministic: Ed25519 signs deterministically, so the same
// claims give the same token — a replayed issuance is harmless.
func TestSignIsDeterministic(t *testing.T) {
	_, priv := testKey(1)
	if mustSign(t, priv, testClaims()) != mustSign(t, priv, testClaims()) {
		t.Error("two signatures of the same claims differ")
	}
}

// TestPayloadWire pins the payload's JSON, which the app parses in
// TypeScript (mobile/src/licensing/token.ts).
func TestPayloadWire(t *testing.T) {
	pub, priv := testKey(1)
	tok := mustSign(t, priv, testClaims())
	raw, err := base64.RawURLEncoding.DecodeString(tok[:strings.IndexByte(tok, '.')])
	if err != nil {
		t.Fatal(err)
	}
	want := `{"account":"acc-1","box_id":"ZTEG00000001","tier":"basic","expires_at":1796126400,"issued_at":1795262400,"key_id":"` + KeyID(pub) + `"}`
	if string(raw) != want {
		t.Errorf("payload = %s\nwant      %s", raw, want)
	}
}

// TestAppFixture is the token pinned in mobile/src/__tests__/token.test.ts:
// the key with seed bytes 1..32 signing a Max pass. If this fails, the wire
// format changed and the app's fixture must be regenerated with it.
func TestAppFixture(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	iat := time.Unix(1795000000, 0)
	tok := mustSign(t, priv, Claims{Account: "acc_7f3a", BoxID: "ZTEG00000001", Tier: TierMax, IssuedAt: iat, ExpiresAt: iat.Add(30 * day)})
	const want = "eyJhY2NvdW50IjoiYWNjXzdmM2EiLCJib3hfaWQiOiJaVEVHMDAwMDAwMDEiLCJ0aWVyIjoibWF4IiwiZXhwaXJlc19hdCI6MTc5NzU5MjAwMCwiaXNzdWVkX2F0IjoxNzk1MDAwMDAwLCJrZXlfaWQiOiI2NWI2MDY3M2Q2ZWQ4ODRiIn0.EzDS623Tq9RoLnW0ti8vUARunQt9F8WYt07LUP5AvTCb6EzDcw1fDyloOaQi9fCNlNGJrIZMkQx3iGj7VuSPAQ"
	if tok != want {
		t.Errorf("app fixture token changed:\n%s", tok)
	}
}

func TestKeyID(t *testing.T) {
	pub, _ := testKey(1)
	id := KeyID(pub)
	if len(id) != 16 || !validKeyID(id) {
		t.Errorf("KeyID = %q, want 16 lowercase hex digits", id)
	}
	other, _ := testKey(2)
	if KeyID(other) == id {
		t.Error("two keys share a key id")
	}
}

func TestParseKeySet(t *testing.T) {
	pub1, _ := testKey(1)
	pub2, _ := testKey(2)
	enc := base64.StdEncoding.EncodeToString
	ks, err := ParseKeySet(" " + enc(pub1) + ",\n" + enc(pub2) + " ")
	if err != nil {
		t.Fatal(err)
	}
	if ks.Len() != 2 || !strings.Contains(strings.Join(ks.IDs(), ","), KeyID(pub2)) {
		t.Errorf("ParseKeySet = %v", ks.IDs())
	}
	if ks, err := ParseKeySet(""); err != nil || ks.Len() != 0 {
		t.Errorf("ParseKeySet(\"\") = %v, %v", ks.IDs(), err)
	}
	for _, bad := range []string{"not base64!", enc(pub1[:16]), enc(pub1) + ",AAAA"} {
		if _, err := ParseKeySet(bad); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("ParseKeySet(%q) = %v, want ErrInvalidKey", bad, err)
		}
	}
	if _, err := NewKeySet(pub1[:31]); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("NewKeySet(short) = %v", err)
	}
}

// TestVerifyPicksTheKeyByID: with several trusted keys (a rotation), each
// token verifies with the key its id names.
func TestVerifyPicksTheKeyByID(t *testing.T) {
	pub1, priv1 := testKey(1)
	pub2, priv2 := testKey(2)
	ks := keySet(t, pub1, pub2)
	for _, priv := range []ed25519.PrivateKey{priv1, priv2} {
		if _, _, err := Verify(ks, mustSign(t, priv, testClaims()), t0); err != nil {
			t.Errorf("Verify with a trusted key: %v", err)
		}
	}
}

// TestVerifyExpiry: a token is fine until expires_at, then within the 3-day
// offline grace, then refused.
func TestVerifyExpiry(t *testing.T) {
	pub, priv := testKey(1)
	c := testClaims()
	tok := mustSign(t, priv, c)
	for _, tc := range []struct {
		name      string
		now       time.Time
		wantGrace bool
		wantErr   error
	}{
		{"just issued", c.IssuedAt, false, nil},
		{"one second before expiry", c.ExpiresAt.Add(-time.Second), false, nil},
		{"at expiry", c.ExpiresAt, true, nil},
		{"two days offline", c.ExpiresAt.Add(2 * day), true, nil},
		{"last second of grace", c.ExpiresAt.Add(OfflineGrace - time.Second), true, nil},
		{"grace over", c.ExpiresAt.Add(OfflineGrace), false, ErrTokenExpired},
		{"long gone", c.ExpiresAt.Add(100 * day), false, ErrTokenExpired},
		// A phone whose clock runs behind is not refused for it.
		{"clock before issuance", c.IssuedAt.Add(-day), false, nil},
	} {
		_, grace, err := Verify(keySet(t, pub), tok, tc.now)
		if !errors.Is(err, tc.wantErr) || grace != tc.wantGrace {
			t.Errorf("%s: Verify = grace %v, %v; want grace %v, %v", tc.name, grace, err, tc.wantGrace, tc.wantErr)
		}
	}
}

func TestVerifyRejects(t *testing.T) {
	pub, priv := testKey(1)
	_, otherPriv := testKey(2)
	tok := mustSign(t, priv, testClaims())
	payload, sig, _ := strings.Cut(tok, ".")
	kid := KeyID(pub)

	upgraded := testClaims()
	upgraded.Tier = TierMax
	forgedPayload, _, _ := strings.Cut(mustSign(t, otherPriv, upgraded), ".")

	enc := base64.RawURLEncoding.EncodeToString
	signedBy := func(priv ed25519.PrivateKey, payload string) string {
		return enc([]byte(payload)) + "." + enc(ed25519.Sign(priv, []byte(payload)))
	}
	signedJunk := func(payload string) string { return signedBy(priv, payload) }
	claimsJSON := func(extra string) string {
		return `{"account":"a","box_id":"B","tier":"max","expires_at":1796126400,"issued_at":1795262400` + extra + `}`
	}
	// The payload re-signed with the other key but naming the trusted key's
	// id: the id is not a capability, the signature still has to hold.
	impostor := signedBy(otherPriv, claimsJSON(`,"key_id":"`+kid+`"`))
	flipped := []byte(sig)
	flipped[0] ^= 'A' ^ 'B'
	if flipped[0] == sig[0] {
		flipped[0] = 'C'
	}

	ks := keySet(t, pub)
	for _, tc := range []struct {
		name string
		keys KeySet
		tok  string
		want error
	}{
		{"signed by an untrusted key", ks, mustSign(t, otherPriv, testClaims()), ErrUnknownKey},
		{"payload swapped for an upgraded one", ks, forgedPayload + "." + sig, ErrUnknownKey},
		{"impostor naming the trusted key id", ks, impostor, ErrBadSignature},
		{"signature altered", ks, payload + "." + string(flipped), ErrBadSignature},
		{"no trusted key", KeySet{}, tok, ErrUnknownKey},
		{"no key id", ks, signedJunk(claimsJSON("")), ErrUnknownKey},
		{"empty", ks, "", ErrMalformedToken},
		{"no dot", ks, payload + sig, ErrMalformedToken},
		{"three parts", ks, tok + ".x", ErrMalformedToken},
		{"empty signature", ks, payload + ".", ErrMalformedToken},
		{"padded base64", ks, payload + "==." + sig, ErrMalformedToken},
		{"standard base64 alphabet", ks, strings.NewReplacer("-", "+", "_", "/").Replace(payload) + "+." + sig, ErrMalformedToken},
		{"short signature", ks, payload + "." + enc([]byte("short")), ErrMalformedToken},
		{"signed, not JSON", ks, signedJunk("not json"), ErrMalformedToken},
		{"signed, unknown tier", ks, signedJunk(strings.Replace(claimsJSON(`,"key_id":"`+kid+`"`), "max", "gold", 1)), ErrMalformedToken},
		{"signed, free tier", ks, signedJunk(strings.Replace(claimsJSON(`,"key_id":"`+kid+`"`), "max", "free", 1)), ErrMalformedToken},
		{"signed, no box", ks, signedJunk(`{"account":"a","tier":"max","expires_at":1796126400,"issued_at":1795262400,"key_id":"` + kid + `"}`), ErrMalformedToken},
		{"signed, extra field", ks, signedJunk(claimsJSON(`,"key_id":"` + kid + `","admin":true`)), ErrMalformedToken},
		{"signed, trailing data", ks, signedJunk(claimsJSON(`,"key_id":"`+kid+`"`) + "{}"), ErrMalformedToken},
		{"signed, lifetime over 30 days", ks, signedJunk(strings.Replace(claimsJSON(`,"key_id":"`+kid+`"`), "1796126400", "1799999999", 1)), ErrMalformedToken},
	} {
		if _, _, err := Verify(tc.keys, tc.tok, t0); !errors.Is(err, tc.want) {
			t.Errorf("%s: Verify = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestSignRejects(t *testing.T) {
	_, priv := testKey(1)
	mod := func(f func(*Claims)) Claims { c := testClaims(); f(&c); return c }
	for _, tc := range []struct {
		name string
		priv ed25519.PrivateKey
		c    Claims
		want error
	}{
		{"no key", nil, testClaims(), ErrInvalidKey},
		{"short key", priv[:32], testClaims(), ErrInvalidKey},
		{"no account", priv, mod(func(c *Claims) { c.Account = "" }), ErrInvalidClaims},
		{"no box", priv, mod(func(c *Claims) { c.BoxID = "" }), ErrInvalidClaims},
		{"free tier", priv, mod(func(c *Claims) { c.Tier = TierFree }), ErrInvalidClaims},
		{"unknown tier", priv, mod(func(c *Claims) { c.Tier = "gold" }), ErrInvalidClaims},
		{"expires before issued", priv, mod(func(c *Claims) { c.ExpiresAt = c.IssuedAt }), ErrInvalidClaims},
		{"lifetime over 30 days", priv, mod(func(c *Claims) { c.ExpiresAt = c.IssuedAt.Add(30*day + time.Second) }), ErrInvalidClaims},
	} {
		if _, err := Sign(tc.priv, tc.c); !errors.Is(err, tc.want) {
			t.Errorf("%s: Sign = %v, want %v", tc.name, err, tc.want)
		}
	}
}
