/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The entitlement store against a fake hub: what is kept, refreshed and
// dropped. The token is the hub-signed vector from token.test.ts.
const GO_PUB = 'ebVWLo/mVPlAeLES6KmLp5AfhTrmlb7X4OORC60ElmQ=';
const GO_TOKEN =
  'eyJhY2NvdW50IjoiYWNjXzdmM2EiLCJib3hfaWQiOiJaVEVHMDAwMDAwMDEiLCJ0aWVyIjoibWF4IiwiZXhwaXJlc19hdCI6MTc5NzU5MjAwMCwiaXNzdWVkX2F0IjoxNzk1MDAwMDAwLCJrZXlfaWQiOiI2NWI2MDY3M2Q2ZWQ4ODRiIn0.EzDS623Tq9RoLnW0ti8vUARunQt9F8WYt07LUP5AvTCb6EzDcw1fDyloOaQi9fCNlNGJrIZMkQx3iGj7VuSPAQ';
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

const answer = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));
// The fake hub: POST bind always answers (the app acts on nothing in it);
// every other call is fetchMock's, in order.
const fetchMock = jest.fn();
let routes: string[] = [];
let bindReply = () => answer(200, { boxId: 'ZTEG00000001', subscription: {} });
global.fetch = ((url: string, init: RequestInit) => {
  routes.push(url.replace('https://hub.test/licensing/v1/', ''));
  return url.endsWith('/licensing/v1/bind') ? bindReply() : fetchMock(url, init);
}) as unknown as typeof fetch;
const GO_EXP = 1797592000;

beforeEach(async () => {
  jest.useFakeTimers({ now: IAT * 1000 + 3600_000 });
  mockStore.clear();
  fetchMock.mockReset();
  routes = [];
  bindReply = () => answer(200, { boxId: 'ZTEG00000001', subscription: {} });
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

test('the first sign-in on a box binds it to the account, then fetches its token', async () => {
  await useEntitlement.getState().setBox('ZTEG00000001');
  fetchMock.mockImplementationOnce(() => answer(200, { boxId: 'ZTEG00000001', tier: 'max', token: GO_TOKEN }));
  await useEntitlement.getState().refresh({ force: true });
  expect(routes).toEqual(['otp/verify', 'bind', 'token']);
  expect(currentTier(useEntitlement.getState())).toBe('max');
});

test('a bind refused for the session signs the account out and keeps no token', async () => {
  await useEntitlement.getState().setBox('ZTEG00000001');
  bindReply = () => answer(401, { error: { code: 'SESSION_INVALID', message: 'no' } });
  await useEntitlement.getState().refresh({ force: true });
  expect(routes).toEqual(['otp/verify', 'bind']);
  expect(useAccount.getState().account).toBeNull();
  expect(currentTier(useEntitlement.getState())).toBe('free');
});

test('Max unlocks once the token says Max, and locks again after expiry plus the 3-day grace', async () => {
  await useEntitlement.getState().setBox('ZTEG00000001');
  expect(currentTier(useEntitlement.getState())).toBe('free');
  fetchMock.mockImplementationOnce(() => answer(200, { boxId: 'ZTEG00000001', tier: 'max', token: GO_TOKEN }));
  await useEntitlement.getState().refresh({ force: true });
  expect(currentTier(useEntitlement.getState())).toBe('max');

  // The phone goes offline: the fake clock runs past the expiry.
  jest.setSystemTime(GO_EXP * 1000);
  expect(currentTier(useEntitlement.getState())).toBe('max');
  jest.setSystemTime((GO_EXP + 3 * 24 * 3600 - 1) * 1000);
  expect(currentTier(useEntitlement.getState())).toBe('max');
  jest.setSystemTime((GO_EXP + 3 * 24 * 3600) * 1000);
  expect(currentTier(useEntitlement.getState())).toBe('free');

  // A restart past the grace does not bring it back either.
  useEntitlement.setState({ boxId: null, token: null, claims: null });
  await useEntitlement.getState().load();
  expect(currentTier(useEntitlement.getState())).toBe('free');
});
