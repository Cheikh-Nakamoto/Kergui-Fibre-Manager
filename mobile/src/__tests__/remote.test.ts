/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Remote mode (#46, RFC-005 design rule 1): when this phone's owner key signed
// a bundle naming a relay, the device list and block / unblock go through the
// hub, the core verifies every list and every result, and a relay that is
// not there is said, never replaced by a login to the box.
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
  routerDevices: jest.fn(),
  relayStatus: jest.fn(),
  deviceCommand: jest.fn(),
  command: jest.fn(),
};
jest.mock('../licensing/hub', () => ({
  get hub() {
    return mockHub;
  },
}));
const mockConfig = { hubUrl: 'https://hub.test' };
jest.mock('../licensing/config', () => ({
  get config() {
    return mockConfig;
  },
}));

import KerguiCore from '../../modules/kergui-core';
import { ApiError } from '../core/client';
import { RemoteCommandError, RemoteTrustError } from '../core/remote';
import { ageWords, detectRemote, freshness, remoteDevices, remoteSetBlocked, STALE_AFTER_S, type RemoteHousehold } from '../features/remote';
import { errorMessage } from '../i18n';
import { useAccount } from '../licensing/account';
import { HubError } from '../licensing/api';

const core = KerguiCore as unknown as Record<'note' | 'remoteRelays' | 'openDeviceSnapshot' | 'signDeviceCommand' | 'verifyCommandResult' | 'request', jest.Mock>;
const BUNDLE = { payload: 'UA==', sig: 'Uw==', version: 3 };
const HOUSEHOLD: RemoteHousehold = { tenantId: 'T1', bundle: BUNDLE, relays: [{ agentId: 'A1', kind: 'pc' }] };
const MAC = 'ac:bb:cc:00:00:22';
const SIGNED = { payload: 'Q01E', sig: 'U0lH', cmdId: 'c1', mac: MAC, expiresAt: '2026-11-22T10:01:00Z' };
const T0 = Date.parse('2026-11-22T10:00:00Z');
const opts = (extra: Partial<Parameters<typeof remoteSetBlocked>[3]> = {}) => ({
  boxId: 'ZTEG00000001',
  now: () => T0,
  sleep: async () => undefined,
  ...extra,
});

beforeEach(() => {
  mockStore.clear();
  for (const m of Object.values(mockHub)) m.mockReset();
  for (const k of ['remoteRelays', 'openDeviceSnapshot', 'signDeviceCommand', 'verifyCommandResult', 'request'] as const) core[k].mockReset();
  mockConfig.hubUrl = 'https://hub.test';
  useAccount.setState({ account: { accountId: 'acc', phone: '+221770000001', sessionToken: 'kgs_x', expiresAt: '' } });
  mockHub.trustBundle.mockResolvedValue(BUNDLE);
  mockHub.relayStatus.mockResolvedValue({ state: 'active', agentId: 'A1', lastSeenAt: '2026-11-22T09:59:50Z', ageSeconds: 10 });
  core.remoteRelays.mockResolvedValue(JSON.stringify({ owner: true, relays: [{ agentId: 'A1', kind: 'pc' }] }));
  core.signDeviceCommand.mockResolvedValue(JSON.stringify(SIGNED));
  core.verifyCommandResult.mockResolvedValue(JSON.stringify({ agentId: 'A1', status: 200, executedAt: '2026-11-22T10:00:01Z' }));
});

describe('which mode', () => {
  test('the owner of a bundle naming a relay goes through the hub', async () => {
    await expect(detectRemote()).resolves.toEqual(HOUSEHOLD);
    expect(core.remoteRelays).toHaveBeenCalledWith('T1', JSON.stringify(BUNDLE));
  });

  test('local mode: no hub, not signed in, no bundle, not the owner, no relay', async () => {
    mockConfig.hubUrl = '';
    await expect(detectRemote()).resolves.toBeNull();
    mockConfig.hubUrl = 'https://hub.test';

    useAccount.setState({ account: null });
    await expect(detectRemote()).resolves.toBeNull();
    useAccount.setState({ account: { accountId: 'acc', phone: '+221770000001', sessionToken: 'kgs_x', expiresAt: '' } });

    mockHub.trustBundle.mockResolvedValueOnce(null);
    await expect(detectRemote()).resolves.toBeNull();

    // Another member's phone, with no delegation in the bundle (issue #47).
    core.remoteRelays.mockResolvedValueOnce(JSON.stringify({ owner: false, relays: [] }));
    await expect(detectRemote()).resolves.toBeNull();

    core.remoteRelays.mockResolvedValueOnce(JSON.stringify({ owner: true, relays: [] }));
    await expect(detectRemote()).resolves.toBeNull();
    expect(core.request).not.toHaveBeenCalled();
  });

  test('a hub that cannot be reached keeps a phone that was remote in remote mode', async () => {
    await detectRemote();
    mockHub.trustBundle.mockRejectedValue(new HubError(0, 'NETWORK', 'offline'));
    const h = await detectRemote();
    expect(h).toMatchObject({ tenantId: 'T1', bundle: null });
    // Nothing can be verified: the list says why, the box is not asked.
    await expect(remoteDevices(h!)).rejects.toBeInstanceOf(HubError);
    expect(errorMessage(await remoteDevices(h!).catch((e: unknown) => e))).toMatch(/injoignable/);
    expect(core.request).not.toHaveBeenCalled();
  });

  test('a phone that never was remote stays local when the hub cannot be reached', async () => {
    mockHub.trustBundle.mockRejectedValue(new HubError(0, 'NETWORK', 'offline'));
    await expect(detectRemote()).resolves.toBeNull();
  });
});

describe('the device list', () => {
  test('is the list the relay signed, with its as_of and the relay’s state', async () => {
    const snapshot = { payload: 'cA==', sig: 'cw==' };
    mockHub.routerDevices.mockResolvedValue({ snapshot, agentId: 'A1', asOf: '2026-11-22T09:58:00Z' });
    core.openDeviceSnapshot.mockResolvedValue(
      JSON.stringify({ agentId: 'A1', asOf: '2026-11-22T09:58:00Z', devices: [{ MAC, Hostname: 'Laptop-Bureau', Blocked: false }] }),
    );
    const v = await remoteDevices(HOUSEHOLD);
    expect(core.openDeviceSnapshot).toHaveBeenCalledWith('T1', JSON.stringify(BUNDLE), JSON.stringify(snapshot));
    expect(v.asOf).toBe('2026-11-22T09:58:00Z');
    expect(v.devices).toHaveLength(1);
    expect(v.relay.state).toBe('active');
  });

  test('a list the core does not verify is not shown', async () => {
    mockHub.routerDevices.mockResolvedValue({ snapshot: {}, agentId: 'A1', asOf: '2026-11-22T09:58:00Z' });
    core.openDeviceSnapshot.mockRejectedValue(new Error('trust: signing key is not in the trust bundle'));
    const e = await remoteDevices(HOUSEHOLD).catch((x: unknown) => x);
    expect(e).toBeInstanceOf(RemoteTrustError);
    expect(errorMessage(e)).toMatch(/non vérifiée/);
  });

  test('before the relay ever pushed one, the list is empty and has no as_of', async () => {
    mockHub.routerDevices.mockResolvedValue(null);
    await expect(remoteDevices(HOUSEHOLD)).resolves.toMatchObject({ devices: [], asOf: null });
  });

  test('staleness: fresh under three minutes, old beyond', () => {
    expect(freshness('2026-11-22T09:59:00Z', T0)).toEqual({ ageS: 60, stale: false });
    expect(freshness('2026-11-22T09:40:00Z', T0)).toEqual({ ageS: 1200, stale: true });
    expect(freshness(null, T0)).toEqual({ ageS: null, stale: true });
    expect(STALE_AFTER_S).toBe(180);
    expect(ageWords(20)).toBe('moins d’une minute');
    expect(ageWords(20 * 60)).toBe('20 min');
    expect(ageWords(3 * 3600)).toBe('3 h');
  });
});

describe('block / unblock', () => {
  test('signed in the core, carried by the hub, believed after the relay’s signature', async () => {
    const result = { payload: 'cg==', sig: 'cw==' };
    mockHub.deviceCommand.mockResolvedValue({ commandId: 'c1', status: 'done', result });
    const res = await remoteSetBlocked(HOUSEHOLD, MAC, true, opts());
    expect(core.signDeviceCommand).toHaveBeenCalledWith('T1', MAC, 'block', 'ZTEG00000001');
    expect(mockHub.deviceCommand).toHaveBeenCalledWith('tok', 'T1', MAC, 'block', { payload: 'Q01E', sig: 'U0lH' });
    expect(core.verifyCommandResult).toHaveBeenCalledWith('T1', 'c1', JSON.stringify(BUNDLE), JSON.stringify(result));
    expect(res.status).toBe(200);
    // Never the box from this phone.
    expect(core.request).not.toHaveBeenCalled();
  });

  test('a result the core does not verify is no ✅', async () => {
    mockHub.deviceCommand.mockResolvedValue({ commandId: 'c1', status: 'done', result: { payload: 'eA==', sig: 'eA==' } });
    core.verifyCommandResult.mockRejectedValue(new Error('trust: signing key is not in the trust bundle'));
    await expect(remoteSetBlocked(HOUSEHOLD, MAC, true, opts())).rejects.toBeInstanceOf(RemoteTrustError);
  });

  test('a slow relay: the command is followed until it answers', async () => {
    mockHub.deviceCommand.mockResolvedValue({ commandId: 'c1', status: 'queued' });
    mockHub.command
      .mockResolvedValueOnce({ commandId: 'c1', status: 'delivered' })
      .mockResolvedValueOnce({ commandId: 'c1', status: 'done', result: { payload: 'cg==', sig: 'cw==' } });
    await expect(remoteSetBlocked(HOUSEHOLD, MAC, false, opts())).resolves.toMatchObject({ status: 200 });
    expect(core.signDeviceCommand).toHaveBeenCalledWith('T1', MAC, 'unblock', 'ZTEG00000001');
    expect(mockHub.command).toHaveBeenCalledTimes(2);
  });

  test('expired: by the hub, or on the phone’s clock', async () => {
    mockHub.deviceCommand.mockResolvedValue({ commandId: 'c1', status: 'queued' });
    mockHub.command.mockResolvedValue({ commandId: 'c1', status: 'expired' });
    const e = await remoteSetBlocked(HOUSEHOLD, MAC, true, opts()).catch((x: unknown) => x);
    expect(e).toBeInstanceOf(RemoteCommandError);
    expect((e as RemoteCommandError).reason).toBe('expired');
    expect(errorMessage(e)).toMatch(/Commande expirée/);

    mockHub.command.mockResolvedValue({ commandId: 'c1', status: 'queued' });
    let t = T0;
    const late = await remoteSetBlocked(HOUSEHOLD, MAC, true, opts({ now: () => (t += 30_000) })).catch((x: unknown) => x);
    expect((late as RemoteCommandError).reason).toBe('expired');
  });

  test('an offline relay: a clear sentence, nothing signed, nothing sent, no box session', async () => {
    mockHub.relayStatus.mockResolvedValue({ state: 'offline', agentId: 'A1', lastSeenAt: '2026-11-22T09:40:00Z', ageSeconds: 1200 });
    const e = await remoteSetBlocked(HOUSEHOLD, MAC, true, opts()).catch((x: unknown) => x);
    expect((e as RemoteCommandError).reason).toBe('relay_offline');
    expect(errorMessage(e)).toMatch(/hors ligne depuis \d\d:\d\d.*ne se connecte pas à la box à sa place/);
    expect(core.signDeviceCommand).not.toHaveBeenCalled();
    expect(mockHub.deviceCommand).not.toHaveBeenCalled();
    expect(core.request).not.toHaveBeenCalled();

    mockHub.relayStatus.mockResolvedValue({ state: 'none' });
    expect(((await remoteSetBlocked(HOUSEHOLD, MAC, true, opts()).catch((x: unknown) => x)) as RemoteCommandError).reason).toBe('relay_none');
  });

  test('the relay’s refusal and the box’s are told apart', async () => {
    mockHub.deviceCommand.mockResolvedValue({ commandId: 'c1', status: 'done', result: {} });
    core.verifyCommandResult.mockResolvedValueOnce(JSON.stringify({ agentId: 'A1', status: 403, executedAt: 'x' }));
    expect(((await remoteSetBlocked(HOUSEHOLD, MAC, true, opts()).catch((x: unknown) => x)) as RemoteCommandError).reason).toBe('refused');
    core.verifyCommandResult.mockResolvedValueOnce(JSON.stringify({ agentId: 'A1', status: 502, executedAt: 'x' }));
    const e = await remoteSetBlocked(HOUSEHOLD, MAC, true, opts()).catch((x: unknown) => x);
    // No code on screen (#119): the box's answer goes to the journal.
    expect(errorMessage(e)).toBe('La box n’a pas accepté. Réessayez dans une minute.');
    expect(core.note).toHaveBeenCalledWith('warn', expect.stringContaining('502'));
  });

  test('a paid action the core’s gate refuses is the lock, and nothing is sent', async () => {
    core.signDeviceCommand.mockRejectedValue(new Error('not_entitled: not entitled: block needs basic (no_token)'));
    const e = await remoteSetBlocked(HOUSEHOLD, MAC, true, opts()).catch((x: unknown) => x);
    expect(e).toBeInstanceOf(ApiError);
    expect((e as ApiError).notEntitled).toBe(true);
    expect(mockHub.deviceCommand).not.toHaveBeenCalled();
  });
});
