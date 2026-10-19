package trust

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// Recovery covers the owner losing their phone with no co-owner device
// signed in advance: at home, on the Wi-Fi, the new phone proves it knows
// the box password and the relay signs its new key in. Whoever knows that
// password already controls the box, so it is the right fallback.
//
// RFC-005 wrote the proof as HMAC(box password, nonce ‖ new key). That
// would let whoever sees the proof — the hub, if the exchange ever went
// through it — try passwords offline, and box passwords are often weak. The
// key is therefore stretched with argon2id first, at the same cost as the
// credential vault, which turns each guess into ~64 MiB and a visible
// fraction of a second. The exchange itself stays on the LAN (H3): the two
// protections are independent.

// Argon2id parameters, matching internal/adapter/persistence/vault.
const (
	recoveryTime    = 1
	recoveryMemory  = 64 * 1024 // KiB
	recoveryThreads = 4
	recoveryKeyLen  = 32
	// RecoveryNonceSize is the length of the nonce the relay hands out.
	RecoveryNonceSize = 32
)

// RecoveryProof derives the proof the new phone sends. The nonce comes from
// the relay, so the owner cannot be made to sign a stale challenge.
func RecoveryProof(tenantID, boxPassword string, nonce []byte, newKey ed25519.PublicKey) ([]byte, error) {
	if len(nonce) != RecoveryNonceSize {
		return nil, fmt.Errorf("%w: recovery nonce must be %d bytes", ErrMalformed, RecoveryNonceSize)
	}
	if len(newKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: new owner key", ErrMalformed)
	}
	// The salt binds the stretch to this household and this challenge, so a
	// table built for one is worthless for another.
	salt := sha256.Sum256(concat([]byte(DomainRecovery), []byte(tenantID), nonce))
	key := argon2.IDKey([]byte(boxPassword), salt[:], recoveryTime, recoveryMemory, recoveryThreads, recoveryKeyLen)

	mac := hmac.New(sha256.New, key)
	mac.Write(concat([]byte(DomainRecovery), nonce, newKey))
	return mac.Sum(nil), nil
}

// VerifyRecovery checks a proof in constant time.
func VerifyRecovery(proof []byte, tenantID, boxPassword string, nonce []byte, newKey ed25519.PublicKey) bool {
	want, err := RecoveryProof(tenantID, boxPassword, nonce, newKey)
	if err != nil {
		return false
	}
	return hmac.Equal(proof, want)
}

// concat joins length-prefixed parts, so no two different splits can produce
// the same bytes.
func concat(parts ...[]byte) []byte {
	out := make([]byte, 0, 64)
	for _, p := range parts {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(p)))
		out = append(out, n[:]...)
		out = append(out, p...)
	}
	return out
}
