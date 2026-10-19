// Package trust holds the owner-rooted trust model of RFC-005: the keys, the
// signed trust bundle, the signed commands and results, pairing, sealed box
// credentials and the recovery proof.
//
// The hub only transports. Everything that decides whether a command may run
// is verified here, on the relay, against keys the owner signed. A hub that
// forged, replayed, reordered or widened a command is refused by a Verifier.
//
// The package is pure: no I/O, no clock, no randomness of its own. Callers
// pass an io.Reader for entropy and a time.Time for "now", which is what makes
// every refusal in RFC-005's Vérification section testable.
//
// A signature always covers the exact bytes that were transmitted, never a
// re-serialisation of a decoded struct: Signed[T] carries the payload it
// signed. Nothing in this package depends on Go's field order or on JSON
// staying byte-stable across versions.
package trust

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
)

// Signing domains. The domain is mixed into every signature so that a
// signature made over one kind of message can never be replayed as another.
const (
	DomainBundle   = "kergui/bundle/v1"
	DomainCommand  = "kergui/cmd/v1"
	DomainResult   = "kergui/result/v1"
	DomainSnapshot = "kergui/snapshot/v1"
	DomainPairing  = "kergui/pair/v1"
	DomainSeal     = "kergui/seal/v1"
	DomainRecovery = "kergui/recovery/v1"
)

// Refusals. Every one of them maps to a case listed in RFC-005's Vérification
// section, so a test can name what it expects to be refused.
var (
	ErrUnsigned          = errors.New("trust: message carries no signature")
	ErrBadSignature      = errors.New("trust: signature does not match")
	ErrUnknownKey        = errors.New("trust: signing key is not in the trust bundle")
	ErrRevoked           = errors.New("trust: signing key is revoked")
	ErrOwnerOnly         = errors.New("trust: household is owner-only, delegated keys may not act")
	ErrScope             = errors.New("trust: key is not delegated for this command")
	ErrDelegationExpired = errors.New("trust: delegation has expired")
	ErrExpired           = errors.New("trust: command has expired")
	ErrNotYetValid       = errors.New("trust: command is issued in the future")
	ErrTooLongLived      = errors.New("trust: command lives longer than the allowed maximum")
	ErrReplay            = errors.New("trust: command id has already been used")
	ErrTenant            = errors.New("trust: message belongs to another household")
	ErrBodyMismatch      = errors.New("trust: body does not match the signed digest")
	ErrStaleBundle       = errors.New("trust: trust bundle is not newer than the current one")
	ErrNoBundle          = errors.New("trust: no trust bundle accepted yet")
	ErrProtected         = errors.New("trust: target device is protected")
	ErrRateLimited       = errors.New("trust: delegated key is over its rate limit")
	ErrMalformed         = errors.New("trust: malformed message")
)

// Signed is a message plus the signature over the bytes it was sent as.
// Keeping the payload means a verifier never re-encodes anything, so a
// signature cannot be broken by a field added in a later version.
type Signed[T any] struct {
	Payload []byte `json:"payload"`
	Sig     []byte `json:"sig"`
}

// Sign serialises v and signs it under the given domain.
func Sign[T any](id *Identity, domain string, v T) (Signed[T], error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return Signed[T]{}, fmt.Errorf("trust: encode %s: %w", domain, err)
	}
	return Signed[T]{Payload: payload, Sig: ed25519.Sign(id.signKey, signingInput(domain, payload))}, nil
}

// Unverified decodes the payload WITHOUT checking the signature. It is how a
// verifier learns which key id to check, and how the hub reads a command it
// only transports. Nothing it returns may be acted upon before Open succeeds.
func (s Signed[T]) Unverified() (T, error) {
	var v T
	if err := json.Unmarshal(s.Payload, &v); err != nil {
		return v, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return v, nil
}

// Open checks the signature against pub and returns the decoded message.
func (s Signed[T]) Open(domain string, pub ed25519.PublicKey) (T, error) {
	var zero T
	if len(s.Sig) == 0 {
		return zero, ErrUnsigned
	}
	if len(pub) != ed25519.PublicKeySize {
		return zero, ErrUnknownKey
	}
	if !ed25519.Verify(pub, signingInput(domain, s.Payload), s.Sig) {
		return zero, ErrBadSignature
	}
	return s.Unverified()
}

// signingInput binds a payload to its domain. The NUL separator cannot occur
// in a domain name, so no payload can be made to look like another domain.
func signingInput(domain string, payload []byte) []byte {
	out := make([]byte, 0, len(domain)+1+len(payload))
	out = append(out, domain...)
	out = append(out, 0)
	return append(out, payload...)
}
