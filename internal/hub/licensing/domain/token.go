package domain

import (
	"crypto/ed25519"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
)

// The token's wire format, signing and verification live in
// internal/entitlement, shared with the mobile core that checks it offline.
// This file only adds the hub's typed ids and the issuance rule.

const (
	// TokenMaxLifetime caps one token, whatever the pass: the app refreshes
	// it whenever it has network (RFC-006, Entitlement token).
	TokenMaxLifetime = entitlement.TokenMaxLifetime
	// OfflineGrace is how long past expires_at a token is still honoured,
	// for a phone that could not reach the hub to refresh it.
	OfflineGrace = entitlement.OfflineGrace
)

var (
	ErrInvalidKey     = entitlement.ErrInvalidKey
	ErrInvalidClaims  = entitlement.ErrInvalidClaims
	ErrMalformedToken = entitlement.ErrMalformedToken
	ErrUnknownKey     = entitlement.ErrUnknownKey
	ErrBadSignature   = entitlement.ErrBadSignature
	ErrTokenExpired   = entitlement.ErrTokenExpired
)

// Claims is what an entitlement token says: this account may use this tier
// on this box until ExpiresAt. KeyID is the signing key's id, set by Sign
// and read back by Verify.
type Claims struct {
	Account   AccountID
	BoxID     BoxID
	Tier      Tier
	ExpiresAt time.Time
	IssuedAt  time.Time
	KeyID     string
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

// Sign encodes and signs the claims (entitlement.Sign).
func Sign(priv ed25519.PrivateKey, c Claims) (string, error) {
	return entitlement.Sign(priv, entitlement.Claims{
		Account: string(c.Account), BoxID: string(c.BoxID), Tier: c.Tier,
		ExpiresAt: c.ExpiresAt, IssuedAt: c.IssuedAt,
	})
}

// Verify checks a token offline against one public key (entitlement.Verify).
func Verify(pub ed25519.PublicKey, token string, now time.Time) (Claims, bool, error) {
	keys, err := entitlement.NewKeySet(pub)
	if err != nil {
		return Claims{}, false, err
	}
	c, inGrace, err := entitlement.Verify(keys, token, now)
	if err != nil {
		return Claims{}, false, err
	}
	return Claims{Account: AccountID(c.Account), BoxID: BoxID(c.BoxID), Tier: c.Tier,
		ExpiresAt: c.ExpiresAt, IssuedAt: c.IssuedAt, KeyID: c.KeyID}, inGrace, nil
}
