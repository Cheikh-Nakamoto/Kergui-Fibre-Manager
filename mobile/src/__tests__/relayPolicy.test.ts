/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Auto-reject on the household's relay (#36), the app's side. Publication:
// the owner's core signs the rule and the token, and the app PUTs it to the
// hub whenever either changes — never from a phone that is not the owner's,
// never from the demo, and a hub out of reach changes nothing locally.
// Election: before a round the app asks the hub who enforces and hands the
// answer to the core, whose rounds (the watch service's too) then stand
// down for the relay; no session, no network or an error: the phone acts.
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('../licensing/household', () => ({
  withHousehold: (run: (tok: string, id: string) => Promise<unknown>) => run('tok', 'T1'),
}));
const mockHub = {
  trustBundle: jest.fn(),
  relayPolicy: jest.fn(),
  putRelayPolicy: jest.fn(),
  autoRejectEnforcer: jest.fn(),
};
jest.mock('../licensing/hub', () => ({
  get hub() {
    return mockHub;
  },
}));
const mockOwner = { signRelayPolicy: jest.fn() };
jest.mock('../core/owner', () => ({
  get owner() {
    return mockOwner;
  },
}));
const mockApi = { autoReject: jest.fn(), setAutoRejectEnforcer: jest.fn() };
const mockNote = jest.fn();
jest.mock('../core/client', () => ({
  get api() {
    return mockApi;
  },
  core: { note: (...a: unknown[]) => mockNote(...a) },
}));
const mockConfig = { hubUrl: 'https://hub.test' };
jest.mock('../licensing/config', () => ({
  get config() {
    return mockConfig;
  },
}));
const mockAccount: { account: unknown; household: () => unknown } = {
  account: { accountId: 'acc', phone: '+221', sessionToken: 's', expiresAt: '' },
  household: () => ({ tenantId: 'T1', userId: 'U1' }),
};
jest.mock('../licensing/account', () => ({ useAccount: { getState: () => mockAccount } }));
const mockEntitlement = { token: 'max.token' as string | null };
jest.mock('../licensing/entitlement', () => ({
  useEntitlement: { getState: () => mockEntitlement },
  afterCoreEntitlement: jest.fn(),
}));

import { HubError } from '../licensing/api';
import { afterCoreEntitlement } from '../licensing/entitlement';
import { forgetRelayPolicy, refreshEnforcer, syncRelayPolicy } from '../features/relayPolicy';

const BUNDLE = { payload: 'Yg==', sig: 'Yg==', version: 2 };
const SIGNED = { payload: 'UA==', sig: 'Uw==', version: 1 };

function rule(enabled: boolean, accepted: string[] = ['aa:bb:cc:00:00:01']) {
  return {
    enabled,
    since: enabled ? '2026-11-23T08:00:00Z' : '',
    delay_minutes: 15,
    devices: accepted.map((mac) => ({ mac, state: 'accepted', first_seen: '', deadline: '' })),
  };
}

let now = 1_000_000;

beforeEach(() => {
  for (const m of [...Object.values(mockHub), ...Object.values(mockOwner), ...Object.values(mockApi), mockNote]) m.mockReset();
  forgetRelayPolicy();
  now += 3_600_000;
  jest.spyOn(Date, 'now').mockImplementation(() => now);
  mockConfig.hubUrl = 'https://hub.test';
  mockAccount.account = { accountId: 'acc', phone: '+221', sessionToken: 's', expiresAt: '' };
  mockEntitlement.token = 'max.token';
  mockApi.autoReject.mockResolvedValue(rule(true));
  mockApi.setAutoRejectEnforcer.mockImplementation(async (a: { enforcer: string }) => ({ enforcer: a.enforcer }));
  mockHub.trustBundle.mockResolvedValue(BUNDLE);
  mockHub.relayPolicy.mockResolvedValue(null);
  mockHub.putRelayPolicy.mockResolvedValue(undefined);
  mockOwner.signRelayPolicy.mockResolvedValue(SIGNED);
});

afterEach(() => jest.restoreAllMocks());

describe('publishing the relay policy', () => {
  test('the owner’s core signs it from the hub’s bundle and policy, and the app PUTs it', async () => {
    const current = { payload: 'cA==', sig: 'cw==', version: 4 };
    mockHub.relayPolicy.mockResolvedValue(current);
    await expect(syncRelayPolicy()).resolves.toBe('published');
    expect(mockOwner.signRelayPolicy).toHaveBeenCalledWith('T1', BUNDLE, current);
    expect(mockHub.putRelayPolicy).toHaveBeenCalledWith('tok', 'T1', SIGNED);
  });

  test('nothing changed: the hub is not asked again', async () => {
    await syncRelayPolicy();
    mockHub.trustBundle.mockClear();
    mockHub.putRelayPolicy.mockClear();
    await expect(syncRelayPolicy()).resolves.toBe('unchanged');
    expect(mockHub.trustBundle).not.toHaveBeenCalled();
    expect(mockHub.putRelayPolicy).not.toHaveBeenCalled();
  });

  test('the setting, an accepted device or the token changes: published again', async () => {
    await syncRelayPolicy();
    mockApi.autoReject.mockResolvedValue(rule(false, []));
    await expect(syncRelayPolicy()).resolves.toBe('published');
    mockApi.autoReject.mockResolvedValue(rule(false, ['aa:bb:cc:00:00:02']));
    await expect(syncRelayPolicy()).resolves.toBe('published');
    mockEntitlement.token = 'renewed.token';
    await expect(syncRelayPolicy()).resolves.toBe('published');
    expect(mockHub.putRelayPolicy).toHaveBeenCalledTimes(4);
  });

  test('a phone that is not the owner’s publishes nothing: its core signs nothing', async () => {
    mockOwner.signRelayPolicy.mockResolvedValue(null);
    await expect(syncRelayPolicy()).resolves.toBe('unchanged');
    expect(mockHub.putRelayPolicy).not.toHaveBeenCalled();
  });

  test('a household without a trust bundle has no relay to tell', async () => {
    mockHub.trustBundle.mockResolvedValue(null);
    await expect(syncRelayPolicy()).resolves.toBe('unchanged');
    expect(mockOwner.signRelayPolicy).not.toHaveBeenCalled();
    expect(mockHub.putRelayPolicy).not.toHaveBeenCalled();
  });

  test('no account, no hub in this build, or the demo: nothing is asked', async () => {
    mockAccount.account = null;
    await expect(syncRelayPolicy()).resolves.toBe('skipped');
    mockAccount.account = { accountId: 'acc' };
    mockConfig.hubUrl = '';
    await expect(syncRelayPolicy()).resolves.toBe('skipped');
    mockConfig.hubUrl = 'https://hub.test';
    await expect(syncRelayPolicy({ demo: true })).resolves.toBe('skipped');
    expect(mockHub.trustBundle).not.toHaveBeenCalled();
    expect(mockOwner.signRelayPolicy).not.toHaveBeenCalled();
  });

  test('a box without Wi-Fi access control has no rule to send', async () => {
    mockApi.autoReject.mockRejectedValue(new Error('501'));
    await expect(syncRelayPolicy()).resolves.toBe('skipped');
    expect(mockHub.trustBundle).not.toHaveBeenCalled();
  });

  test('the hub out of reach: noted, never thrown, tried again a little later', async () => {
    mockHub.putRelayPolicy.mockRejectedValueOnce(new HubError(0, 'NETWORK', 'offline'));
    await expect(syncRelayPolicy()).resolves.toBe('failed');
    expect(mockNote).toHaveBeenCalledWith('warn', expect.stringContaining('relais'));
    // Right after: not hammered.
    await expect(syncRelayPolicy()).resolves.toBe('skipped');
    expect(mockHub.putRelayPolicy).toHaveBeenCalledTimes(1);
    // A few minutes later, or at once when the setting changes: again.
    now += 3 * 60_000;
    await expect(syncRelayPolicy()).resolves.toBe('published');
    expect(mockHub.putRelayPolicy).toHaveBeenCalledTimes(2);
  });

  test('forced (a token handed to the core, a relay just paired): sent even if nothing seemed to change', async () => {
    await syncRelayPolicy();
    await expect(syncRelayPolicy({ force: true })).resolves.toBe('published');
    expect(mockHub.putRelayPolicy).toHaveBeenCalledTimes(2);
  });

  test('the core handed a new token: the policy is sent without waiting for the next refresh', async () => {
    await syncRelayPolicy();
    const listeners = (afterCoreEntitlement as jest.Mock).mock.calls.map(([l]) => l as () => void);
    expect(listeners).toHaveLength(1);
    listeners[0]();
    await syncRelayPolicy(); // queued behind the forced one
    expect(mockHub.putRelayPolicy).toHaveBeenCalledTimes(2);
  });
});

describe('who enforces', () => {
  test('the hub names the relay: its answer goes to the core, which stands down', async () => {
    const answer = { enforcer: 'relay', agentId: 'A1', relayHeartbeatAt: '2026-11-23T08:00:00Z', relayHeartbeatAgeSeconds: 12 };
    mockHub.autoRejectEnforcer.mockResolvedValue(answer);
    await expect(refreshEnforcer()).resolves.toBe('relay');
    expect(mockHub.autoRejectEnforcer).toHaveBeenCalledWith('tok', 'T1');
    expect(mockApi.setAutoRejectEnforcer).toHaveBeenCalledWith(answer);
  });

  test('the hub names the phone: the core acts at once', async () => {
    mockHub.autoRejectEnforcer.mockResolvedValue({ enforcer: 'phone' });
    await expect(refreshEnforcer()).resolves.toBe('phone');
    expect(mockApi.setAutoRejectEnforcer).toHaveBeenCalledWith({ enforcer: 'phone' });
  });

  test('the hub out of reach or wrong: the phone acts', async () => {
    mockHub.autoRejectEnforcer.mockRejectedValue(new HubError(0, 'NETWORK', 'offline'));
    await expect(refreshEnforcer()).resolves.toBe('phone');
    expect(mockApi.setAutoRejectEnforcer).toHaveBeenLastCalledWith({ enforcer: 'phone' });
    now += 60_000;
    mockHub.autoRejectEnforcer.mockResolvedValue({ enforcer: 'relay' }); // no age
    await expect(refreshEnforcer()).resolves.toBe('phone');
    expect(mockApi.setAutoRejectEnforcer).toHaveBeenLastCalledWith({ enforcer: 'phone' });
  });

  test('no session, the demo, or the rule off: the hub is not asked and the phone acts', async () => {
    mockAccount.account = null;
    await expect(refreshEnforcer()).resolves.toBe('phone');
    mockAccount.account = { accountId: 'acc' };
    await expect(refreshEnforcer({ demo: true })).resolves.toBe('phone');
    mockApi.autoReject.mockResolvedValue(rule(false));
    await expect(refreshEnforcer()).resolves.toBe('phone');
    expect(mockHub.autoRejectEnforcer).not.toHaveBeenCalled();
  });

  test('asked at most every 20 seconds; the core keeps the last answer meanwhile', async () => {
    mockHub.autoRejectEnforcer.mockResolvedValue({ enforcer: 'relay', relayHeartbeatAgeSeconds: 3 });
    await refreshEnforcer();
    now += 5_000;
    await expect(refreshEnforcer()).resolves.toBe('relay');
    expect(mockHub.autoRejectEnforcer).toHaveBeenCalledTimes(1);
    now += 30_000;
    await refreshEnforcer();
    expect(mockHub.autoRejectEnforcer).toHaveBeenCalledTimes(2);
  });
});
