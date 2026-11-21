/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The entitlement store against a fake hub: what is kept, refreshed and
// dropped. The token is the hub-signed vector from token.test.ts.
const GO_PUB = 'ebVWLo/mVPlAeLES6KmLp5AfhTrmlb7X4OORC60ElmQ=';
const GO_TOKEN =
  'eyJhY2NvdW50IjoiYWNjXzdmM2EiLCJib3hfaWQiOiJaVEVHMDAwMDAwMDEiLCJ0aWVyIjoibWF4IiwiZXhwaXJlc19hdCI6MTc5NzU5MjAwMCwiaXNzdWVkX2F0IjoxNzk1MDAwMDAwfQ.zaFGaLATpLAD7XMd9jnyqt1NDtbYaIWxljJDAhlelvPYh6f-jJ5mx7jtUw_IyUXMTriWJepNTCZFdaBErdZRCg';
const IAT = 1795000000;

jest.mock('expo-constants', () => ({
  __esModule: true,
  default: {
    deviceName: 'Test phone',
    expoConfig: { extra: { licensing: { hubUrl: 'https://hub.test', publicKey: GO_PUB } } },
  },
}));
const mockStore = new Map<string, string>();
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async (k: string) => mockStore.get(k) ?? null,
  setItemAsync: async (k: string, v: string) => void mockStore.set(k, v),
  deleteItemAsync: async (k: string) => void mockStore.delete(k),
}));
const mockInspect = jest.fn();
jest.mock('../core/client', () => ({ api: { inspect: () => mockInspect() } }));

import { useAccount } from '../licensing/account';
import { currentTier, useEntitlement } from '../licensing/entitlement';

const fetchMock = jest.fn();
global.fetch = fetchMock as unknown as typeof fetch;
const answer = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));

beforeEach(async () => {
  jest.useFakeTimers({ now: IAT * 1000 + 3600_000 });
  mockStore.clear();
  fetchMock.mockReset();
  useEntitlement.setState({ boxId: null, token: null, claims: null, error: null, lastRefresh: 0, refreshing: false });
  fetchMock.mockImplementationOnce(() => answer(200, { sessionToken: 'kgs_1', accountId: 'acc_7f3a', phone: '+221771234567', expiresAt: 'x' }));
  await useAccount.getState().verify('+221771234567', '123456');
});
afterEach(() => jest.useRealTimers());

test('the box serial comes from /api/inspect; an empty one means unsupported', async () => {
  mockInspect.mockResolvedValueOnce({ Info: { SerialNumber: 'zteg00000001' } });
  await useEntitlement.getState().identify();
  expect(useEntitlement.getState().boxId).toBe('ZTEG00000001');
  mockInspect.mockResolvedValueOnce({ Info: { SerialNumber: '' } });
  await useEntitlement.getState().identify();
  expect(useEntitlement.getState().boxId).toBe('');
});

test('a verified token is kept, survives a restart, and gives its tier on its box only', async () => {
  await useEntitlement.getState().setBox('ZTEG00000001');
  fetchMock.mockImplementationOnce(() => answer(200, { boxId: 'ZTEG00000001', tier: 'max', token: GO_TOKEN }));
  await useEntitlement.getState().refresh({ force: true });
  expect(fetchMock.mock.calls[1][0]).toBe('https://hub.test/licensing/v1/token');
  expect(fetchMock.mock.calls[1][1].headers.Authorization).toBe('Bearer kgs_1');
  expect(currentTier(useEntitlement.getState())).toBe('max');

  useEntitlement.setState({ boxId: null, token: null, claims: null });
  await useEntitlement.getState().load();
  expect(currentTier(useEntitlement.getState())).toBe('max');

  await useEntitlement.getState().setBox('ZTEG00000002');
  expect(useEntitlement.getState().token).toBeNull();
  expect(currentTier(useEntitlement.getState())).toBe('free');
});

test('offline, the cached token stays', async () => {
  await useEntitlement.getState().setBox('ZTEG00000001');
  fetchMock.mockImplementationOnce(() => answer(200, { boxId: 'ZTEG00000001', tier: 'max', token: GO_TOKEN }));
  await useEntitlement.getState().refresh({ force: true });
  fetchMock.mockImplementationOnce(() => Promise.reject(new TypeError('Network request failed')));
  await useEntitlement.getState().refresh({ force: true });
  expect(currentTier(useEntitlement.getState())).toBe('max');
  expect(useEntitlement.getState().error).toMatchObject({ code: 'NETWORK' });
});

test('Free from the hub drops the token; a token for another box is refused', async () => {
  await useEntitlement.getState().setBox('ZTEG00000001');
  fetchMock.mockImplementationOnce(() => answer(200, { boxId: 'ZTEG00000001', tier: 'max', token: GO_TOKEN }));
  await useEntitlement.getState().refresh({ force: true });
  fetchMock.mockImplementationOnce(() => answer(200, { boxId: 'ZTEG00000001', tier: 'free' }));
  await useEntitlement.getState().refresh({ force: true });
  expect(currentTier(useEntitlement.getState())).toBe('free');

  await useEntitlement.getState().setBox('ZTEG00000009');
  fetchMock.mockImplementationOnce(() => answer(200, { boxId: 'ZTEG00000009', tier: 'max', token: GO_TOKEN }));
  await useEntitlement.getState().refresh({ force: true });
  expect(currentTier(useEntitlement.getState())).toBe('free');
  expect(useEntitlement.getState().error).toMatchObject({ code: 'BAD_RESPONSE' });
});

test('a refused session signs the account out', async () => {
  await useEntitlement.getState().setBox('ZTEG00000001');
  fetchMock.mockImplementationOnce(() => answer(401, { error: { code: 'SESSION_INVALID', message: 'no' } }));
  await useEntitlement.getState().refresh({ force: true });
  expect(useAccount.getState().account).toBeNull();
});

test('refreshes are throttled unless forced', async () => {
  await useEntitlement.getState().setBox('ZTEG00000001');
  fetchMock.mockImplementation(() => answer(200, { boxId: 'ZTEG00000001', tier: 'free' }));
  await useEntitlement.getState().refresh({ force: true });
  await useEntitlement.getState().refresh();
  expect(fetchMock).toHaveBeenCalledTimes(2); // verify + one token call
  jest.advanceTimersByTime(61_000);
  await useEntitlement.getState().refresh();
  expect(fetchMock).toHaveBeenCalledTimes(3);
});
