import nacl from 'tweetnacl';

import { decodeBase64, decodeBase64Url, utf8 } from './base64';

// The hub public keys this app trusts to sign entitlement tokens, for the
// signing key rotation (docs/licensing-key-rotation.md): the keys embedded in
// the build, plus those learned from the hub's signed key list, minus the
// revoked key ids, which stay revoked for good. A list is learned only when
// a key already trusted signed it, so nobody can slip a key in. Wire format,
// from internal/entitlement/keys.go:
//   base64url(payload JSON) "." base64url(Ed25519 signature of those bytes)
// unpadded; the payload is
// {"keys":[{"key_id","public_key","status"}],"revoked":[key ids],"issued_at","key_id"}
// with public_key in standard base64 and the last key_id the signer's, the
// one key listed as active.

/** A trusted key; publicKey in standard base64, as the Go core takes it. */
export type TrustedKey = { keyId: string; publicKey: string };
export type KeyRing = { keys: TrustedKey[]; revoked: string[] };
export type KeyStatus = 'active' | 'next' | 'retiring';
export type KeyList = {
  keys: (TrustedKey & { status: KeyStatus })[];
  revoked: string[];
  /** Unix seconds. */
  issuedAt: number;
  /** The key that signed the list. */
  keyId: string;
};

export type LookupError = 'unknown_key' | 'revoked_key';
export type KeyListError = 'malformed' | 'bad_signature' | LookupError;

const KEY_ID = /^[0-9a-f]{16}$/;
const STATUSES: readonly string[] = ['active', 'next', 'retiring'];

function rawKey(b64: string): Uint8Array | null {
  const k = decodeBase64(b64);
  return k && k.length === nacl.sign.publicKeyLength ? k : null;
}

/** A ring of these keys, without the revoked ones, the invalid ones or duplicates. */
export function makeRing(keys: readonly TrustedKey[], revoked: readonly string[] = []): KeyRing {
  const gone = [...new Set(revoked.filter((id) => KEY_ID.test(id)))].sort();
  const out: TrustedKey[] = [];
  for (const k of keys) {
    if (!KEY_ID.test(k.keyId) || !rawKey(k.publicKey) || gone.includes(k.keyId) || out.some((o) => o.keyId === k.keyId)) continue;
    out.push({ keyId: k.keyId, publicKey: k.publicKey });
  }
  return { keys: out, revoked: gone };
}

/** The trusted key with this id. */
export function lookup(ring: KeyRing, keyId: string): { ok: true; key: Uint8Array } | { ok: false; error: LookupError } {
  if (ring.revoked.includes(keyId)) return { ok: false, error: 'revoked_key' };
  const k = ring.keys.find((x) => x.keyId === keyId);
  const raw = k ? rawKey(k.publicKey) : null;
  return raw ? { ok: true, key: raw } : { ok: false, error: 'unknown_key' };
}

function parseList(payload: Uint8Array): KeyList | null {
  const text = utf8(payload);
  if (text === null) return null;
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    return null;
  }
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return null;
  const o = raw as Record<string, unknown>;
  const fields = Object.keys(o);
  if (fields.length !== 4 || !['keys', 'revoked', 'issued_at', 'key_id'].every((f) => fields.includes(f))) return null;
  const { keys, revoked, issued_at, key_id } = o;
  if (!Array.isArray(keys) || !Array.isArray(revoked) || typeof key_id !== 'string' || !KEY_ID.test(key_id)) return null;
  if (!Number.isSafeInteger(issued_at) || (issued_at as number) <= 0) return null;
  const list: KeyList = { keys: [], revoked: [], issuedAt: issued_at as number, keyId: key_id };
  let active = 0;
  for (const k of keys as unknown[]) {
    if (!k || typeof k !== 'object' || Array.isArray(k)) return null;
    const e = k as Record<string, unknown>;
    if (Object.keys(e).length !== 3) return null;
    const { key_id: id, public_key: pub, status } = e;
    if (typeof id !== 'string' || !KEY_ID.test(id) || typeof pub !== 'string' || !rawKey(pub)) return null;
    if (typeof status !== 'string' || !STATUSES.includes(status) || list.keys.some((x) => x.keyId === id)) return null;
    if (status === 'active') {
      if (id !== key_id) return null;
      active++;
    }
    list.keys.push({ keyId: id, publicKey: pub, status: status as KeyStatus });
  }
  if (active !== 1) return null;
  for (const id of revoked as unknown[]) {
    if (typeof id !== 'string' || !KEY_ID.test(id) || list.keys.some((x) => x.keyId === id)) return null;
    list.revoked.push(id);
  }
  return list;
}

/** Checks the hub's signed key list against the keys already trusted. */
export function verifyKeyList(ring: KeyRing, doc: string): { ok: true; list: KeyList } | { ok: false; error: KeyListError } {
  const parts = doc.split('.');
  if (parts.length !== 2 || !parts[0] || !parts[1]) return { ok: false, error: 'malformed' };
  const payload = decodeBase64Url(parts[0]);
  const signature = decodeBase64Url(parts[1]);
  if (!payload || !signature || signature.length !== nacl.sign.signatureLength) return { ok: false, error: 'malformed' };
  const list = parseList(payload);
  if (!list) return { ok: false, error: 'malformed' };
  const signer = lookup(ring, list.keyId);
  if (!signer.ok) return signer;
  if (!nacl.sign.detached.verify(payload, signature, signer.key)) return { ok: false, error: 'bad_signature' };
  return { ok: true, list };
}

/**
 * The ring once a verified list is learned: the embedded keys, the list's
 * keys (the previously learned ones it no longer lists are dropped), and
 * every revocation known so far, which no later list undoes.
 */
export function learn(embedded: readonly TrustedKey[], ring: KeyRing, list: KeyList): KeyRing {
  return makeRing([...embedded, ...list.keys.map(({ keyId, publicKey }) => ({ keyId, publicKey }))], [...ring.revoked, ...list.revoked]);
}

/** The ring as the Go core's SetPublicKeys takes it: keys, then "revoked:<id>". */
export function coreKeyList(ring: KeyRing): string {
  return [...ring.keys.map((k) => k.publicKey), ...ring.revoked.map((id) => `revoked:${id}`)].join(',');
}
