package trust

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"golang.org/x/crypto/curve25519"
)

// Key material sizes, in bytes.
const (
	boxKeySize      = 32
	identityVersion = 1
	// identityBlobSize is the serialised private identity: a version byte,
	// the Ed25519 seed and the X25519 scalar.
	identityBlobSize = 1 + ed25519.SeedSize + boxKeySize
)

// Identity is one party's private key material: Ed25519 to sign, X25519 to
// seal. The owner, every relay and every member allowed to control the box
// each hold one (RFC-005, Trust model).
//
// The private halves never leave Go: they live in the AES-GCM vault on a PC
// and in the app's vault on a phone, and no accessor returns them.
type Identity struct {
	signKey ed25519.PrivateKey
	boxPriv [boxKeySize]byte
	boxPub  [boxKeySize]byte
}

// PublicKeys is what a party publishes: both public halves.
type PublicKeys struct {
	Ed25519 []byte `json:"ed25519"`
	X25519  []byte `json:"x25519"`
}

// Generate creates a fresh identity from rand (crypto/rand in production).
func Generate(rand io.Reader) (*Identity, error) {
	_, signKey, err := ed25519.GenerateKey(rand)
	if err != nil {
		return nil, fmt.Errorf("trust: ed25519: %w", err)
	}
	var seed [boxKeySize]byte
	if _, err := io.ReadFull(rand, seed[:]); err != nil {
		return nil, fmt.Errorf("trust: x25519: %w", err)
	}
	return newIdentity(signKey, seed)
}

func newIdentity(signKey ed25519.PrivateKey, boxPriv [boxKeySize]byte) (*Identity, error) {
	pub, err := curve25519.X25519(boxPriv[:], curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("trust: x25519 public: %w", err)
	}
	id := &Identity{signKey: signKey, boxPriv: boxPriv}
	copy(id.boxPub[:], pub)
	return id, nil
}

// Public returns the two public keys.
func (i *Identity) Public() PublicKeys {
	return PublicKeys{
		Ed25519: append([]byte(nil), i.signKey.Public().(ed25519.PublicKey)...),
		X25519:  append([]byte(nil), i.boxPub[:]...),
	}
}

// KeyID is the short, stable name of this identity's signing key.
func (i *Identity) KeyID() string { return i.Public().KeyID() }

// Marshal serialises the private identity for the vault. It is never logged,
// never sent to the hub and never crosses into JS.
func (i *Identity) Marshal() []byte {
	out := make([]byte, 0, identityBlobSize)
	out = append(out, identityVersion)
	out = append(out, i.signKey.Seed()...)
	return append(out, i.boxPriv[:]...)
}

// ParseIdentity reads back what Marshal wrote.
func ParseIdentity(blob []byte) (*Identity, error) {
	if len(blob) != identityBlobSize || blob[0] != identityVersion {
		return nil, fmt.Errorf("%w: identity blob", ErrMalformed)
	}
	var boxPriv [boxKeySize]byte
	copy(boxPriv[:], blob[1+ed25519.SeedSize:])
	return newIdentity(ed25519.NewKeyFromSeed(blob[1:1+ed25519.SeedSize]), boxPriv)
}

// KeyID names a signing key: the first 16 bytes of its SHA-256, in hex. It is
// what a command carries, what a delegation and a revocation point at, and
// what the pairing QR shows as the owner's fingerprint.
func (p PublicKeys) KeyID() string { return KeyID(p.Ed25519) }

// KeyID names an Ed25519 public key. An empty or malformed key has no id, so
// a lookup on "" never matches a real one.
func KeyID(pub ed25519.PublicKey) string {
	if len(pub) != ed25519.PublicKeySize {
		return ""
	}
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:16])
}

// valid reports whether both halves are present and the right size.
func (p PublicKeys) valid() bool {
	return len(p.Ed25519) == ed25519.PublicKeySize && len(p.X25519) == boxKeySize
}

func (p PublicKeys) signPub() ed25519.PublicKey { return ed25519.PublicKey(p.Ed25519) }

// SigningKey is the Ed25519 half, for callers outside this package that
// have to pin it — a relay pinning the owner's key at pairing.
func (p PublicKeys) SigningKey() ed25519.PublicKey { return p.signPub() }
