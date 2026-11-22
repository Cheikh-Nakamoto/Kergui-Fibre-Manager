import nacl from 'tweetnacl';

import { decodeBase64Url, utf8 } from './base64';
import type { PaidTier, Tier } from './gate';
import { lookup, type KeyRing, type LookupError } from './keyring';

// The hub's entitlement token, verified offline (RFC-006, Entitlement
// token). Wire format, from internal/entitlement/token.go (shared by the hub
// and the Go core, which checks the same token again before a paid action):
//   base64url(payload JSON) "." base64url(Ed25519 signature of those bytes)
// unpadded; the payload is
// {"account","box_id","tier","expires_at","issued_at","key_id"} with
// Unix-second times and key_id naming the signing key (16 hex digits). The
// rules below mirror entitlement.Verify: whatever the hub would not sign is
// refused even with a valid signature.

/** A token is honoured this long past its expiry, for a phone offline. */
export const OFFLINE_GRACE_S = 3 * 24 * 3600;
/** The hub never signs a token living longer than this. */
export const TOKEN_MAX_LIFETIME_S = 30 * 24 * 3600;

export type Claims = {
  account: string;
  boxId: string;
  tier: PaidTier;
  /** Unix seconds. */
  expiresAt: number;
  /** Unix seconds. */
  issuedAt: number;
  /** The signing key's id: first 8 bytes of its SHA-256, hex. */
  keyId: string;
};

/**
 * unknown_key: signed by a key this app does not trust (yet); revoked_key:
 * by a key the hub revoked, so a fresh token is needed (key rotation).
 */
export type VerifyError = 'invalid_key' | 'malformed' | 'bad_signature' | 'expired' | LookupError;
export type VerifyResult = { ok: true; claims: Claims; inGrace: boolean } | { ok: false; error: VerifyError };

const FIELDS = ['account', 'box_id', 'tier', 'expires_at', 'issued_at', 'key_id'] as const;
const KEY_ID = /^[0-9a-f]{16}$/;

function parseClaims(payload: Uint8Array): Claims | null {
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
  const keys = Object.keys(o);
  if (keys.length !== FIELDS.length || !FIELDS.every((f) => keys.includes(f))) return null;
  const { account, box_id, tier, expires_at, issued_at, key_id } = o;
  if (typeof account !== 'string' || typeof box_id !== 'string' || typeof tier !== 'string') return null;
  if (typeof key_id !== 'string' || !KEY_ID.test(key_id)) return null;
  if (!Number.isSafeInteger(expires_at) || !Number.isSafeInteger(issued_at)) return null;
  const c: Claims = {
    account,
    boxId: box_id,
    tier: tier as PaidTier,
    expiresAt: expires_at as number,
    issuedAt: issued_at as number,
    keyId: key_id,
  };
  const life = c.expiresAt - c.issuedAt;
  const valid =
    c.account !== '' && c.boxId !== '' && (c.tier === 'basic' || c.tier === 'max') && life > 0 && life <= TOKEN_MAX_LIFETIME_S;
  return valid ? c : null;
}

/** Splits and parses a token without checking its signature. */
export function decodeToken(token: string): { payload: Uint8Array; signature: Uint8Array; claims: Claims } | null {
  const parts = token.split('.');
  if (parts.length !== 2 || !parts[0] || !parts[1]) return null;
  const payload = decodeBase64Url(parts[0]);
  const signature = decodeBase64Url(parts[1]);
  if (!payload || !signature || signature.length !== nacl.sign.signatureLength) return null;
  const claims = parseClaims(payload);
  return claims ? { payload, signature, claims } : null;
}

/** The key id a payload names, read before the signature is checked. */
function signerOf(payload: Uint8Array): string | null {
  const text = utf8(payload);
  if (text === null) return null;
  try {
    const o = JSON.parse(text) as unknown;
    const id = o && typeof o === 'object' && !Array.isArray(o) ? (o as Record<string, unknown>).key_id : undefined;
    return typeof id === 'string' && KEY_ID.test(id) ? id : null;
  } catch {
    return null;
  }
}

/**
 * Checks a token against the trusted keys at `nowMs`, with the key its
 * key_id names (a revoked one is refused). `inGrace` is true once expires_at
 * has passed but the 3-day grace has not: keep the tier and refresh.
 * Issuance is not checked against now, so a phone whose clock runs behind is
 * not locked out. The caller still compares boxId with its box.
 */
export function verifyToken(keys: KeyRing, token: string, nowMs: number): VerifyResult {
  // No key at all: the build has none (a revoked one still answers revoked_key).
  if (keys.keys.length === 0 && keys.revoked.length === 0) return { ok: false, error: 'invalid_key' };
  const parts = token.split('.');
  if (parts.length !== 2 || !parts[0] || !parts[1]) return { ok: false, error: 'malformed' };
  const payload = decodeBase64Url(parts[0]);
  const signature = decodeBase64Url(parts[1]);
  if (!payload || !signature || signature.length !== nacl.sign.signatureLength) return { ok: false, error: 'malformed' };
  const keyId = signerOf(payload);
  if (!keyId) return { ok: false, error: 'malformed' };
  const key = lookup(keys, keyId);
  if (!key.ok) return key;
  if (!nacl.sign.detached.verify(payload, signature, key.key)) return { ok: false, error: 'bad_signature' };
  const claims = parseClaims(payload);
  if (!claims) return { ok: false, error: 'malformed' };
  const now = Math.floor(nowMs / 1000);
  if (now >= claims.expiresAt + OFFLINE_GRACE_S) return { ok: false, error: 'expired' };
  return { ok: true, claims, inGrace: now >= claims.expiresAt };
}

/**
 * The tier verified claims give on `boxId` at `nowMs`: Free on any other box,
 * on a box whose serial is unknown or unsupported (empty), and once the
 * grace is over.
 */
export function tierAt(claims: Claims | null, boxId: string | null, nowMs: number): Tier {
  if (!claims || !boxId) return 'free';
  if (claims.boxId !== boxId.trim().toUpperCase()) return 'free';
  if (Math.floor(nowMs / 1000) >= claims.expiresAt + OFFLINE_GRACE_S) return 'free';
  return claims.tier;
}

export type Accepted =
  | { kind: 'paid'; token: string; claims: Claims; inGrace: boolean }
  | { kind: 'free' }
  | { kind: 'rejected'; reason: VerifyError | 'other_box' };

/**
 * What to keep from the hub's POST /token answer on `boxId`: nothing is
 * trusted that does not verify offline with a key the app trusts.
 */
export function acceptAnswer(
  answer: { token?: string; boxId?: string; tier?: string },
  ctx: { keys: KeyRing; boxId: string; nowMs: number },
): Accepted {
  if (!answer.token) return { kind: 'free' };
  const r = verifyToken(ctx.keys, answer.token, ctx.nowMs);
  if (!r.ok) return { kind: 'rejected', reason: r.error };
  if (r.claims.boxId !== ctx.boxId.trim().toUpperCase()) return { kind: 'rejected', reason: 'other_box' };
  return { kind: 'paid', token: answer.token, claims: r.claims, inGrace: r.inGrace };
}
