/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Pairing a relay from the owner's app (issue #43): the first bundle, the
// QR, the relay that arrives, its six digits, and what "same code" and
// "different code" do on the hub.
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
  putTrustBundle: jest.fn(),
  network: jest.fn(),
  putNetwork: jest.fn(),
  agents: jest.fn(),
  issuePairing: jest.fn(),
  revokeAgent: jest.fn(),
  putCredentials: jest.fn(),
};
jest.mock('../licensing/hub', () => ({
  get hub() {
    return mockHub;
  },
}));
const mockOwner = { signBundle: jest.fn(), invite: jest.fn(), pairingCode: jest.fn(), sealCredentials: jest.fn() };
jest.mock('../core/client', () => ({ core: { note: jest.fn() } }));
const mockConfig = { hubUrl: 'https://hub.test' };
jest.mock('../licensing/config', () => ({
  get config() {
    return mockConfig;
  },
}));
jest.mock('../core/owner', () => ({
  get owner() {
    return mockOwner;
  },
}));

import { core } from '../core/client';
import { useAccount } from '../licensing/account';
import { HubError } from '../licensing/api';
import { relays } from '../licensing/relays';

const PC = { id: 'A2', name: 'PC du salon', kind: 'pc', keyId: 'k', ed25519: 'RUQ=', x25519: 'WA==', createdAt: '2026-11-22T10:00:00Z' };

beforeEach(() => {
  for (const m of [...Object.values(mockHub), ...Object.values(mockOwner)]) m.mockReset();
  mockHub.agents.mockResolvedValue([{ ...PC, id: 'A1' }]);
  mockHub.issuePairing.mockResolvedValue({ code: 'CODE', expiresAt: '2026-11-22T10:05:00Z' });
  mockHub.network.mockResolvedValue({ ssids: null, bssids: null, routerPin: '' });
  mockOwner.invite.mockResolvedValue('kgp1.QR.k1');
  mockOwner.signBundle.mockResolvedValue({ payload: 'P', sig: 'S', version: 1 });
  mockOwner.sealCredentials.mockResolvedValue({ version: 1, boxPassword: true, relays: [{ agentId: 'A2', blob: 'QkxPQg==', version: 1 }] });
  (core.note as jest.Mock).mockReset();
});

test('a household without a bundle gets one signed by the owner before the QR', async () => {
  mockHub.trustBundle.mockResolvedValue(null);
  const started = await relays.start('pc', 'ab12');
  expect(mockOwner.signBundle).toHaveBeenCalledWith('T1', null, { routerPin: 'ab12' });
  expect(mockHub.putTrustBundle).toHaveBeenCalledWith('tok', 'T1', { payload: 'P', sig: 'S', version: 1 });
  expect(mockHub.putNetwork).toHaveBeenCalledWith('tok', 'T1', { ssids: [], bssids: [], routerPin: 'ab12' });
  expect(mockHub.issuePairing).toHaveBeenCalledWith('tok', 'T1', 'pc');
  expect(mockOwner.invite).toHaveBeenCalledWith('CODE');
  expect(started).toEqual({ invite: 'kgp1.QR.k1', expiresAt: '2026-11-22T10:05:00Z', known: ['A1'] });
});

test('a household that has a bundle and a network keeps them', async () => {
  mockHub.trustBundle.mockResolvedValue({ payload: 'p', sig: 's', version: 4 });
  mockHub.network.mockResolvedValue({ ssids: [], bssids: [], routerPin: 'ab12' });
  await relays.start('dedicated', 'ab12');
  expect(mockOwner.signBundle).not.toHaveBeenCalled();
  expect(mockHub.putNetwork).not.toHaveBeenCalled();
  expect(mockHub.issuePairing).toHaveBeenCalledWith('tok', 'T1', 'dedicated');
});

test('the relay that arrived is the new one, never a removed one', async () => {
  mockHub.agents.mockResolvedValue([{ ...PC, id: 'A1' }]);
  await expect(relays.arrived(['A1'])).resolves.toBeNull();
  mockHub.agents.mockResolvedValue([{ ...PC, id: 'A3', revokedAt: '2026-11-22T10:01:00Z' }, PC, { ...PC, id: 'A1' }]);
  await expect(relays.arrived(['A1'])).resolves.toEqual(PC);
});

test('the six digits are the core’s, from the keys the hub listed', async () => {
  mockOwner.pairingCode.mockResolvedValue('042917');
  await expect(relays.code(PC)).resolves.toBe('042917');
  expect(mockOwner.pairingCode).toHaveBeenCalledWith('RUQ=', 'WA==');
});

test('same code: the relay is signed into the next bundle, uploaded, and gets the sealed password', async () => {
  const current = { payload: 'p', sig: 's', version: 1 };
  const next = { payload: 'P2', sig: 'S2', version: 2 };
  mockHub.trustBundle.mockResolvedValue(current);
  mockOwner.signBundle.mockResolvedValue(next);
  await expect(relays.confirm(PC)).resolves.toEqual({ version: 2, passwordSent: true });
  expect(mockOwner.signBundle).toHaveBeenCalledWith('T1', current, {
    relay: { agentId: 'A2', kind: 'pc', label: 'PC du salon', ed25519: 'RUQ=', x25519: 'WA==' },
  });
  expect(mockHub.putTrustBundle).toHaveBeenCalledWith('tok', 'T1', next);
  // Sealed from the bundle just signed, for that relay only, and sent as is.
  expect(mockOwner.sealCredentials).toHaveBeenCalledWith('T1', next, 'A2');
  expect(mockHub.putCredentials).toHaveBeenCalledWith('tok', 'T1', 'A2', { blob: 'QkxPQg==', version: 1 });
  expect(mockHub.putTrustBundle.mock.invocationCallOrder[0]).toBeLessThan(mockHub.putCredentials.mock.invocationCallOrder[0]);
});

test('same code without a box password on this phone: the relay is added, the password comes later', async () => {
  mockHub.trustBundle.mockResolvedValue({ payload: 'p', sig: 's', version: 1 });
  mockOwner.signBundle.mockResolvedValue({ payload: 'P2', sig: 'S2', version: 2 });
  mockOwner.sealCredentials.mockResolvedValue({ version: 0, boxPassword: false, relays: [] });
  await expect(relays.confirm(PC)).resolves.toEqual({ version: 2, passwordSent: false });
  expect(mockHub.putTrustBundle).toHaveBeenCalled();
  expect(mockHub.putCredentials).not.toHaveBeenCalled();
});

test('after a login the password is sealed again for every relay of the bundle', async () => {
  const current = { payload: 'p', sig: 's', version: 3 };
  mockHub.trustBundle.mockResolvedValue(current);
  mockOwner.sealCredentials.mockResolvedValue({
    version: 2,
    boxPassword: true,
    relays: [
      { agentId: 'A1', blob: 'MQ==', version: 2 },
      { agentId: 'A2', blob: 'Mg==', version: 2 },
    ],
  });
  // A2 already has this version: the password did not change for it.
  mockHub.putCredentials.mockImplementation(async (_t: string, _i: string, id: string) => {
    if (id === 'A2') throw new HubError(409, 'CONFLICT', 'a newer version is already stored');
  });
  await expect(relays.resealAll()).resolves.toBe(2);
  expect(mockOwner.sealCredentials).toHaveBeenCalledWith('T1', current);
  expect(mockHub.putCredentials).toHaveBeenCalledWith('tok', 'T1', 'A1', { blob: 'MQ==', version: 2 });
  expect(mockHub.putCredentials).toHaveBeenCalledWith('tok', 'T1', 'A2', { blob: 'Mg==', version: 2 });
});

test('no bundle, nothing to seal', async () => {
  mockHub.trustBundle.mockResolvedValue(null);
  await expect(relays.resealAll()).resolves.toBe(0);
  expect(mockOwner.sealCredentials).not.toHaveBeenCalled();
});

test('another hub refusal while sealing is an error', async () => {
  mockHub.trustBundle.mockResolvedValue({ payload: 'p', sig: 's', version: 3 });
  mockHub.putCredentials.mockRejectedValue(new HubError(403, 'FORBIDDEN', 'no'));
  await expect(relays.resealAll()).rejects.toBeInstanceOf(HubError);
});

test('after a box login: signed in, the relays get it; signed out, nothing is asked; a failure is only noted', async () => {
  mockHub.trustBundle.mockResolvedValue({ payload: 'p', sig: 's', version: 3 });
  useAccount.setState({ ready: true, account: null });
  await relays.afterBoxLogin();
  expect(mockOwner.sealCredentials).not.toHaveBeenCalled();

  useAccount.setState({ ready: true, account: { accountId: 'a', phone: '+221771234567', sessionToken: 'kgs', expiresAt: 'x' } });
  await relays.afterBoxLogin();
  expect(mockHub.putCredentials).toHaveBeenCalledTimes(1);
  expect(core.note).toHaveBeenCalledWith('info', expect.stringContaining('transmis à 1 relais'));

  mockHub.putCredentials.mockRejectedValue(new HubError(0, 'NETWORK', 'offline'));
  await expect(relays.afterBoxLogin()).resolves.toBeUndefined();
  expect(core.note).toHaveBeenLastCalledWith('warn', expect.stringContaining('non transmis'));
});

test('different code: the relay is removed and nothing is signed', async () => {
  await relays.reject(PC);
  expect(mockHub.revokeAgent).toHaveBeenCalledWith('tok', 'T1', 'A2');
  expect(mockOwner.signBundle).not.toHaveBeenCalled();
  expect(mockHub.putTrustBundle).not.toHaveBeenCalled();
});
