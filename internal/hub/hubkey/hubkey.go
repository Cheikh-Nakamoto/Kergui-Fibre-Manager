// Package hubkey derives the hub's narrow signing key, the one behind
// WhatsApp's "1 = block" (RFC-005, Trust model).
//
// The key is derived, not stored: HKDF-SHA256 over one secret the
// deployment holds and the household's id. That gives every household its
// own key, which the owner delegates — or refuses, or revokes — on its own,
// without the hub keeping a key vault it would then have to protect. Lose
// the seed and no household's "1" works any more; leak it and only the
// hub_block delegation is at stake, which is blocking one unprotected
// device, rate-limited, and switchable off from the owner's app.
//
// The derivation is deterministic, so a hub that restarts or a second hub
// instance derives the same key: the owner's bundle stays valid.
package hubkey

import (
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// MinSeedLen is the shortest seed accepted. The seed is the only secret
// behind every household's hub key, so it is held to the same length as the
// hub's JWT secret.
const MinSeedLen = 32

// info binds the derivation to this purpose and version. A seed reused for
// something else cannot produce the same keys.
const info = "kergui/hub_block/v1"

// ErrNoSeed says the deployment configured no seed, so "1 = block" is off.
// It is a refusal to be shown, not a failure to be logged: a hub may
// deliberately run without it.
var ErrNoSeed = errors.New("hubkey: no hub key seed is configured")

// Deriver holds the seed and hands out one key per household.
type Deriver struct{ seed []byte }

// New wires a deriver. An empty seed is reported as ErrNoSeed, which the
// caller turns into "WhatsApp blocking is not enabled on this hub".
func New(seed []byte) (*Deriver, error) {
	if len(seed) == 0 {
		return nil, ErrNoSeed
	}
	if len(seed) < MinSeedLen {
		return nil, fmt.Errorf("hubkey: the seed must be at least %d bytes", MinSeedLen)
	}
	return &Deriver{seed: append([]byte(nil), seed...)}, nil
}

// For derives the hub key of one household.
func (d *Deriver) For(tenantID string) (*trust.Identity, error) {
	if tenantID == "" {
		return nil, errors.New("hubkey: no household")
	}
	key, err := hkdf.Key(sha256.New, d.seed, nil, info+"|"+tenantID, keyMaterialLen)
	if err != nil {
		return nil, fmt.Errorf("hubkey: derive: %w", err)
	}
	// trust.Generate draws its key material from a reader, so the same
	// code path makes the owner's keys and this one: there is no second
	// implementation of key generation to keep in step.
	id, err := trust.Generate(&reader{key: key})
	if err != nil {
		return nil, fmt.Errorf("hubkey: %w", err)
	}
	return id, nil
}

// KeyID is the derived key's id, which is what the owner's bundle
// delegates.
func (d *Deriver) KeyID(tenantID string) (string, error) {
	id, err := d.For(tenantID)
	if err != nil {
		return "", err
	}
	return id.KeyID(), nil
}

// keyMaterialLen is what trust.Generate reads: an Ed25519 seed and an
// X25519 scalar, 32 bytes each.
const keyMaterialLen = 64

// reader hands the derived bytes to trust.Generate, and refuses to invent
// any more. A derivation that ran short would otherwise silently produce a
// key nobody can reproduce.
type reader struct {
	key []byte
	n   int
}

func (r *reader) Read(p []byte) (int, error) {
	if r.n >= len(r.key) {
		return 0, io.EOF
	}
	n := copy(p, r.key[r.n:])
	r.n += n
	return n, nil
}
