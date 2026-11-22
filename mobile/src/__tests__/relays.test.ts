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
};
jest.mock('../licensing/hub', () => ({
  get hub() {
    return mockHub;
  },
}));
const mockOwner = { signBundle: jest.fn(), invite: jest.fn(), pairingCode: jest.fn() };
jest.mock('../core/owner', () => ({
  get owner() {
    return mockOwner;
  },
}));

import { relays } from '../licensing/relays';

const PC = { id: 'A2', name: 'PC du salon', kind: 'pc', keyId: 'k', ed25519: 'RUQ=', x25519: 'WA==', createdAt: '2026-11-22T10:00:00Z' };

beforeEach(() => {
  for (const m of [...Object.values(mockHub), ...Object.values(mockOwner)]) m.mockReset();
  mockHub.agents.mockResolvedValue([{ ...PC, id: 'A1' }]);
  mockHub.issuePairing.mockResolvedValue({ code: 'CODE', expiresAt: '2026-11-22T10:05:00Z' });
  mockHub.network.mockResolvedValue({ ssids: null, bssids: null, routerPin: '' });
  mockOwner.invite.mockResolvedValue('kgp1.QR.k1');
  mockOwner.signBundle.mockResolvedValue({ payload: 'P', sig: 'S', version: 1 });
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

test('same code: the relay is signed into the next bundle and uploaded', async () => {
  const current = { payload: 'p', sig: 's', version: 1 };
  mockHub.trustBundle.mockResolvedValue(current);
  mockOwner.signBundle.mockResolvedValue({ payload: 'P2', sig: 'S2', version: 2 });
  await expect(relays.confirm(PC)).resolves.toBe(2);
  expect(mockOwner.signBundle).toHaveBeenCalledWith('T1', current, {
    relay: { agentId: 'A2', kind: 'pc', label: 'PC du salon', ed25519: 'RUQ=', x25519: 'WA==' },
  });
  expect(mockHub.putTrustBundle).toHaveBeenCalledWith('tok', 'T1', { payload: 'P2', sig: 'S2', version: 2 });
});

test('different code: the relay is removed and nothing is signed', async () => {
  await relays.reject(PC);
  expect(mockHub.revokeAgent).toHaveBeenCalledWith('tok', 'T1', 'A2');
  expect(mockOwner.signBundle).not.toHaveBeenCalled();
  expect(mockHub.putTrustBundle).not.toHaveBeenCalled();
});
