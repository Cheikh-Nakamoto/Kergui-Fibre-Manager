package domain

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

const (
	// TokenMaxLifetime caps one token, whatever the pass: the app refreshes
	// it whenever it has network (RFC-006, Entitlement token).
	TokenMaxLifetime = 30 * 24 * time.Hour
	// OfflineGrace is how long past expires_at a token is still honoured,
	// for a phone that could not reach the hub to refresh it.
	OfflineGrace = 3 * 24 * time.Hour
)

var (
	// ErrInvalidKey is a signing or verification key of the wrong size.
	ErrInvalidKey = errors.New("licensing: not an Ed25519 key")
	// ErrInvalidClaims refuses to sign claims no valid token could carry.
	ErrInvalidClaims = errors.New("licensing: invalid entitlement claims")
	// ErrMalformedToken is a token that does not parse, or whose signed
	// payload is not a valid entitlement.
	ErrMalformedToken = errors.New("licensing: malformed entitlement token")
	// ErrBadSignature is a token not signed by this key: tampered with, or
	// from another hub.
	ErrBadSignature = errors.New("licensing: entitlement token signature does not verify")
	// ErrTokenExpired is a token past its expiry and its offline grace.
	ErrTokenExpired = errors.New("licensing: entitlement token expired")
)

// Claims is what an entitlement token says: this account may use this tier
// on this box until ExpiresAt. Times travel as Unix seconds.
type Claims struct {
	Account   AccountID
	BoxID     BoxID
	Tier      Tier
	ExpiresAt time.Time
	IssuedAt  time.Time
}

// wireClaims is the payload's JSON. Field order is fixed by the struct, so a
// token is byte-for-byte reproducible.
type wireClaims struct {
	Account   AccountID `json:"account"`
	BoxID     BoxID     `json:"box_id"`
	Tier      Tier      `json:"tier"`
	ExpiresAt int64     `json:"expires_at"`
	IssuedAt  int64     `json:"issued_at"`
}

// IssueClaims is the token to hand out for a subscription at now: it ends
// with the pass, or TokenMaxLifetime from now, whichever comes first.
func IssueClaims(s Subscription, now time.Time) (Claims, error) {
	if !s.Active(now) {
		return Claims{}, ErrNoActivePass
	}
	exp := s.ExpiresAt
	if limit := now.Add(TokenMaxLifetime); limit.Before(exp) {
		exp = limit
	}
	return Claims{Account: s.Account, BoxID: s.BoxID, Tier: s.Tier, ExpiresAt: exp, IssuedAt: now}, nil
}

func (c Claims) valid() bool {
	life := c.ExpiresAt.Sub(c.IssuedAt)
	return c.Account != "" && c.BoxID != "" && c.Tier.Includes(TierBasic) &&
		life > 0 && life <= TokenMaxLifetime
}

var b64 = base64.RawURLEncoding.Strict()

// Sign encodes and signs the claims as base64url(payload JSON) "."
// base64url(Ed25519 signature of the payload JSON), unpadded. Times are
// truncated to the second.
func Sign(priv ed25519.PrivateKey, c Claims) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", ErrInvalidKey
	}
	w := wireClaims{Account: c.Account, BoxID: c.BoxID, Tier: c.Tier,
		ExpiresAt: c.ExpiresAt.Unix(), IssuedAt: c.IssuedAt.Unix()}
	if !w.claims().valid() {
		return "", ErrInvalidClaims
	}
	payload, err := json.Marshal(w)
	if err != nil {
		return "", err
	}
	return b64.EncodeToString(payload) + "." + b64.EncodeToString(ed25519.Sign(priv, payload)), nil
}

func (w wireClaims) claims() Claims {
	return Claims{Account: w.Account, BoxID: w.BoxID, Tier: w.Tier,
		ExpiresAt: time.Unix(w.ExpiresAt, 0).UTC(), IssuedAt: time.Unix(w.IssuedAt, 0).UTC()}
}

// Verify checks a token offline against the hub's public key and returns its
// claims. inGrace is true once expires_at has passed but the OfflineGrace has
// not: the app should keep the tier and refresh the token. The issuance time
// is not checked against now, so a phone whose clock runs behind is not
// locked out. The caller still compares BoxID with the box it is on.
func Verify(pub ed25519.PublicKey, token string, now time.Time) (c Claims, inGrace bool, err error) {
	if len(pub) != ed25519.PublicKeySize {
		return Claims{}, false, ErrInvalidKey
	}
	p64, s64, ok := strings.Cut(token, ".")
	if !ok || p64 == "" || s64 == "" || strings.Contains(s64, ".") {
		return Claims{}, false, ErrMalformedToken
	}
	payload, err := b64.DecodeString(p64)
	if err != nil {
		return Claims{}, false, ErrMalformedToken
	}
	sig, err := b64.DecodeString(s64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return Claims{}, false, ErrMalformedToken
	}
	if !ed25519.Verify(pub, payload, sig) {
		return Claims{}, false, ErrBadSignature
	}
	c, err = decodeClaims(payload)
	if err != nil {
		return Claims{}, false, err
	}
	if !now.Before(c.ExpiresAt.Add(OfflineGrace)) {
		return Claims{}, false, ErrTokenExpired
	}
	return c, !now.Before(c.ExpiresAt), nil
}

// decodeClaims parses a signed payload strictly: no unknown field, nothing
// after the object, and claims Sign would have accepted.
func decodeClaims(payload []byte) (Claims, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	var w wireClaims
	if err := dec.Decode(&w); err != nil {
		return Claims{}, ErrMalformedToken
	}
	if _, err := dec.Token(); err != io.EOF {
		return Claims{}, ErrMalformedToken
	}
	c := w.claims()
	if !c.valid() {
		return Claims{}, ErrMalformedToken
	}
	return c, nil
}
