package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

// Secret sizes and lifetimes, from RFC-005.
const (
	// PairingCodeBits is the entropy of a pairing code. It is short-lived
	// and single-use, but it is what a new relay presents to join a
	// household, so it is not guessable.
	PairingCodeBits = 128
	// TokenPrefix marks a relay token in logs and support messages, the
	// way GitHub's ghp_ does, so one that leaks is recognisable.
	TokenPrefix = "kga_"
	tokenBytes  = 32
)

// codeAlphabet avoids padding and reads back over the phone.
var codeAlphabet = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewPairingCode returns a fresh code and the hash to store. The plain code
// leaves in the response and is never written down.
func NewPairingCode(r io.Reader) (code string, hash []byte, err error) {
	raw := make([]byte, PairingCodeBits/8)
	if _, err := io.ReadFull(r, raw); err != nil {
		return "", nil, fmt.Errorf("agents: pairing code: %w", err)
	}
	code = group(codeAlphabet.EncodeToString(raw))
	return code, HashSecret(code), nil
}

// NewAgentToken returns a fresh relay token and the hash to store.
func NewAgentToken(r io.Reader) (token string, hash []byte, err error) {
	raw := make([]byte, tokenBytes)
	if _, err := io.ReadFull(r, raw); err != nil {
		return "", nil, fmt.Errorf("agents: token: %w", err)
	}
	token = TokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return token, HashSecret(token), nil
}

// HashSecret is how pairing codes and tokens are stored. SHA-256 without a
// work factor is right here and wrong for passwords: these are 128- and
// 256-bit random values, so guessing is hopeless with or without stretching,
// and they are verified on every heartbeat.
func HashSecret(s string) []byte {
	sum := sha256.Sum256([]byte(normaliseCode(s)))
	return sum[:]
}

// Rand is the source every secret here is drawn from.
var Rand io.Reader = rand.Reader

// group cuts a code into readable blocks of four.
func group(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// normaliseCode lets someone retype a code with different case or spacing.
// Tokens contain neither dashes nor spaces, so they pass through unchanged
// except for a trim.
func normaliseCode(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, TokenPrefix) {
		return s
	}
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(s))
}
