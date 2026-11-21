package googleplay_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/googleplay"
)

const (
	audience  = "https://hub.example.org/licensing/v1/webhooks/play"
	pushEmail = "rtdn-push@kergui-play.iam.gserviceaccount.com"
)

type jwks struct {
	srv  *httptest.Server
	mu   sync.Mutex
	keys map[string]*rsa.PublicKey
	hits int
}

func newJWKS(t *testing.T) *jwks {
	j := &jwks{keys: map[string]*rsa.PublicKey{"g1": &testKey.PublicKey}}
	j.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		j.mu.Lock()
		defer j.mu.Unlock()
		j.hits++
		var out struct {
			Keys []map[string]string `json:"keys"`
		}
		for kid, k := range j.keys {
			out.Keys = append(out.Keys, map[string]string{
				"kty": "RSA", "alg": "RS256", "use": "sig", "kid": kid,
				"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
			})
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(j.srv.Close)
	return j
}

func mint(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	p, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func goodClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss": "https://accounts.google.com", "aud": audience, "email": pushEmail, "email_verified": true,
		"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix(), "sub": "1234",
	}
}

func TestPushVerifier(t *testing.T) {
	j := newJWKS(t)
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	v, err := googleplay.NewPushVerifier(googleplay.PushConfig{
		Audience: audience, ServiceAccount: pushEmail, JWKSURL: j.srv.URL, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if err := v.Verify(ctx, mint(t, testKey, "g1", goodClaims(now))); err != nil {
		t.Fatalf("good token: %v", err)
	}
	c := goodClaims(now)
	c["iss"] = "accounts.google.com" // Google issues both spellings
	if err := v.Verify(ctx, mint(t, testKey, "g1", c)); err != nil {
		t.Fatalf("bare issuer: %v", err)
	}
	if j.hits != 1 {
		t.Fatalf("JWKS fetched %d times, want 1 (cached)", j.hits)
	}

	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	bad := map[string]string{
		"empty":     "",
		"garbage":   "a.b.c",
		"wrong key": mint(t, other, "g1", goodClaims(now)),
	}
	for name, mutate := range map[string]func(map[string]any){
		"audience":   func(c map[string]any) { c["aud"] = "https://evil.example/hook" },
		"email":      func(c map[string]any) { c["email"] = "someone@evil.iam.gserviceaccount.com" },
		"unverified": func(c map[string]any) { c["email_verified"] = false },
		"issuer":     func(c map[string]any) { c["iss"] = "https://evil.example" },
		"expired":    func(c map[string]any) { c["exp"] = now.Add(-10 * time.Minute).Unix() },
		"future":     func(c map[string]any) { c["iat"] = now.Add(10 * time.Minute).Unix() },
	} {
		c := goodClaims(now)
		mutate(c)
		bad[name] = mint(t, testKey, "g1", c)
	}
	// alg none, with the good claims.
	p, _ := json.Marshal(goodClaims(now))
	bad["alg none"] = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"g1"}`)) + "." +
		base64.RawURLEncoding.EncodeToString(p) + "."
	for name, tok := range bad {
		if err := v.Verify(ctx, tok); !errors.Is(err, googleplay.ErrPushUnauthenticated) {
			t.Errorf("%s: want ErrPushUnauthenticated, got %v", name, err)
		}
	}

	// Google rotates its keys: an unknown kid refetches the set once (a
	// minute after the last fetch).
	now = now.Add(2 * time.Minute)
	k2, _ := rsa.GenerateKey(rand.Reader, 2048)
	j.mu.Lock()
	j.keys["g2"] = &k2.PublicKey
	hits := j.hits
	j.mu.Unlock()
	if err := v.Verify(ctx, mint(t, k2, "g2", goodClaims(now))); err != nil {
		t.Fatalf("rotated key: %v", err)
	}
	if j.hits != hits+1 {
		t.Fatalf("rotation: %d fetches", j.hits-hits)
	}
	// An unknown kid does not hammer Google: at most one refetch a minute.
	hits = j.hits
	for range 3 {
		_ = v.Verify(ctx, mint(t, testKey, "nope", goodClaims(now)))
	}
	if j.hits != hits {
		t.Fatalf("unknown kid refetched %d times within a minute", j.hits-hits)
	}
}

func TestNewPushVerifier_NeedsAudienceAndAccount(t *testing.T) {
	if _, err := googleplay.NewPushVerifier(googleplay.PushConfig{ServiceAccount: pushEmail}); err == nil {
		t.Error("no audience accepted")
	}
	if _, err := googleplay.NewPushVerifier(googleplay.PushConfig{Audience: audience}); err == nil {
		t.Error("no service account accepted")
	}
}
