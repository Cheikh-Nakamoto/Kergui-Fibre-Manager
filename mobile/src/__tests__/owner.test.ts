// The owner's keys through the native bridge (issue #43): the Go core keeps
// them in its vault, and what JS reads of them is the public halves only.
import KerguiCore from '../../modules/kergui-core';
import { OwnerKeyError, owner } from '../core/owner';

const native = KerguiCore as unknown as Record<string, jest.Mock>;

beforeEach(() => {
  for (const k of ['ownerKeys', 'pairingInvite', 'pairingCode', 'signTrustBundle', 'sealCredentials', 'demoRelayRedeem']) native[k].mockReset();
});

test('the owner keys reach JS as public keys only, whatever else the core says', async () => {
  native.ownerKeys.mockResolvedValue(
    JSON.stringify({ keyId: 'k1', ed25519: 'RUQ=', x25519: 'WA==', identity: 'PRIVATE', seed: 'PRIVATE' }),
  );
  const keys = await owner.keys();
  expect(keys).toEqual({ keyId: 'k1', ed25519: 'RUQ=', x25519: 'WA==' });
  expect(JSON.stringify(keys)).not.toContain('PRIVATE');
});

test('the bridge has no way to read a private key out of the core', () => {
  // The only calls the app has on the owner's keys: none returns secrets.
  expect(Object.keys(owner).sort()).toEqual(['demoRelay', 'invite', 'keys', 'pairingCode', 'sealCredentials', 'signBundle']);
  for (const k of Object.keys(KerguiCore)) expect(k).not.toMatch(/private|secret|export|identity|seed/i);
});

test('an answer that is not the keys is an error, not empty keys', async () => {
  native.ownerKeys.mockResolvedValue('{"keyId":""}');
  await expect(owner.keys()).rejects.toBeInstanceOf(OwnerKeyError);
  native.ownerKeys.mockResolvedValue('oops');
  await expect(owner.keys()).rejects.toBeInstanceOf(OwnerKeyError);
});

test('the six digits come from the core and are checked', async () => {
  native.pairingCode.mockResolvedValue('042917');
  await expect(owner.pairingCode('ED', 'X')).resolves.toBe('042917');
  expect(native.pairingCode).toHaveBeenCalledWith('ED', 'X');
  native.pairingCode.mockResolvedValue('42917');
  await expect(owner.pairingCode('ED', 'X')).rejects.toBeInstanceOf(OwnerKeyError);
});

test('signing a bundle passes the current one and the relay to the core', async () => {
  native.signTrustBundle.mockResolvedValue('{"payload":"UA==","sig":"Uw==","version":2}');
  const relay = { agentId: 'A1', kind: 'pc', ed25519: 'RUQ=', x25519: 'WA==' };
  const current = { payload: 'cA==', sig: 'cw==', version: 1 };
  await expect(owner.signBundle('T1', current, { relay })).resolves.toEqual({ payload: 'UA==', sig: 'Uw==', version: 2 });
  expect(native.signTrustBundle).toHaveBeenCalledWith('T1', JSON.stringify(current), JSON.stringify({ relay }));

  await owner.signBundle('T1', null, { routerPin: 'ab' });
  expect(native.signTrustBundle).toHaveBeenLastCalledWith('T1', '', '{"routerPin":"ab"}');

  native.signTrustBundle.mockResolvedValue('{"payload":"UA==","sig":"Uw=="}');
  await expect(owner.signBundle('T1', null)).rejects.toBeInstanceOf(OwnerKeyError);
});

test('the invite is the core’s QR payload', async () => {
  native.pairingInvite.mockResolvedValue('kgp1.ABC.k1');
  await expect(owner.invite('CODE')).resolves.toBe('kgp1.ABC.k1');
  expect(native.pairingInvite).toHaveBeenCalledWith('CODE');
});

test('sealing passes the bundle to the core and reads back ciphertext only', async () => {
  native.sealCredentials.mockResolvedValue(
    JSON.stringify({
      version: 2,
      boxPassword: true,
      password: 'LEAK',
      relays: [{ agentId: 'A1', blob: 'Qk9Y', version: 2, username: 'LEAK' }],
    }),
  );
  const bundle = { payload: 'cA==', sig: 'cw==', version: 3 };
  const sealed = await owner.sealCredentials('T1', bundle, 'A1');
  expect(native.sealCredentials).toHaveBeenCalledWith('T1', JSON.stringify(bundle), 'A1');
  expect(sealed).toEqual({ version: 2, boxPassword: true, relays: [{ agentId: 'A1', blob: 'Qk9Y', version: 2 }] });
  expect(JSON.stringify(sealed)).not.toContain('LEAK');

  await owner.sealCredentials('T1', bundle);
  expect(native.sealCredentials).toHaveBeenLastCalledWith('T1', JSON.stringify(bundle), '');
});

test('no password in the core is said, not an error', async () => {
  native.sealCredentials.mockResolvedValue('{"version":0,"boxPassword":false,"relays":[]}');
  await expect(owner.sealCredentials('T1', { payload: 'p', sig: 's', version: 1 })).resolves.toEqual({
    version: 0,
    boxPassword: false,
    relays: [],
  });
});

test('a sealed answer without blobs or versions is an error', async () => {
  const bundle = { payload: 'p', sig: 's', version: 1 };
  native.sealCredentials.mockResolvedValue('{"version":1,"boxPassword":true,"relays":[{"agentId":"A1","version":1}]}');
  await expect(owner.sealCredentials('T1', bundle)).rejects.toBeInstanceOf(OwnerKeyError);
  native.sealCredentials.mockResolvedValue('{"version":1,"boxPassword":true}');
  await expect(owner.sealCredentials('T1', bundle)).rejects.toBeInstanceOf(OwnerKeyError);
  native.sealCredentials.mockResolvedValue('{"version":1,"boxPassword":true,"relays":[{"agentId":"A1","blob":"Qg=="}]}');
  await expect(owner.sealCredentials('T1', bundle)).rejects.toBeInstanceOf(OwnerKeyError);
});
