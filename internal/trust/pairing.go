package trust

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
)

// Pairing does not trust the hub (RFC-005). The owner's QR carries the
// pairing code and the owner's key fingerprint; the relay pins that
// fingerprint and sends its own keys through the hub; both screens then show
// the same six digits, derived from both parties' keys. A hub that swapped a
// key makes the two codes differ, and the owner stops.

// invitePrefix versions the QR payload, so a future format is recognised
// rather than misread.
const invitePrefix = "kgp1"

var inviteEnc = base32.StdEncoding.WithPadding(base32.NoPadding)

// Invite is what the owner's QR code carries.
type Invite struct {
	Code string // the pairing code, as handed out by the hub
	// OwnerFingerprint is the owner's key id. The relay pins it before it
	// trusts any bundle, so the hub cannot insert its own owner key.
	OwnerFingerprint string
}

// String renders the invite for a QR code.
func (i Invite) String() string {
	return invitePrefix + "." + inviteEnc.EncodeToString([]byte(i.Code)) + "." + i.OwnerFingerprint
}

// ParseInvite reads a scanned QR payload.
func ParseInvite(s string) (Invite, error) {
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) != 3 || parts[0] != invitePrefix {
		return Invite{}, fmt.Errorf("%w: invite", ErrMalformed)
	}
	code, err := inviteEnc.DecodeString(strings.ToUpper(parts[1]))
	if err != nil {
		return Invite{}, fmt.Errorf("%w: invite code: %v", ErrMalformed, err)
	}
	if len(code) == 0 || parts[2] == "" {
		return Invite{}, fmt.Errorf("%w: empty invite", ErrMalformed)
	}
	return Invite{Code: string(code), OwnerFingerprint: parts[2]}, nil
}

// PairingCode derives the six digits both screens show. It covers both
// parties' signing and sealing keys, so swapping any one of them changes the
// code.
//
// Reducing a uniform 64-bit value modulo 10^6 biases the result by about
// 10^-13, far below what six digits are worth; it is not a secret, only a
// human comparison.
func PairingCode(owner, relay PublicKeys) string {
	h := sha256.New()
	h.Write([]byte(DomainPairing))
	for _, k := range [][]byte{owner.Ed25519, owner.X25519, relay.Ed25519, relay.X25519} {
		var n [2]byte
		binary.BigEndian.PutUint16(n[:], uint16(len(k)))
		h.Write(n[:]) // length-prefixed, so two keys cannot be re-cut
		h.Write(k)
	}
	sum := h.Sum(nil)
	return fmt.Sprintf("%06d", binary.BigEndian.Uint64(sum[:8])%1_000_000)
}
