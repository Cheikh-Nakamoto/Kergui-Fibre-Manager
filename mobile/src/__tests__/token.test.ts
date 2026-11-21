import { generateKeyPairSync, sign as nodeSign, type KeyObject } from 'crypto';

import { decodeBase64, decodeBase64Url } from '../licensing/base64';
import { acceptAnswer, decodeToken, OFFLINE_GRACE_S, tierAt, verifyToken, type Claims } from '../licensing/token';

// A token signed by the hub's Go code (entitlement.Sign), with the key whose
// seed is bytes 1..32: it pins the wire format to the hub's, byte for byte
// (internal/entitlement TestAppFixture signs the same one).
const GO_PUB = 'ebVWLo/mVPlAeLES6KmLp5AfhTrmlb7X4OORC60ElmQ=';
const GO_TOKEN =
  'eyJhY2NvdW50IjoiYWNjXzdmM2EiLCJib3hfaWQiOiJaVEVHMDAwMDAwMDEiLCJ0aWVyIjoibWF4IiwiZXhwaXJlc19hdCI6MTc5NzU5MjAwMCwiaXNzdWVkX2F0IjoxNzk1MDAwMDAwLCJrZXlfaWQiOiI2NWI2MDY3M2Q2ZWQ4ODRiIn0.EzDS623Tq9RoLnW0ti8vUARunQt9F8WYt07LUP5AvTCb6EzDcw1fDyloOaQi9fCNlNGJrIZMkQx3iGj7VuSPAQ';
const GO_IAT = 1795000000;
const GO_EXP = GO_IAT + 30 * 86400;
const DAY = 86400;
const at = (unix: number) => unix * 1000;

const goKey = decodeBase64(GO_PUB)!;
const GO_KID = '65b60673d6ed884b';

// Tokens for cases the hub would never sign, made with Node's own Ed25519.
const { publicKey, privateKey } = generateKeyPairSync('ed25519');
const rawPub = (k: KeyObject) => new Uint8Array(k.export({ format: 'der', type: 'spki' }).subarray(12));
const nodeKey = rawPub(publicKey);
const b64url = (b: Buffer) => b.toString('base64url');
function forge(payload: unknown, key = privateKey): string {
  const p = Buffer.from(typeof payload === 'string' ? payload : JSON.stringify(payload));
  return `${b64url(p)}.${b64url(nodeSign(null, p, key))}`;
}
const good = { account: 'acc_1', box_id: 'ZTEG00000001', tier: 'basic', expires_at: GO_EXP, issued_at: GO_IAT, key_id: GO_KID };

describe('base64', () => {
  test('base64url is strict: no padding, no foreign characters, no stray bits', () => {
    expect(Array.from(decodeBase64Url('AQID')!)).toEqual([1, 2, 3]);
    expect(Array.from(decodeBase64Url('-_8')!)).toEqual([0xfb, 0xff]);
    expect(decodeBase64Url('AQI=')).toBeNull();
    expect(decodeBase64Url('AQ+/')).toBeNull();
    expect(decodeBase64Url('A')).toBeNull();
    expect(decodeBase64Url('AR')).toBeNull(); // trailing bits not zero
  });

  test('standard base64 with padding', () => {
    expect(Array.from(decodeBase64('AQI=')!)).toEqual([1, 2]);
    expect(decodeBase64(GO_PUB)!.length).toBe(32);
    expect(decodeBase64('not base64!')).toBeNull();
  });
});

describe('verifyToken', () => {
  test('accepts the hub-signed token and reads its claims', () => {
    const r = verifyToken(goKey, GO_TOKEN, at(GO_IAT + DAY));
    expect(r).toEqual({
      ok: true,
      inGrace: false,
      claims: { account: 'acc_7f3a', boxId: 'ZTEG00000001', tier: 'max', expiresAt: GO_EXP, issuedAt: GO_IAT, keyId: GO_KID },
    });
  });

  test('is in grace for 3 days after expiry, then expired', () => {
    expect(verifyToken(goKey, GO_TOKEN, at(GO_EXP))).toMatchObject({ ok: true, inGrace: true });
    expect(verifyToken(goKey, GO_TOKEN, at(GO_EXP + OFFLINE_GRACE_S - 1))).toMatchObject({ ok: true, inGrace: true });
    expect(verifyToken(goKey, GO_TOKEN, at(GO_EXP + OFFLINE_GRACE_S))).toEqual({ ok: false, error: 'expired' });
  });

  test('a clock running behind the issuance is not locked out', () => {
    expect(verifyToken(goKey, GO_TOKEN, at(GO_IAT - 10 * DAY))).toMatchObject({ ok: true, inGrace: false });
  });

  test('refuses a tampered payload, another key, or a wrong key size', () => {
    const [p, s] = GO_TOKEN.split('.');
    const tampered = Buffer.from(p, 'base64url').toString().replace('"max"', '"bas"');
    expect(verifyToken(goKey, `${b64url(Buffer.from(tampered))}.${s}`, at(GO_IAT))).toEqual({ ok: false, error: 'bad_signature' });
    expect(verifyToken(nodeKey, GO_TOKEN, at(GO_IAT))).toEqual({ ok: false, error: 'bad_signature' });
    expect(verifyToken(goKey.subarray(1), GO_TOKEN, at(GO_IAT))).toEqual({ ok: false, error: 'invalid_key' });
  });

  test('refuses malformed shapes', () => {
    const [p, s] = GO_TOKEN.split('.');
    for (const t of ['', 'abc', `${p}.`, `.${s}`, `${p}.${s}.x`, `${p}=.${s}`, `${p}.${s.slice(0, -2)}`]) {
      expect(verifyToken(goKey, t, at(GO_IAT))).toEqual({ ok: false, error: 'malformed' });
    }
  });

  test('a validly signed payload is still refused when the hub could not have signed it', () => {
    expect(verifyToken(nodeKey, forge(good), at(GO_IAT))).toMatchObject({ ok: true });
    const bad: unknown[] = [
      { ...good, extra: 1 },
      { ...good, tier: 'free' },
      { ...good, tier: 'gold' },
      { ...good, account: '' },
      { ...good, box_id: '' },
      { ...good, expires_at: GO_IAT },
      { ...good, expires_at: GO_IAT + 30 * DAY + 1 },
      { ...good, expires_at: String(GO_EXP) },
      { account: 'acc_1', box_id: 'ZTEG00000001', tier: 'basic', expires_at: GO_EXP },
      // The key id is part of the format: required, 16 lowercase hex digits.
      { account: 'acc_1', box_id: 'ZTEG00000001', tier: 'basic', expires_at: GO_EXP, issued_at: GO_IAT },
      { ...good, key_id: '' },
      { ...good, key_id: 'ABCDEF0123456789' },
      { ...good, key_id: 1 },
      [good],
      'null',
      `${JSON.stringify(good)} {}`,
    ];
    for (const payload of bad) {
      expect(verifyToken(nodeKey, forge(payload), at(GO_IAT))).toEqual({ ok: false, error: 'malformed' });
    }
  });
});

describe('decodeToken', () => {
  test('reads claims without checking the signature', () => {
    expect(decodeToken(GO_TOKEN)?.claims.tier).toBe('max');
    expect(decodeToken('nope')).toBeNull();
  });
});

describe('tierAt', () => {
  const claims: Claims = { account: 'acc_1', boxId: 'ZTEG00000001', tier: 'basic', expiresAt: GO_EXP, issuedAt: GO_IAT, keyId: GO_KID };

  test('the token tier on its own box, through the grace', () => {
    expect(tierAt(claims, 'ZTEG00000001', at(GO_IAT))).toBe('basic');
    expect(tierAt(claims, 'zteg00000001', at(GO_EXP + DAY))).toBe('basic');
    expect(tierAt(claims, 'ZTEG00000001', at(GO_EXP + OFFLINE_GRACE_S))).toBe('free');
  });

  test('free on another box, on an unidentified box, or with no token', () => {
    expect(tierAt(claims, 'ZTEG00000002', at(GO_IAT))).toBe('free');
    expect(tierAt(claims, '', at(GO_IAT))).toBe('free');
    expect(tierAt(claims, null, at(GO_IAT))).toBe('free');
    expect(tierAt(null, 'ZTEG00000001', at(GO_IAT))).toBe('free');
  });
});

describe('acceptAnswer', () => {
  const ctx = { publicKey: goKey, boxId: 'ZTEG00000001', nowMs: at(GO_IAT + DAY) };

  test('a verified token for this box is kept', () => {
    const r = acceptAnswer({ boxId: 'ZTEG00000001', tier: 'max', token: GO_TOKEN }, ctx);
    expect(r).toMatchObject({ kind: 'paid', token: GO_TOKEN, inGrace: false, claims: { tier: 'max', boxId: 'ZTEG00000001' } });
  });

  test('no token means Free', () => {
    expect(acceptAnswer({ boxId: 'ZTEG00000001', tier: 'free' }, ctx)).toEqual({ kind: 'free' });
  });

  test('a token for another box, from another key, or with no key in the build is rejected', () => {
    expect(acceptAnswer({ boxId: 'X', tier: 'max', token: GO_TOKEN }, { ...ctx, boxId: 'ZTEG00000002' })).toEqual({
      kind: 'rejected',
      reason: 'other_box',
    });
    expect(acceptAnswer({ boxId: 'X', tier: 'max', token: GO_TOKEN }, { ...ctx, publicKey: nodeKey })).toEqual({
      kind: 'rejected',
      reason: 'bad_signature',
    });
    expect(acceptAnswer({ boxId: 'X', tier: 'max', token: GO_TOKEN }, { ...ctx, publicKey: null })).toEqual({
      kind: 'rejected',
      reason: 'invalid_key',
    });
  });
});
