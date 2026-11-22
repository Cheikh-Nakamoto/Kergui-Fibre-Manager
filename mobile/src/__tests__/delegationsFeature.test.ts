/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Member delegations (issue #47): the owner's core signs every change into
// the next bundle and the hub stores it; the owner's app renews what ends
// within a week; a member's phone publishes its key, goes through the hub
// once delegated, and signs its own block / unblock — or nothing, with why.
const mockStore = new Map<string, string>();
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async (k: string) => mockStore.get(k) ?? null,
  setItemAsync: async (k: string, v: string) => void mockStore.set(k, v),
  deleteItemAsync: async (k: string) => void mockStore.delete(k),
}));
jest.mock('../licensing/household', () => ({
  withHousehold: (run: (tok: string, id: string) => Promise<unknown>) => run('tok', 'T1'),
}));
const mockHub = {
  trustBundle: jest.fn(),
  putTrustBundle: jest.fn(),
  putMemberKey: jest.fn(),
  relayStatus: jest.fn(),
  deviceCommand: jest.fn(),
  command: jest.fn(),
};
jest.mock('../licensing/hub', () => ({
  get hub() {
    return mockHub;
  },
}));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test' } }));

import KerguiCore from '../../modules/kergui-core';
import { RemoteCommandError } from '../core/remote';
import { delegations, loadDelegations, renewDue, spacedCode, useDemoDelegations } from '../features/delegations';
import { detectRemote, remoteSetBlocked } from '../features/remote';
import { errorMessage } from '../i18n';
import { useAccount } from '../licensing/account';

type CoreFn =
  | 'remoteRelays'
  | 'signTrustBundle'
  | 'renewDelegations'
  | 'memberKeys'
  | 'signMemberCommand'
  | 'signDeviceCommand'
  | 'verifyCommandResult'
  | 'pairingCode'
  | 'request';
const core = KerguiCore as unknown as Record<CoreFn, jest.Mock>;
const BUNDLE = { payload: 'UA==', sig: 'Uw==', version: 3 };
const NEXT = { payload: 'Tg==', sig: 'Tg==', version: 4 };
const AWA = { membershipId: 'M1', userId: 'U1', role: 'admin' as const, phone: '+221772222222', keys: { keyId: 'k-awa', ed25519: 'RUQ=', x25519: 'WA==' } };
const MAC = 'ac:bb:cc:00:00:22';
const SIGNED = { payload: 'Q01E', sig: 'U0lH', cmdId: 'c1', mac: MAC, expiresAt: '2026-11-22T10:01:00Z' };
const ownerView = (over: object = {}) =>
  JSON.stringify({ owner: true, ownerOnly: false, relays: [{ agentId: 'A1', kind: 'pc' }], delegations: [], ...over });
const memberView = (member: object, over: object = {}) =>
  JSON.stringify({ owner: false, ownerOnly: false, relays: [{ agentId: 'A1', kind: 'pc' }], delegations: [], member, ...over });

beforeEach(() => {
  mockStore.clear();
  for (const m of Object.values(mockHub)) m.mockReset();
  for (const k of Object.keys(core) as CoreFn[]) if (typeof core[k]?.mockReset === 'function') core[k].mockReset();
  useAccount.setState({ account: { accountId: 'acc', phone: '+221770000001', sessionToken: 'kgs_x', expiresAt: '' } });
  mockHub.trustBundle.mockResolvedValue(BUNDLE);
  mockHub.putTrustBundle.mockResolvedValue(undefined);
  mockHub.putMemberKey.mockResolvedValue(undefined);
  mockHub.relayStatus.mockResolvedValue({ state: 'active', agentId: 'A1', lastSeenAt: '2026-11-22T09:59:50Z', ageSeconds: 10 });
  core.signTrustBundle.mockResolvedValue(JSON.stringify(NEXT));
  core.renewDelegations.mockResolvedValue('');
  core.remoteRelays.mockResolvedValue(ownerView());
});

describe('the owner signs every change', () => {
  test('grant, revoke and owner-only are signed in the core and stored on the hub', async () => {
    await delegations.grant(AWA, 'control');
    expect(core.signTrustBundle).toHaveBeenLastCalledWith(
      'T1',
      JSON.stringify(BUNDLE),
      JSON.stringify({ delegate: { ed25519: 'RUQ=', x25519: 'WA==', scope: 'control', label: '+221772222222' } }),
    );
    expect(mockHub.putTrustBundle).toHaveBeenLastCalledWith('tok', 'T1', NEXT);

    await delegations.revoke('k-awa');
    expect(core.signTrustBundle).toHaveBeenLastCalledWith('T1', JSON.stringify(BUNDLE), JSON.stringify({ revoke: 'k-awa' }));
    await delegations.setOwnerOnly(true);
    expect(core.signTrustBundle).toHaveBeenLastCalledWith('T1', JSON.stringify(BUNDLE), JSON.stringify({ ownerOnly: true }));
    expect(mockHub.putTrustBundle).toHaveBeenCalledTimes(3);
  });

  test('a member who has not published a key cannot be delegated', async () => {
    await expect(delegations.grant({ ...AWA, keys: undefined }, 'view')).rejects.toThrow(/key/);
    expect(core.signTrustBundle).not.toHaveBeenCalled();
  });

  test('the view reads the delegations from the bundle the core opened', async () => {
    core.remoteRelays.mockResolvedValue(ownerView({ delegations: [{ keyId: 'k-awa', scope: 'view', expiresAt: '2026-12-22T10:00:00Z' }, { keyId: 'x', scope: 'hub_block', expiresAt: 'y' }] }));
    await expect(loadDelegations()).resolves.toEqual({
      owner: true,
      ownerOnly: false,
      hasRelay: true,
      delegations: [{ keyId: 'k-awa', scope: 'view', expiresAt: '2026-12-22T10:00:00Z' }],
      member: null,
    });
    mockHub.trustBundle.mockResolvedValue(null);
    await expect(loadDelegations()).resolves.toMatchObject({ owner: false, hasRelay: false, delegations: [] });
  });
});

describe('automatic renewal', () => {
  test('nothing due: nothing is signed or sent', async () => {
    await expect(renewDue('tok', 'T1', BUNDLE)).resolves.toBe(BUNDLE);
    expect(core.renewDelegations).toHaveBeenCalledWith('T1', JSON.stringify(BUNDLE));
    expect(mockHub.putTrustBundle).not.toHaveBeenCalled();
  });

  test('a delegation due within the week: the renewed bundle is stored and used', async () => {
    core.renewDelegations.mockResolvedValue(JSON.stringify(NEXT));
    await expect(renewDue('tok', 'T1', BUNDLE)).resolves.toEqual(NEXT);
    expect(mockHub.putTrustBundle).toHaveBeenCalledWith('tok', 'T1', NEXT);
  });

  test('a renewal that fails keeps the current bundle, for the next detection to try again', async () => {
    core.renewDelegations.mockResolvedValue(JSON.stringify(NEXT));
    mockHub.putTrustBundle.mockRejectedValue(new Error('offline'));
    await expect(renewDue('tok', 'T1', BUNDLE)).resolves.toBe(BUNDLE);
  });

  test('the owner’s remote-mode detection renews, and goes on with the renewed bundle', async () => {
    core.remoteRelays.mockResolvedValue(ownerView({ delegations: [{ keyId: 'k-awa', scope: 'control', expiresAt: '2026-11-25T10:00:00Z' }] }));
    core.renewDelegations.mockResolvedValue(JSON.stringify(NEXT));
    const h = await detectRemote();
    expect(h).toMatchObject({ tenantId: 'T1', bundle: NEXT });
    expect(h?.member).toBeUndefined();
    expect(mockHub.putTrustBundle).toHaveBeenCalledWith('tok', 'T1', NEXT);
  });

  test('no delegation: the detection does not ask for a renewal', async () => {
    await detectRemote();
    expect(core.renewDelegations).not.toHaveBeenCalled();
  });
});

describe('a member’s phone', () => {
  test('publishes its key once, when the household does not list it', async () => {
    core.memberKeys.mockResolvedValue(JSON.stringify({ keyId: 'k-awa', ed25519: 'RUQ=', x25519: 'WA==' }));
    await expect(delegations.publishKey({ ...AWA, keys: undefined })).resolves.toBe(true);
    expect(mockHub.putMemberKey).toHaveBeenCalledWith('tok', 'T1', { ed25519: 'RUQ=', x25519: 'WA==' });
    await expect(delegations.publishKey(AWA)).resolves.toBe(false);
    expect(mockHub.putMemberKey).toHaveBeenCalledTimes(1);
  });

  test('goes through the hub once delegated, and only then', async () => {
    core.remoteRelays.mockResolvedValue(memberView({ keyId: 'k', state: 'none', control: false, code: '123456' }));
    await expect(detectRemote()).resolves.toBeNull();
    for (const state of ['expired', 'revoked', 'other_owner']) {
      core.remoteRelays.mockResolvedValue(memberView({ keyId: 'k', state, control: false }, { relays: [] }));
      await expect(detectRemote()).resolves.toBeNull();
    }
    core.remoteRelays.mockResolvedValue(memberView({ keyId: 'k', state: 'active', scope: 'view', control: false, expiresAt: 'x' }));
    await expect(detectRemote()).resolves.toMatchObject({ tenantId: 'T1', member: { control: false, ownerOnly: false } });
    core.remoteRelays.mockResolvedValue(memberView({ keyId: 'k', state: 'active', scope: 'control', control: true, expiresAt: 'x' }));
    await expect(detectRemote()).resolves.toMatchObject({ member: { control: true } });
    expect(core.renewDelegations).not.toHaveBeenCalled(); // a member renews nothing
  });

  test('signs its own block with its member key, never the owner’s', async () => {
    core.signMemberCommand.mockResolvedValue(JSON.stringify(SIGNED));
    mockHub.deviceCommand.mockResolvedValue({ commandId: 'c1', status: 'done', result: { payload: 'cg==', sig: 'cw==' } });
    core.verifyCommandResult.mockResolvedValue(JSON.stringify({ agentId: 'A1', status: 200, executedAt: '2026-11-22T10:00:01Z' }));
    const h = { tenantId: 'T1', bundle: BUNDLE, relays: [{ agentId: 'A1', kind: 'pc' }], member: { control: true, ownerOnly: false } };
    await remoteSetBlocked(h, MAC, true, { boxId: 'ZTEG00000001', sleep: async () => undefined });
    expect(core.signMemberCommand).toHaveBeenCalledWith('T1', JSON.stringify(BUNDLE), MAC, 'block', 'ZTEG00000001');
    expect(core.signDeviceCommand).not.toHaveBeenCalled();
  });

  test('without control nothing is signed or sent, and the app says why', async () => {
    const view = { tenantId: 'T1', bundle: BUNDLE, relays: [{ agentId: 'A1', kind: 'pc' }], member: { control: false, ownerOnly: false } };
    const e1 = await remoteSetBlocked(view, MAC, true, { boxId: '' }).catch((e: unknown) => e);
    expect(e1).toEqual(new RemoteCommandError('not_delegated'));
    expect(errorMessage(e1)).toMatch(/ne vous a pas donné le droit de contrôler/);
    const e2 = await remoteSetBlocked({ ...view, member: { control: false, ownerOnly: true } }, MAC, false, { boxId: '' }).catch((e: unknown) => e);
    expect(errorMessage(e2)).toMatch(/réservé au propriétaire/);
    expect(core.signMemberCommand).not.toHaveBeenCalled();
    expect(mockHub.deviceCommand).not.toHaveBeenCalled();
  });

  test('a core that refuses to sign (owner-only since the last detection) is said the same way', async () => {
    core.signMemberCommand.mockRejectedValue(new Error('owner_only: trust: household is owner-only, delegated keys may not act'));
    const h = { tenantId: 'T1', bundle: BUNDLE, relays: [{ agentId: 'A1', kind: 'pc' }], member: { control: true, ownerOnly: false } };
    const e = await remoteSetBlocked(h, MAC, true, { boxId: '' }).catch((x: unknown) => x);
    expect(e).toBeInstanceOf(RemoteCommandError);
    expect((e as RemoteCommandError).reason).toBe('owner_only');
    expect(mockHub.deviceCommand).not.toHaveBeenCalled();
  });
});

test('the demo grants for 30 days, revokes and pauses, in memory', () => {
  const now = Date.parse('2026-11-22T10:00:00Z');
  useDemoDelegations.getState().grant('demo-2', 'control', now);
  expect(useDemoDelegations.getState().byMember['demo-2']).toMatchObject({ scope: 'control', expiresAt: '2026-12-22T10:00:00.000Z' });
  useDemoDelegations.getState().revoke('demo-2');
  expect(useDemoDelegations.getState().byMember['demo-2']).toBeUndefined();
  useDemoDelegations.getState().setOwnerOnly(true);
  expect(useDemoDelegations.getState().ownerOnly).toBe(true);
  expect(spacedCode('482913')).toBe('482 913');
});
