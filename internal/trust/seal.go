package trust

import (
	"encoding/json"
	"fmt"
	"io"

	"golang.org/x/crypto/nacl/box"
)

// The box password is the household's most sensitive secret: whoever has it
// controls the router. The owner types it once, seals it for one relay with
// nacl/box (owner X25519 -> relay X25519), and the hub only ever stores
// ciphertext it cannot read (RFC-005, Trust model).

const nonceSize = 24

// Secret is a value that must not reach a log or a UI. It prints redacted,
// including through %v and %#v, and is only readable by asking for it.
type Secret string

// String implements fmt.Stringer with a redacted value.
func (s Secret) String() string { return "[redacted]" }

// GoString implements fmt.GoStringer, so %#v does not leak either.
func (s Secret) GoString() string { return "[redacted]" }

// Reveal returns the secret itself. Every call site is a place to check that
// the value is going to the box and nowhere else.
func (s Secret) Reveal() string { return string(s) }

// Credentials are the box's login, sealed for one relay. AgentID and
// TenantID are sealed with them so a blob cannot be moved to another relay
// or another household.
type Credentials struct {
	TenantID string `json:"tenant_id"`
	AgentID  string `json:"agent_id"`
	Username string `json:"username"`
	Password Secret `json:"password"`
	Version  uint64 `json:"version"`
}

// SealCredentials encrypts the credentials from the owner to one relay. The
// result is `nonce || box`, which is what the hub stores.
func SealCredentials(rand io.Reader, owner *Identity, relayX []byte, c Credentials) ([]byte, error) {
	if len(relayX) != boxKeySize {
		return nil, fmt.Errorf("%w: relay X25519 key", ErrMalformed)
	}
	plain, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("trust: seal: %w", err)
	}
	var nonce [nonceSize]byte
	if _, err := io.ReadFull(rand, nonce[:]); err != nil {
		return nil, fmt.Errorf("trust: seal nonce: %w", err)
	}
	var peer [boxKeySize]byte
	copy(peer[:], relayX)
	return box.Seal(nonce[:], plain, &nonce, &peer, &owner.boxPriv), nil
}

// OpenCredentials decrypts a blob the owner sealed for this relay.
//
// box authenticates the sender: it only opens with the owner's X25519 key,
// which the relay takes from the signed bundle. A blob sealed by anyone else,
// the hub included, fails here. The tenant and agent inside must match too,
// so a blob cannot be replayed onto another relay of the same household.
func OpenCredentials(relay *Identity, ownerX []byte, blob []byte, wantTenant, wantAgent string) (Credentials, error) {
	if len(ownerX) != boxKeySize {
		return Credentials{}, fmt.Errorf("%w: owner X25519 key", ErrMalformed)
	}
	if len(blob) <= nonceSize {
		return Credentials{}, fmt.Errorf("%w: sealed blob too short", ErrMalformed)
	}
	var nonce [nonceSize]byte
	copy(nonce[:], blob[:nonceSize])
	var peer [boxKeySize]byte
	copy(peer[:], ownerX)

	plain, ok := box.Open(nil, blob[nonceSize:], &nonce, &peer, &relay.boxPriv)
	if !ok {
		return Credentials{}, fmt.Errorf("%w: sealed credentials are not from the owner", ErrBadSignature)
	}
	var c Credentials
	if err := json.Unmarshal(plain, &c); err != nil {
		return Credentials{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if c.TenantID != wantTenant {
		return Credentials{}, fmt.Errorf("%w: credentials are for %s", ErrTenant, c.TenantID)
	}
	if c.AgentID != wantAgent {
		return Credentials{}, fmt.Errorf("%w: credentials are for agent %s", ErrMalformed, c.AgentID)
	}
	return c, nil
}
