// Package signingkey reads the hub's Ed25519 entitlement signing key
// (RFC-006: "the signing key is kept out of the repository"). It comes from
// an environment variable or a file — a mounted secret — and is accepted in
// the encodings an operator is likely to have at hand: the PEM that
// `openssl genpkey -algorithm ed25519` writes, or the 32-byte seed (or the
// 64-byte private key) in base64, base64url or hex.
package signingkey

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
)

// ErrInvalid is a value that is not an Ed25519 private key.
var ErrInvalid = errors.New("licensing: not an Ed25519 private key (PEM, or a 32-byte seed in base64 or hex)")

// Parse reads a private key.
func Parse(raw []byte) (ed25519.PrivateKey, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, ErrInvalid
	}
	if block, _ := pem.Decode(raw); block != nil {
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, ErrInvalid
		}
		priv, ok := k.(ed25519.PrivateKey)
		if !ok {
			return nil, ErrInvalid
		}
		return priv, nil
	}
	s := strings.TrimSpace(string(raw))
	for _, dec := range []func(string) ([]byte, error){
		hex.DecodeString,
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
	} {
		b, err := dec(s)
		if err != nil {
			continue
		}
		switch len(b) {
		case ed25519.SeedSize:
			return ed25519.NewKeyFromSeed(b), nil
		case ed25519.PrivateKeySize:
			// The second half is the public key; one that does not match
			// the seed would sign tokens no app can verify.
			k := ed25519.NewKeyFromSeed(b[:ed25519.SeedSize])
			if !bytes.Equal(k, b) {
				return nil, ErrInvalid
			}
			return k, nil
		}
	}
	return nil, ErrInvalid
}

// Load reads the key from a value or a file path, at most one of them. Both
// empty is no key (nil, nil): a hub that does not sell Pro.
func Load(value, path string) (ed25519.PrivateKey, error) {
	switch {
	case value != "" && path != "":
		return nil, errors.New("licensing: set the signing key or its file, not both")
	case value != "":
		return Parse([]byte(value))
	case path != "":
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("licensing: signing key file: %w", err)
		}
		return Parse(raw)
	}
	return nil, nil
}

// KeyID names a public key: the first 8 bytes of its SHA-256, in hex
// (entitlement.KeyID). Every token carries the id of the key that signed it.
func KeyID(pub ed25519.PublicKey) string { return entitlement.KeyID(pub) }
