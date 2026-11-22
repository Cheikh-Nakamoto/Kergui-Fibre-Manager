import { createPrivateKey, createPublicKey, sign as nodeSign } from 'crypto';

import { coreKeyList, learn, lookup, makeRing, verifyKeyList, type TrustedKey } from '../licensing/keyring';

// The hub's signed key list (signing key rotation, issue #39). GO_LIST is
// signed by the hub's Go code (entitlement.SignKeyList) with the key whose
// seed is bytes 1..32, listing it active and the key with seed bytes 33..64
// as next, and revoking 0123456789abcdef: it pins the wire format to the
// hub's (internal/entitlement TestAppKeyListFixture signs the same one).
const GO: TrustedKey = { keyId: '65b60673d6ed884b', publicKey: 'ebVWLo/mVPlAeLES6KmLp5AfhTrmlb7X4OORC60ElmQ=' };
const NEXT: TrustedKey = { keyId: 'c945cbf2a5602002', publicKey: '5/FioQvsVZr+oZXk3OhLaVaNXSywlj60RsBoXisX8vA=' };
const GO_LIST =
  'eyJrZXlzIjpbeyJrZXlfaWQiOiI2NWI2MDY3M2Q2ZWQ4ODRiIiwicHVibGljX2tleSI6ImViVldMby9tVlBsQWVMRVM2S21McDVBZmhUcm1sYjdYNE9PUkM2MEVsbVE9Iiwic3RhdHVzIjoiYWN0aXZlIn0seyJrZXlfaWQiOiJjOTQ1Y2JmMmE1NjAyMDAyIiwicHVibGljX2tleSI6IjUvRmlvUXZzVlpyK29aWGszT2hMYVZhTlhTeXdsajYwUnNCb1hpc1g4dkE9Iiwic3RhdHVzIjoibmV4dCJ9XSwicmV2b2tlZCI6WyIwMTIzNDU2Nzg5YWJjZGVmIl0sImlzc3VlZF9hdCI6MTc5NTAwMDAwMCwia2V5X2lkIjoiNjViNjA2NzNkNmVkODg0YiJ9.DnTfNZdBZNDJox2Mb43qC9IG952Q8eBRc4lMKCyilNEZO5NTnIK9Zb-w8lsQiTTCAKZmneqku-Cmj248CHkUDA';
const IAT = 1795000000;

// The Ed25519 key with seed bytes from..from+31, as the Go tests make it.
function seedKey(from: number) {
  const seed = Buffer.from(Array.from({ length: 32 }, (_, i) => from + i));
  const priv = createPrivateKey({ key: Buffer.concat([Buffer.from('302e020100300506032b657004220420', 'hex'), seed]), format: 'der', type: 'pkcs8' });
  const pub = createPublicKey(priv).export({ format: 'der', type: 'spki' }).subarray(12).toString('base64');
  return { priv, pub };
}
const nextPriv = seedKey(33).priv;
const goPriv = seedKey(1).priv;

function signList(payload: unknown, priv = goPriv): string {
  const p = Buffer.from(typeof payload === 'string' ? payload : JSON.stringify(payload));
  return `${p.toString('base64url')}.${nodeSign(null, p, priv).toString('base64url')}`;
}
const wire = (k: TrustedKey, status: string) => ({ key_id: k.keyId, public_key: k.publicKey, status });

test('the test keys are the Go fixture keys', () => {
  expect(seedKey(1).pub).toBe(GO.publicKey);
  expect(seedKey(33).pub).toBe(NEXT.publicKey);
});

describe('verifyKeyList', () => {
  test('accepts the hub-signed list with a key the app already trusts', () => {
    expect(verifyKeyList(makeRing([GO]), GO_LIST)).toEqual({
      ok: true,
      list: { keys: [{ ...GO, status: 'active' }, { ...NEXT, status: 'next' }], revoked: ['0123456789abcdef'], issuedAt: IAT, keyId: GO.keyId },
    });
  });

  test('a list signed by a key the app does not trust, or a revoked one, teaches nothing', () => {
    expect(verifyKeyList(makeRing([NEXT]), GO_LIST)).toEqual({ ok: false, error: 'unknown_key' });
    expect(verifyKeyList(makeRing([GO, NEXT], [GO.keyId]), GO_LIST)).toEqual({ ok: false, error: 'revoked_key' });
    // Another key filed under the signer's id does not verify it.
    expect(verifyKeyList(makeRing([{ keyId: GO.keyId, publicKey: NEXT.publicKey }]), GO_LIST)).toEqual({ ok: false, error: 'bad_signature' });
  });

  test('refuses a tampered list and malformed shapes', () => {
    const [p, s] = GO_LIST.split('.');
    const tampered = Buffer.from(p, 'base64url').toString().replace('"next"', '"retiring"');
    expect(verifyKeyList(makeRing([GO]), `${Buffer.from(tampered).toString('base64url')}.${s}`)).toEqual({ ok: false, error: 'bad_signature' });
    for (const doc of ['', 'x', `${p}.`, `${p}.${s}.x`, `${p}=.${s}`]) {
      expect(verifyKeyList(makeRing([GO]), doc)).toEqual({ ok: false, error: 'malformed' });
    }
  });

  test('a validly signed list is still refused when the hub could not have signed it', () => {
    const good = { keys: [wire(GO, 'active'), wire(NEXT, 'next')], revoked: [], issued_at: IAT, key_id: GO.keyId };
    expect(verifyKeyList(makeRing([GO]), signList(good))).toMatchObject({ ok: true });
    for (const bad of [
      { ...good, extra: 1 },
      { ...good, keys: [wire(GO, 'active'), wire(NEXT, 'active')] },
      { ...good, keys: [wire(GO, 'retiring'), wire(NEXT, 'next')] },
      { ...good, keys: [wire(GO, 'active'), wire(NEXT, 'revoked')] },
      { ...good, keys: [wire(GO, 'active'), wire(GO, 'next')] },
      { ...good, keys: [wire(GO, 'active'), { ...wire(NEXT, 'next'), public_key: 'AQID' }] },
      { ...good, keys: [wire(GO, 'active'), { ...wire(NEXT, 'next'), extra: 1 }] },
      { ...good, revoked: [NEXT.keyId] },
      { ...good, revoked: ['XYZ'] },
      { ...good, issued_at: 0 },
      { keys: good.keys, revoked: [], issued_at: IAT },
      [good],
      'null',
    ]) {
      expect(verifyKeyList(makeRing([GO]), signList(bad))).toEqual({ ok: false, error: 'malformed' });
    }
  });
});

describe('learn', () => {
  test('the next key is learned; then a list it signs revokes the old one, for good', () => {
    const embedded = [GO];
    const v1 = verifyKeyList(makeRing(embedded), GO_LIST);
    if (!v1.ok) throw new Error(v1.error);
    let ring = learn(embedded, makeRing(embedded), v1.list);
    expect(ring).toEqual({ keys: [GO, NEXT], revoked: ['0123456789abcdef'] });

    // The new key is active and revokes the old one: the list it signs is
    // learned because the app learned that key first.
    const v2 = verifyKeyList(ring, signList({ keys: [wire(NEXT, 'active')], revoked: [GO.keyId], issued_at: IAT + 86400, key_id: NEXT.keyId }, nextPriv));
    if (!v2.ok) throw new Error(v2.error);
    ring = learn(embedded, ring, v2.list);
    expect(ring).toEqual({ keys: [NEXT], revoked: ['0123456789abcdef', GO.keyId] });
    expect(lookup(ring, GO.keyId)).toEqual({ ok: false, error: 'revoked_key' });

    // A replay of the first list cannot even be verified any more.
    expect(verifyKeyList(ring, GO_LIST)).toEqual({ ok: false, error: 'revoked_key' });
    // And learning it anyway would not bring the old key back.
    expect(learn(embedded, ring, v1.list).keys).toEqual([NEXT]);
  });

  test('a learned key the hub no longer lists is dropped; an embedded one stays', () => {
    const v = verifyKeyList(makeRing([GO]), signList({ keys: [wire(GO, 'active')], revoked: [], issued_at: IAT, key_id: GO.keyId }));
    if (!v.ok) throw new Error(v.error);
    expect(learn([GO], makeRing([GO, NEXT]), v.list).keys).toEqual([GO]);
    expect(learn([GO, NEXT], makeRing([GO, NEXT]), v.list).keys).toEqual([GO, NEXT]);
  });
});

test('the Go core gets the keys and the revoked ids in one list', () => {
  expect(coreKeyList(makeRing([GO, NEXT], ['0123456789abcdef']))).toBe(`${GO.publicKey},${NEXT.publicKey},revoked:0123456789abcdef`);
  expect(coreKeyList(makeRing([]))).toBe('');
});

test('makeRing drops invalid, duplicate and revoked keys', () => {
  expect(makeRing([GO, GO, { keyId: 'nope', publicKey: GO.publicKey }, { keyId: '0000000000000000', publicKey: 'AQID' }, NEXT], [NEXT.keyId, 'bad'])).toEqual({
    keys: [GO],
    revoked: [NEXT.keyId],
  });
});
