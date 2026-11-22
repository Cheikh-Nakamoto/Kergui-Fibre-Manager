package entitlement

// Signing key rotation (issue #39). The hub holds one active key, which
// signs, and may publish others: the next one (shipped in an app update
// before it signs anything) and the retiring ones (no longer signing, still
// trusted until the tokens they signed have run out). A verifier trusts a
// KeySet: the keys embedded in its build plus those it learned from the hub's
// signed key list, minus the revoked key ids, which stay revoked for good.
//
// The key list has the token's wire format,
//
//	base64url(payload JSON) "." base64url(Ed25519 signature of those bytes)
//
// where the payload is
//
//	{"keys":[{"key_id","public_key","status"}],"revoked":[key ids],"issued_at","key_id"}
//
// public_key in standard base64 and key_id (the last one) naming the active
// key that signed it. It is learned only when a key the verifier already
// trusts signed it, so the hub cannot be impersonated into adding a key.
// mobile/src/licensing/keyring.ts reads the same bytes.

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// ErrMalformedKeyList is a key list that does not parse, or that the hub
// could not have signed: a key under another key's id, two active keys, a
// key both listed and revoked...
var ErrMalformedKeyList = errors.New("licensing: malformed key list")

// RevokedPrefix marks a revoked key id in a ParseKeySet list.
const RevokedPrefix = "revoked:"

// KeySet is the public keys a verifier trusts, indexed by key id, and the
// key ids it knows to be revoked. The zero value trusts nothing. Its methods
// return new sets: a KeySet is never changed in place.
type KeySet struct {
	keys    map[string]ed25519.PublicKey
	revoked map[string]struct{}
}

// NewKeySet trusts the given keys.
func NewKeySet(pubs ...ed25519.PublicKey) (KeySet, error) {
	ks := KeySet{keys: make(map[string]ed25519.PublicKey, len(pubs))}
	for _, p := range pubs {
		if len(p) != ed25519.PublicKeySize {
			return KeySet{}, ErrInvalidKey
		}
		ks.keys[KeyID(p)] = slices.Clone(p)
	}
	return ks, nil
}

// ParseKeySet reads raw 32-byte Ed25519 public keys in standard base64 (as
// GET /licensing/v1/public-key serves them and KERGUI_LICENSING_PUBLIC_KEY
// holds them), and revoked key ids written "revoked:<key id>", separated by
// commas or white space. A revocation wins over a key, whatever the order.
// An empty string is an empty set.
func ParseKeySet(s string) (KeySet, error) {
	var pubs []ed25519.PublicKey
	var revoked []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == '\r' }) {
		if id, ok := strings.CutPrefix(f, RevokedPrefix); ok {
			revoked = append(revoked, id)
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(f)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return KeySet{}, fmt.Errorf("%w: %q", ErrInvalidKey, f)
		}
		pubs = append(pubs, raw)
	}
	ks, err := NewKeySet(pubs...)
	if err != nil {
		return KeySet{}, err
	}
	return ks.Revoke(revoked...)
}

// IDs lists the trusted key ids, sorted. A revoked key is not trusted.
func (k KeySet) IDs() []string {
	return slices.Sorted(maps.Keys(k.keys))
}

// Revoked lists the revoked key ids, sorted.
func (k KeySet) Revoked() []string {
	return slices.Sorted(maps.Keys(k.revoked))
}

// Len is the number of trusted keys.
func (k KeySet) Len() int { return len(k.keys) }

func (k KeySet) clone() KeySet {
	out := KeySet{keys: maps.Clone(k.keys), revoked: maps.Clone(k.revoked)}
	if out.keys == nil {
		out.keys = map[string]ed25519.PublicKey{}
	}
	if out.revoked == nil {
		out.revoked = map[string]struct{}{}
	}
	return out
}

// Revoke returns the set with these key ids revoked: their keys are no
// longer trusted, and no list learned later trusts them again.
func (k KeySet) Revoke(ids ...string) (KeySet, error) {
	out := k.clone()
	for _, id := range ids {
		if !validKeyID(id) {
			return KeySet{}, fmt.Errorf("%w: revoked key id %q", ErrInvalidKey, id)
		}
		out.revoked[id] = struct{}{}
		delete(out.keys, id)
	}
	return out, nil
}

// Learn returns the set with what a verified key list says: its keys are
// trusted, unless revoked, and its revocations added to the known ones.
// Revocations are never undone, so replaying an older list cannot bring a
// revoked key back.
func (k KeySet) Learn(l KeyList) KeySet {
	out := k.clone()
	for _, id := range l.Revoked {
		out.revoked[id] = struct{}{}
		delete(out.keys, id)
	}
	for _, p := range l.Keys {
		if _, gone := out.revoked[p.KeyID]; !gone && len(p.PublicKey) == ed25519.PublicKeySize && KeyID(p.PublicKey) == p.KeyID {
			out.keys[p.KeyID] = slices.Clone(p.PublicKey)
		}
	}
	return out
}

// lookup is the trusted key with this id.
func (k KeySet) lookup(id string) (ed25519.PublicKey, error) {
	if _, gone := k.revoked[id]; gone {
		return nil, ErrRevokedKey
	}
	pub, ok := k.keys[id]
	if !ok {
		return nil, ErrUnknownKey
	}
	return pub, nil
}

// KeyStatus is where a published key is in its rotation.
type KeyStatus string

const (
	// KeyNext is published so app updates can embed it; it signs nothing yet.
	KeyNext KeyStatus = "next"
	// KeyActive signs the tokens and the key list. There is one.
	KeyActive KeyStatus = "active"
	// KeyRetiring signs nothing any more; it is published until the tokens
	// it signed have expired.
	KeyRetiring KeyStatus = "retiring"
)

// PublishedKey is one entry of the hub's key list.
type PublishedKey struct {
	KeyID     string
	PublicKey ed25519.PublicKey
	Status    KeyStatus
}

// KeyList is the hub's signed list of its keys. KeyID names the key that
// signed it, set by SignKeyList.
type KeyList struct {
	Keys     []PublishedKey
	Revoked  []string
	IssuedAt time.Time
	KeyID    string
}

type wireKey struct {
	KeyID     string    `json:"key_id"`
	PublicKey string    `json:"public_key"`
	Status    KeyStatus `json:"status"`
}

type wireKeyList struct {
	Keys     []wireKey `json:"keys"`
	Revoked  []string  `json:"revoked"`
	IssuedAt int64     `json:"issued_at"`
	KeyID    string    `json:"key_id"`
}

// list checks the rules every list the hub signs follows: each key under its
// own id, once; one active key, the signer; revoked ids well formed and not
// listed.
func (w wireKeyList) list() (KeyList, bool) {
	if w.IssuedAt <= 0 || !validKeyID(w.KeyID) {
		return KeyList{}, false
	}
	l := KeyList{IssuedAt: time.Unix(w.IssuedAt, 0).UTC(), KeyID: w.KeyID, Revoked: slices.Clone(w.Revoked)}
	seen := map[string]bool{}
	active := 0
	for _, k := range w.Keys {
		raw, err := base64.StdEncoding.Strict().DecodeString(k.PublicKey)
		if err != nil || len(raw) != ed25519.PublicKeySize || KeyID(raw) != k.KeyID || seen[k.KeyID] {
			return KeyList{}, false
		}
		switch k.Status {
		case KeyActive:
			if k.KeyID != w.KeyID {
				return KeyList{}, false
			}
			active++
		case KeyNext, KeyRetiring:
		default:
			return KeyList{}, false
		}
		seen[k.KeyID] = true
		l.Keys = append(l.Keys, PublishedKey{KeyID: k.KeyID, PublicKey: raw, Status: k.Status})
	}
	if active != 1 {
		return KeyList{}, false
	}
	for _, id := range w.Revoked {
		if !validKeyID(id) || seen[id] {
			return KeyList{}, false
		}
	}
	return l, true
}

// SignKeyList encodes and signs the hub's key list with its active key. The
// signer's id is the key's, whatever l.KeyID says, and it must be listed as
// the active key. Times are truncated to the second.
func SignKeyList(priv ed25519.PrivateKey, l KeyList) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", ErrInvalidKey
	}
	w := wireKeyList{Keys: []wireKey{}, Revoked: []string{}, KeyID: KeyID(priv.Public().(ed25519.PublicKey))}
	if !l.IssuedAt.IsZero() {
		w.IssuedAt = l.IssuedAt.Unix()
	}
	for _, k := range l.Keys {
		w.Keys = append(w.Keys, wireKey{KeyID: k.KeyID, PublicKey: base64.StdEncoding.EncodeToString(k.PublicKey), Status: k.Status})
	}
	w.Revoked = append(w.Revoked, l.Revoked...)
	if _, ok := w.list(); !ok {
		return "", ErrMalformedKeyList
	}
	payload, err := json.Marshal(w)
	if err != nil {
		return "", err
	}
	return b64.EncodeToString(payload) + "." + b64.EncodeToString(ed25519.Sign(priv, payload)), nil
}

// VerifyKeyList checks the hub's key list against the keys the verifier
// already trusts: one signed by an unknown or revoked key is refused, so
// only a trusted key can introduce the next one.
func VerifyKeyList(trusted KeySet, doc string) (KeyList, error) {
	payload, sig, err := splitSigned(doc)
	if err != nil {
		return KeyList{}, ErrMalformedKeyList
	}
	var w wireKeyList
	if err := decodeStrict(payload, &w); err != nil {
		return KeyList{}, ErrMalformedKeyList
	}
	pub, err := trusted.lookup(w.KeyID)
	if err != nil {
		return KeyList{}, err
	}
	if !ed25519.Verify(pub, payload, sig) {
		return KeyList{}, ErrBadSignature
	}
	l, ok := w.list()
	if !ok {
		return KeyList{}, ErrMalformedKeyList
	}
	return l, nil
}
