/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Recovery of a lost owner phone (issue #48): the owner's core signs a
// co-owner in or out; a co-owner's core takes the household over and the
// hub stores the new owner's bundle; the new phone's core proves the box
// password to the relay at home, and its bundle is stored only for the
// signed-in household. The core's refusals become reasons the screen words.
jest.mock('../licensing/household', () => ({
  withHousehold: (run: (tok: string, id: string) => Promise<unknown>) => run('tok', 'T1'),
}));
const mockHub = { trustBundle: jest.fn(), putTrustBundle: jest.fn() };
jest.mock('../licensing/hub', () => ({
  get hub() {
    return mockHub;
  },
}));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test' } }));

import KerguiCore from '../../modules/kergui-core';
import { recoveryCore, recoveryFailure, RecoveryError } from '../core/recovery';
import { DEMO_RELAY_ADDRESS, demoFindRelays, isCoOwner, loadRecovery, recovery, useDemoRecovery } from '../features/recovery';
import { recoveryErrorMessage } from '../ui/Recovery';

type CoreFn = 'note' | 'remoteRelays' | 'signTrustBundle' | 'takeOverHousehold' | 'recoverAtHome' | 'findRelaysAtHome';
const core = KerguiCore as unknown as Record<CoreFn, jest.Mock>;
const BUNDLE = { payload: 'UA==', sig: 'Uw==', version: 3 };
const NEXT = { payload: 'Tg==', sig: 'Tg==', version: 4 };
const AWA = { membershipId: 'M1', userId: 'U1', role: 'admin' as const, phone: '+221772222222', keys: { keyId: 'k-awa', ed25519: 'RUQ=', x25519: 'WA==' } };
const answer = (tenantId = 'T1') => JSON.stringify({ tenantId, bundle: NEXT, revoked: 'k-old' });

beforeEach(() => {
  for (const m of Object.values(mockHub)) m.mockReset();
  for (const k of ['remoteRelays', 'signTrustBundle', 'takeOverHousehold', 'recoverAtHome', 'findRelaysAtHome'] as CoreFn[]) core[k].mockReset();
  mockHub.trustBundle.mockResolvedValue(BUNDLE);
  mockHub.putTrustBundle.mockResolvedValue(undefined);
  core.signTrustBundle.mockResolvedValue(JSON.stringify(NEXT));
  useDemoRecovery.setState({ coOwners: {}, recovered: false });
});

test('the owner signs a co-owner in, and out, and the hub stores each bundle', async () => {
  await recovery.designate(AWA);
  expect(core.signTrustBundle).toHaveBeenLastCalledWith(
    'T1',
    JSON.stringify(BUNDLE),
    JSON.stringify({ coOwner: { ed25519: 'RUQ=', x25519: 'WA==', label: '+221772222222' } }),
  );
  expect(mockHub.putTrustBundle).toHaveBeenLastCalledWith('tok', 'T1', NEXT);
  await recovery.remove('k-awa');
  expect(core.signTrustBundle).toHaveBeenLastCalledWith('T1', JSON.stringify(BUNDLE), JSON.stringify({ removeCoOwner: 'k-awa' }));
  await expect(recovery.designate({ ...AWA, keys: undefined })).rejects.toThrow('not published a key');
});

test('the owner’s view lists the co-owners; a member’s says whether it is one', async () => {
  core.remoteRelays.mockResolvedValue(
    JSON.stringify({ owner: true, relays: [{ agentId: 'A1', kind: 'pc' }], delegations: [], coOwners: [{ keyId: 'k-awa', label: 'Awa' }, { label: 'no key' }] }),
  );
  const v = await loadRecovery();
  expect(v).toEqual({ owner: true, hasRelay: true, coOwners: [{ keyId: 'k-awa', label: 'Awa' }], member: null });
  expect(isCoOwner(AWA, v)).toBe(true);
  expect(isCoOwner({ ...AWA, keys: { ...AWA.keys, keyId: 'k-other' } }, v)).toBe(false);

  core.remoteRelays.mockResolvedValue(
    JSON.stringify({ owner: false, relays: [], delegations: [], coOwners: [{ keyId: 'x' }], member: { keyId: 'k-awa', state: 'none', coOwner: true } }),
  );
  const m = await loadRecovery();
  expect(m.coOwners).toEqual([]);
  expect(m.member?.coOwner).toBe(true);

  mockHub.trustBundle.mockResolvedValue(null);
  expect(await loadRecovery()).toEqual({ owner: false, hasRelay: false, coOwners: [], member: null });
});

test('a co-owner takes the household over: the core signs on the hub’s bundle, the hub stores the new one', async () => {
  core.takeOverHousehold.mockResolvedValue(answer());
  const r = await recovery.takeOver();
  expect(core.takeOverHousehold).toHaveBeenCalledWith('T1', JSON.stringify(BUNDLE));
  expect(r).toEqual({ tenantId: 'T1', bundle: NEXT, revoked: 'k-old' });
  expect(mockHub.putTrustBundle).toHaveBeenCalledWith('tok', 'T1', NEXT);

  core.takeOverHousehold.mockRejectedValue(new Error('not_co_owner: trust: this phone is not a co-owner'));
  await expect(recovery.takeOver()).rejects.toMatchObject({ reason: 'not_co_owner' });
});

test('at home: the core talks to the relay, and the bundle goes to the signed-in household only', async () => {
  core.recoverAtHome.mockResolvedValue(answer());
  await recovery.atHome('192.168.1.20', 'Sene-Gal-2026');
  expect(core.recoverAtHome).toHaveBeenCalledWith('192.168.1.20', 'Sene-Gal-2026');
  expect(mockHub.putTrustBundle).toHaveBeenCalledWith('tok', 'T1', NEXT);

  mockHub.putTrustBundle.mockClear();
  core.recoverAtHome.mockResolvedValue(answer('T-other'));
  await expect(recovery.atHome('192.168.1.20', 'x')).rejects.toMatchObject({ reason: 'other_household' });
  expect(mockHub.putTrustBundle).not.toHaveBeenCalled();
});

test('the core’s refusals become reasons, and the reasons sentences', async () => {
  const cases: [string, string][] = [
    ['recovery_not_lan: "203.0.113.7" is not an address on the home network', 'not_lan'],
    ['recovery_proof: the relay answered 403', 'proof'],
    ['recovery_rate_limited: the relay answered 429', 'rate_limited'],
    ['recovery_not_ready: the relay answered 409', 'not_ready'],
    ['recovery_unreachable: dial tcp: connection refused', 'unreachable'],
    ['recovery_already_owner: this phone already owns the household', 'already_owner'],
    ['something else', 'other'],
  ];
  for (const [m, reason] of cases) {
    expect(recoveryFailure(m)).toBe(reason);
    core.recoverAtHome.mockRejectedValueOnce(new Error(m));
    await expect(recoveryCore.atHome('192.168.1.20', 'p')).rejects.toMatchObject({ reason });
  }
  expect(recoveryErrorMessage(new RecoveryError('proof', ''))).toBe('Ce n’est pas le mot de passe de la box.');
  // The core's own text goes to the journal, not to the screen (#119).
  expect(recoveryErrorMessage(new RecoveryError('other', 'boom'))).not.toContain('boom');
  expect(core.note).toHaveBeenCalledWith('warn', expect.stringContaining('boom'));
  core.recoverAtHome.mockResolvedValueOnce(JSON.stringify({ tenantId: 'T1', bundle: { payload: 'x' } }));
  await expect(recoveryCore.atHome('192.168.1.20', 'p')).rejects.toMatchObject({ reason: 'other' });
});

test('the demo checks a home address and the demo box’s password, in memory', () => {
  const demo = useDemoRecovery.getState();
  expect(() => demo.recover('203.0.113.7', 'admin')).toThrow(RecoveryError);
  expect(() => demo.recover('192.168.1.20', 'guess')).toThrow(expect.objectContaining({ reason: 'proof' }));
  expect(useDemoRecovery.getState().recovered).toBe(false);
  demo.recover('192.168.1.20', 'admin');
  expect(useDemoRecovery.getState().recovered).toBe(true);
  expect(core.recoverAtHome).not.toHaveBeenCalled();
});

// « Récupérer à la maison » finds the relay by itself (#117): the core asks
// the home Wi-Fi with the signed-in household and the hub's bundle, and
// hands back the household's relays, the best first.
test('the relay is looked for with the signed-in household and the hub’s bundle', async () => {
  core.findRelaysAtHome.mockResolvedValue(
    JSON.stringify({
      relays: [
        { address: '192.168.1.20', agentId: 'A1', version: 4 },
        { address: '192.168.1.31', agentId: 'A2', version: 3 },
      ],
      others: 1,
    }),
  );
  const found = await recovery.findRelays();
  expect(core.findRelaysAtHome).toHaveBeenCalledWith('T1', JSON.stringify(BUNDLE));
  expect(found).toEqual({
    relays: [
      { address: '192.168.1.20', agentId: 'A1', version: 4 },
      { address: '192.168.1.31', agentId: 'A2', version: 3 },
    ],
    others: 1,
  });

  // No bundle on the hub: the household alone filters.
  mockHub.trustBundle.mockResolvedValue(null);
  core.findRelaysAtHome.mockResolvedValue(JSON.stringify({ relays: [], others: 0 }));
  expect(await recovery.findRelays()).toEqual({ relays: [], others: 0 });
  expect(core.findRelaysAtHome).toHaveBeenLastCalledWith('T1', '');
});

test('a malformed answer of the core finds nothing rather than a bad address', async () => {
  core.findRelaysAtHome.mockResolvedValueOnce(JSON.stringify({ relays: [{ agentId: 'A1' }, { address: '192.168.1.20', agentId: 'A2', version: 2 }], others: 'x' }));
  expect(await recoveryCore.findRelays('T1', null)).toEqual({ relays: [{ address: '192.168.1.20', agentId: 'A2', version: 2 }], others: 0 });
  core.findRelaysAtHome.mockResolvedValueOnce('not json');
  await expect(recoveryCore.findRelays('T1', null)).rejects.toMatchObject({ reason: 'other' });
});

test('the demo finds the demo relay, without the core', async () => {
  const found = await demoFindRelays(0);
  expect(found.relays[0]?.address).toBe(DEMO_RELAY_ADDRESS);
  expect(core.findRelaysAtHome).not.toHaveBeenCalled();
});
