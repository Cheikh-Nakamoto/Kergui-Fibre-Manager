package domain

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/signingkey"
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

// The wire format and every rejection are tested in internal/entitlement;
// here only the hub's typed wrappers around it.

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
		got.Account != want.Account || got.BoxID != want.BoxID || got.Tier != want.Tier ||
		got.KeyID != signingkey.KeyID(pub) {
		t.Errorf("Verify = %+v, want %+v", got, want)
	}
}

// TestVerifyWrapper: the hub's one-key Verify refuses a token of another
// key, and a key of the wrong size.
func TestVerifyWrapper(t *testing.T) {
	_, priv := testKey(1)
	otherPub, _ := testKey(2)
	tok := mustSign(t, priv, testClaims())
	if _, _, err := Verify(otherPub, tok, t0); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("other key: %v, want ErrUnknownKey", err)
	}
	if _, _, err := Verify(nil, tok, t0); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("no key: %v, want ErrInvalidKey", err)
	}
	if _, err := Sign(priv[:32], testClaims()); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("short key: %v", err)
	}
}
