package googleplay

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

// DefaultJWKSURL is where Google publishes the keys of its OIDC tokens.
const DefaultJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"

const (
	// clockSkew is how much the hub's clock may differ from Google's.
	clockSkew = 5 * time.Minute
	// jwksTTL bounds the cache when Google sends no max-age.
	jwksTTL = time.Hour
	// jwksRefetchEvery limits refetches on an unknown key id, so tokens
	// naming made-up keys cannot make the hub hammer Google.
	jwksRefetchEvery = time.Minute
)

// ErrPushUnauthenticated is a push whose OIDC token does not check out.
var ErrPushUnauthenticated = usecase.ErrPushUnauthenticated

var _ usecase.PushVerifier = (*PushVerifier)(nil)

// PushConfig configures the check of Pub/Sub's push authentication.
type PushConfig struct {
	// Audience is the aud claim the push subscription was configured
	// with; Pub/Sub's default is the push endpoint URL.
	Audience string
	// ServiceAccount is the email the push subscription signs as. It is
	// required: anyone with a Google Cloud project can get a Google-signed
	// token for any audience, so the audience alone proves nothing.
	ServiceAccount string
	// JWKSURL replaces DefaultJWKSURL (tests).
	JWKSURL string
	HTTP    *http.Client
	Now     func() time.Time
}

// PushVerifier checks the OIDC token (Authorization: Bearer) Pub/Sub sends
// with each push. It is safe for concurrent use.
type PushVerifier struct {
	cfg PushConfig

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fresh     time.Time // the cache holds until then
	lastFetch time.Time
}

// NewPushVerifier builds a PushVerifier.
func NewPushVerifier(cfg PushConfig) (*PushVerifier, error) {
	if cfg.Audience == "" {
		return nil, errors.New("googleplay: the push audience is required")
	}
	if cfg.ServiceAccount == "" {
		return nil, errors.New("googleplay: the push service account is required")
	}
	if cfg.JWKSURL == "" {
		cfg.JWKSURL = DefaultJWKSURL
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: requestTimeout}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &PushVerifier{cfg: cfg}, nil
}

type idClaims struct {
	Iss           string `json:"iss"`
	Aud           string `json:"aud"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Iat           int64  `json:"iat"`
	Exp           int64  `json:"exp"`
}

// Verify checks a raw JWT: RS256 by one of Google's keys, issued by
// accounts.google.com, for the audience, to the service account, in date.
// Every refusal wraps ErrPushUnauthenticated; a JWKS that cannot be fetched
// is another error, so the push is retried rather than dropped.
func (v *PushVerifier) Verify(ctx context.Context, raw string) error {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return fmt.Errorf("%w: not a JWT", ErrPushUnauthenticated)
	}
	var hdr struct{ Alg, Kid string }
	if err := decodeSegment(parts[0], &hdr); err != nil || hdr.Alg != "RS256" || hdr.Kid == "" {
		return fmt.Errorf("%w: header", ErrPushUnauthenticated)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("%w: signature encoding", ErrPushUnauthenticated)
	}
	key, err := v.key(ctx, hdr.Kid)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return fmt.Errorf("%w: signature", ErrPushUnauthenticated)
	}
	var c idClaims
	if err := decodeSegment(parts[1], &c); err != nil {
		return fmt.Errorf("%w: claims", ErrPushUnauthenticated)
	}
	now := v.cfg.Now()
	switch {
	case c.Iss != "https://accounts.google.com" && c.Iss != "accounts.google.com":
		return fmt.Errorf("%w: issuer %q", ErrPushUnauthenticated, c.Iss)
	case c.Aud != v.cfg.Audience:
		return fmt.Errorf("%w: audience %q", ErrPushUnauthenticated, c.Aud)
	case c.Email != v.cfg.ServiceAccount || !c.EmailVerified:
		return fmt.Errorf("%w: signed as %q", ErrPushUnauthenticated, c.Email)
	case !now.Before(time.Unix(c.Exp, 0).Add(clockSkew)):
		return fmt.Errorf("%w: expired", ErrPushUnauthenticated)
	case time.Unix(c.Iat, 0).After(now.Add(clockSkew)):
		return fmt.Errorf("%w: issued in the future", ErrPushUnauthenticated)
	}
	return nil
}

func decodeSegment(seg string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// key returns Google's key kid, fetching the set when the cache is stale or
// does not know kid (Google rotates its keys), at most once a minute.
func (v *PushVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.cfg.Now()
	k, ok := v.keys[kid]
	if ok && now.Before(v.fresh) {
		return k, nil
	}
	if v.keys != nil && now.Sub(v.lastFetch) < jwksRefetchEvery {
		if ok {
			return k, nil
		}
		return nil, fmt.Errorf("%w: unknown key %q", ErrPushUnauthenticated, kid)
	}
	keys, ttl, err := v.fetch(ctx)
	v.lastFetch = now
	if err != nil {
		if ok {
			return k, nil // a stale key beats dropping the push
		}
		return nil, err
	}
	v.keys, v.fresh = keys, now.Add(ttl)
	if k, ok = keys[kid]; !ok {
		return nil, fmt.Errorf("%w: unknown key %q", ErrPushUnauthenticated, kid)
	}
	return k, nil
}

func (v *PushVerifier) fetch(ctx context.Context) (map[string]*rsa.PublicKey, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.cfg.JWKSURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("googleplay: JWKS: %w", err)
	}
	resp, err := v.cfg.HTTP.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("googleplay: JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("googleplay: JWKS: HTTP %d", resp.StatusCode)
	}
	var set struct {
		Keys []struct {
			Kty, Kid, N, E string
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(&set); err != nil {
		return nil, 0, fmt.Errorf("googleplay: JWKS: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	return keys, maxAge(resp.Header.Get("Cache-Control")), nil
}

// maxAge reads Cache-Control's max-age, capped at jwksTTL.
func maxAge(cc string) time.Duration {
	for _, part := range strings.Split(cc, ",") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(part), "max-age="); ok {
			var secs int64
			if _, err := fmt.Sscan(v, &secs); err == nil && secs > 0 {
				if d := time.Duration(secs) * time.Second; d < jwksTTL {
					return d
				}
			}
		}
	}
	return jwksTTL
}
