package domain

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func testKey(b byte) (ed25519.PublicKey, ed25519.PrivateKey) {
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{b}, ed25519.SeedSize))
	return priv.Public().(ed25519.PublicKey), priv
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

func TestIssueClaims(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sub     Subscription
		wantExp time.Time
	}{
		{"pass ends before 30 days", Subscription{Account: "a", BoxID: "B1", Tier: TierBasic, ExpiresAt: t0.Add(10 * day)}, t0.Add(10 * day)},
		{"a year's pass is capped at 30 days", Subscription{Account: "a", BoxID: "B1", Tier: TierMax, ExpiresAt: t0.Add(365 * day)}, t0.Add(30 * day)},
		{"exactly 30 days", Subscription{Account: "a", BoxID: "B1", Tier: TierMax, ExpiresAt: t0.Add(30 * day)}, t0.Add(30 * day)},
	} {
		c, err := IssueClaims(tc.sub, t0)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		want := Claims{Account: tc.sub.Account, BoxID: tc.sub.BoxID, Tier: tc.sub.Tier, IssuedAt: t0, ExpiresAt: tc.wantExp}
		if c != want {
			t.Errorf("%s: IssueClaims = %+v, want %+v", tc.name, c, want)
		}
	}
	if _, err := IssueClaims(Subscription{Account: "a", BoxID: "B1", Tier: TierMax, ExpiresAt: t0}, t0); !errors.Is(err, ErrNoActivePass) {
		t.Errorf("an expired pass got a token: %v", err)
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv := testKey(1)
	want := testClaims()
	tok := mustSign(t, priv, want)
	if strings.Count(tok, ".") != 1 || strings.ContainsAny(tok, "=+/") {
		t.Errorf("token is not compact base64url: %q", tok)
	}
	got, grace, err := Verify(pub, tok, t0.Add(day))
	if err != nil || grace {
		t.Fatalf("Verify = %v, grace %v", err, grace)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) || !got.IssuedAt.Equal(want.IssuedAt) ||
		got.Account != want.Account || got.BoxID != want.BoxID || got.Tier != want.Tier {
		t.Errorf("Verify = %+v, want %+v", got, want)
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

// TestPayloadWire pins the payload's JSON, which the app parses in Kotlin.
func TestPayloadWire(t *testing.T) {
	_, priv := testKey(1)
	tok := mustSign(t, priv, testClaims())
	raw, err := base64.RawURLEncoding.DecodeString(tok[:strings.IndexByte(tok, '.')])
	if err != nil {
		t.Fatal(err)
	}
	want := `{"account":"acc-1","box_id":"ZTEG00000001","tier":"basic","expires_at":1796126400,"issued_at":1795262400}`
	if string(raw) != want {
		t.Errorf("payload = %s\nwant      %s", raw, want)
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
		_, grace, err := Verify(pub, tok, tc.now)
		if !errors.Is(err, tc.wantErr) || grace != tc.wantGrace {
			t.Errorf("%s: Verify = grace %v, %v; want grace %v, %v", tc.name, grace, err, tc.wantGrace, tc.wantErr)
		}
	}
}

func TestVerifyRejects(t *testing.T) {
	pub, priv := testKey(1)
	otherPub, otherPriv := testKey(2)
	tok := mustSign(t, priv, testClaims())
	payload, sig, _ := strings.Cut(tok, ".")

	upgraded := testClaims()
	upgraded.Tier = TierMax
	forgedPayload, _, _ := strings.Cut(mustSign(t, otherPriv, upgraded), ".")

	enc := base64.RawURLEncoding.EncodeToString
	signedJunk := func(payload string) string {
		return enc([]byte(payload)) + "." + enc(ed25519.Sign(priv, []byte(payload)))
	}
	flipped := []byte(sig)
	flipped[0] ^= 'A' ^ 'B'
	if flipped[0] == sig[0] {
		flipped[0] = 'C'
	}

	for _, tc := range []struct {
		name string
		pub  ed25519.PublicKey
		tok  string
		want error
	}{
		{"wrong key", otherPub, tok, ErrBadSignature},
		{"payload swapped for an upgraded one", pub, forgedPayload + "." + sig, ErrBadSignature},
		{"signature altered", pub, payload + "." + string(flipped), ErrBadSignature},
		{"empty", pub, "", ErrMalformedToken},
		{"no dot", pub, payload + sig, ErrMalformedToken},
		{"three parts", pub, tok + ".x", ErrMalformedToken},
		{"empty signature", pub, payload + ".", ErrMalformedToken},
		{"padded base64", pub, payload + "==." + sig, ErrMalformedToken},
		{"standard base64 alphabet", pub, strings.NewReplacer("-", "+", "_", "/").Replace(payload) + "+." + sig, ErrMalformedToken},
		{"short signature", pub, payload + "." + enc([]byte("short")), ErrMalformedToken},
		{"signed, not JSON", pub, signedJunk("not json"), ErrMalformedToken},
		{"signed, unknown tier", pub, signedJunk(`{"account":"a","box_id":"B","tier":"gold","expires_at":1796126400,"issued_at":1795262400}`), ErrMalformedToken},
		{"signed, free tier", pub, signedJunk(`{"account":"a","box_id":"B","tier":"free","expires_at":1796126400,"issued_at":1795262400}`), ErrMalformedToken},
		{"signed, no box", pub, signedJunk(`{"account":"a","tier":"max","expires_at":1796126400,"issued_at":1795262400}`), ErrMalformedToken},
		{"signed, extra field", pub, signedJunk(`{"account":"a","box_id":"B","tier":"max","expires_at":1796126400,"issued_at":1795262400,"admin":true}`), ErrMalformedToken},
		{"signed, trailing data", pub, signedJunk(`{"account":"a","box_id":"B","tier":"max","expires_at":1796126400,"issued_at":1795262400}{}`), ErrMalformedToken},
		{"signed, lifetime over 30 days", pub, signedJunk(`{"account":"a","box_id":"B","tier":"max","expires_at":1799999999,"issued_at":1795262400}`), ErrMalformedToken},
		{"no key", nil, tok, ErrInvalidKey},
		{"short key", pub[:16], tok, ErrInvalidKey},
	} {
		if _, _, err := Verify(tc.pub, tc.tok, t0); !errors.Is(err, tc.want) {
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
