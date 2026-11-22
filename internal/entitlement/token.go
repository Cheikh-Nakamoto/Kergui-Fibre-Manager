// Package entitlement is the format and the rules of the hub's signed
// entitlement token (RFC-006, Entitlement token), shared by the hub, which
// signs it, and the mobile core, which verifies it offline and gates the paid
// actions with it. It is standard library only: the mobile core links it, and
// must never pull the hub's kernel or Postgres driver along
// (tests/architecture_test.go).
//
// Wire format:
//
//	base64url(payload JSON) "." base64url(Ed25519 signature of those bytes)
//
// unpadded, where the payload is
//
//	{"account","box_id","tier","expires_at","issued_at","key_id"}
//
// with Unix-second times and key_id = KeyID of the signing key. The app's
// mobile/src/licensing/token.ts parses the same bytes.
package entitlement

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

const (
	// TokenMaxLifetime caps one token, whatever the pass: the app refreshes
	// it whenever it has network.
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
	// ErrUnknownKey is a token whose key id is not in the verifier's key
	// set: signed by a key this build does not trust.
	ErrUnknownKey = errors.New("licensing: entitlement token signed by an unknown key")
	// ErrRevokedKey is a token whose key id the verifier knows to be
	// revoked: the key may have leaked, so nothing it signed is honoured,
	// and the app asks the hub for a fresh token.
	ErrRevokedKey = errors.New("licensing: entitlement token signed by a revoked key")
	// ErrBadSignature is a token whose signature does not verify with the
	// key its key id names: tampered with.
	ErrBadSignature = errors.New("licensing: entitlement token signature does not verify")
	// ErrTokenExpired is a token past its expiry and its offline grace.
	ErrTokenExpired = errors.New("licensing: entitlement token expired")
)

// Claims is what an entitlement token says: this account may use this tier
// on this box until ExpiresAt. KeyID names the key that signed it.
type Claims struct {
	Account   string
	BoxID     string
	Tier      Tier
	ExpiresAt time.Time
	IssuedAt  time.Time
	KeyID     string
}

// wireClaims is the payload's JSON. Field order is fixed by the struct, so a
// token is byte-for-byte reproducible.
type wireClaims struct {
	Account   string `json:"account"`
	BoxID     string `json:"box_id"`
	Tier      Tier   `json:"tier"`
	ExpiresAt int64  `json:"expires_at"`
	IssuedAt  int64  `json:"issued_at"`
	KeyID     string `json:"key_id"`
}

func (w wireClaims) claims() Claims {
	return Claims{Account: w.Account, BoxID: w.BoxID, Tier: w.Tier,
		ExpiresAt: time.Unix(w.ExpiresAt, 0).UTC(), IssuedAt: time.Unix(w.IssuedAt, 0).UTC(), KeyID: w.KeyID}
}

func (c Claims) valid() bool {
	life := c.ExpiresAt.Sub(c.IssuedAt)
	return c.Account != "" && c.BoxID != "" && c.Tier.Includes(TierBasic) &&
		life > 0 && life <= TokenMaxLifetime && validKeyID(c.KeyID)
}

// KeyID names a public key: the first 8 bytes of its SHA-256, in hex. A
// token carries the id of the key that signed it, so a verifier holding
// several keys (rotation) knows which one to use.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

func validKeyID(id string) bool {
	if len(id) != 16 {
		return false
	}
	for _, r := range id {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

var b64 = base64.RawURLEncoding.Strict()

// Sign encodes and signs the claims. The key id is the signing key's,
// whatever c.KeyID says. Times are truncated to the second.
func Sign(priv ed25519.PrivateKey, c Claims) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", ErrInvalidKey
	}
	w := wireClaims{Account: c.Account, BoxID: c.BoxID, Tier: c.Tier,
		ExpiresAt: c.ExpiresAt.Unix(), IssuedAt: c.IssuedAt.Unix(),
		KeyID: KeyID(priv.Public().(ed25519.PublicKey))}
	if !w.claims().valid() {
		return "", ErrInvalidClaims
	}
	payload, err := json.Marshal(w)
	if err != nil {
		return "", err
	}
	return b64.EncodeToString(payload) + "." + b64.EncodeToString(ed25519.Sign(priv, payload)), nil
}

// Verify checks a token offline against the trusted keys and returns its
// claims. inGrace is true once expires_at has passed but the OfflineGrace has
// not: keep the tier and refresh the token. The issuance time is not checked
// against now, so a phone whose clock runs behind is not locked out. The
// caller still compares BoxID with the box it is on (see Authorize).
func Verify(keys KeySet, token string, now time.Time) (c Claims, inGrace bool, err error) {
	payload, sig, err := splitSigned(token)
	if err != nil {
		return Claims{}, false, err
	}
	w, err := decodeWire(payload)
	if err != nil {
		return Claims{}, false, err
	}
	pub, err := keys.lookup(w.KeyID)
	if err != nil {
		return Claims{}, false, err
	}
	if !ed25519.Verify(pub, payload, sig) {
		return Claims{}, false, ErrBadSignature
	}
	if c = w.claims(); !c.valid() {
		return Claims{}, false, ErrMalformedToken
	}
	if !now.Before(c.ExpiresAt.Add(OfflineGrace)) {
		return Claims{}, false, ErrTokenExpired
	}
	return c, !now.Before(c.ExpiresAt), nil
}

// splitSigned splits "base64url(payload).base64url(signature)", strictly.
func splitSigned(doc string) (payload, sig []byte, err error) {
	p64, s64, ok := strings.Cut(doc, ".")
	if !ok || p64 == "" || s64 == "" || strings.Contains(s64, ".") {
		return nil, nil, ErrMalformedToken
	}
	if payload, err = b64.DecodeString(p64); err != nil {
		return nil, nil, ErrMalformedToken
	}
	if sig, err = b64.DecodeString(s64); err != nil || len(sig) != ed25519.SignatureSize {
		return nil, nil, ErrMalformedToken
	}
	return payload, sig, nil
}

// decodeWire parses a token payload strictly. Whether the claims make sense
// is checked once the signature holds.
func decodeWire(payload []byte) (wireClaims, error) {
	var w wireClaims
	if err := decodeStrict(payload, &w); err != nil {
		return wireClaims{}, ErrMalformedToken
	}
	return w, nil
}

// decodeStrict parses one JSON object: no unknown field, nothing after it.
func decodeStrict(payload []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing data")
	}
	return nil
}
