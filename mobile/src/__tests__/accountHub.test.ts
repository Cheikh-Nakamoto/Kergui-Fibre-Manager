/* eslint-disable import/first -- the mocks must come before the modules they replace */
// One sign-in, two sessions (issue #41): verifying the WhatsApp code stores
// the kgs_ licensing session and the household's kernel pair, both in
// SecureStore; signing out ends both.

const mockStore = new Map<string, string>();
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async (k: string) => mockStore.get(k) ?? null,
  setItemAsync: async (k: string, v: string) => void mockStore.set(k, v),
  deleteItemAsync: async (k: string) => void mockStore.delete(k),
}));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));
const mockFetch = jest.fn();
global.fetch = mockFetch as unknown as typeof fetch;

import { useAccount } from '../licensing/account';
import { HUB_TOKENS_KEY } from '../licensing/hubTokens';
import { hubTokens } from '../licensing/hub';

function b64url(s: string): string {
  return Buffer.from(s).toString('base64').replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}
const jwt = (exp: number) => `${b64url('{"alg":"HS256"}')}.${b64url(JSON.stringify({ sub: 'u1', exp }))}.sig`;
const inAQuarter = () => Math.floor(Date.now() / 1000) + 900;

function answer(status: number, body: unknown) {
  return new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const VERIFIED = {
  sessionToken: 'kgs_abc',
  accountId: 'acc-1',
  phone: '+221771234567',
  expiresAt: '2027-11-22T00:00:00Z',
  hub: { userId: 'u1', tenantId: 't1', accessToken: jwt(inAQuarter()), refreshToken: 'rt-1' },
};

beforeEach(async () => {
  mockStore.clear();
  mockFetch.mockReset();
  await hubTokens.clear();
  useAccount.setState({ ready: false, account: null });
});

test('verify keeps the licensing session and the household pair in SecureStore', async () => {
  mockFetch.mockResolvedValueOnce(answer(200, VERIFIED));
  await useAccount.getState().verify('+221771234567', '123456');

  expect(useAccount.getState().account).toMatchObject({ accountId: 'acc-1', sessionToken: 'kgs_abc' });
  expect(JSON.parse(mockStore.get(HUB_TOKENS_KEY)!)).toEqual(VERIFIED.hub);
  expect(useAccount.getState().household()).toEqual({ tenantId: 't1', userId: 'u1' });
  // Fresh: no call to get it.
  expect(await useAccount.getState().hubAccessToken()).toBe(VERIFIED.hub.accessToken);
  expect(mockFetch).toHaveBeenCalledTimes(1);
});

test('after a restart both sessions are back, and an expired access token is refreshed', async () => {
  mockStore.set('kergui.hub.account', JSON.stringify({ accountId: 'acc-1', phone: '+221771234567', sessionToken: 'kgs_abc', expiresAt: 'x' }));
  mockStore.set(HUB_TOKENS_KEY, JSON.stringify({ ...VERIFIED.hub, accessToken: jwt(1) }));
  await useAccount.getState().load();
  expect(useAccount.getState().household()).toEqual({ tenantId: 't1', userId: 'u1' });

  const fresh = jwt(inAQuarter());
  mockFetch.mockResolvedValueOnce(answer(200, { accessToken: fresh, refreshToken: 'rt-2' }));
  expect(await useAccount.getState().hubAccessToken()).toBe(fresh);
  expect(String(mockFetch.mock.calls[0][0])).toBe('https://hub.test/api/v1/auth/refresh');
  expect(JSON.parse(mockStore.get(HUB_TOKENS_KEY)!).refreshToken).toBe('rt-2');
});

test('both sessions refused: the account is signed out', async () => {
  mockStore.set('kergui.hub.account', JSON.stringify({ accountId: 'acc-1', phone: '+221771234567', sessionToken: 'kgs_abc', expiresAt: 'x' }));
  mockStore.set(HUB_TOKENS_KEY, JSON.stringify({ ...VERIFIED.hub, accessToken: jwt(1) }));
  await useAccount.getState().load();
  mockFetch
    .mockResolvedValueOnce(answer(401, { error: { code: 'UNAUTHORIZED', message: 'revoked' } }))
    .mockResolvedValueOnce(answer(401, { error: { code: 'SESSION_INVALID', message: 'sign in' } }));
  await expect(useAccount.getState().hubAccessToken()).rejects.toMatchObject({ code: 'SESSION_INVALID' });
  expect(useAccount.getState().account).toBeNull();
  expect(mockStore.size).toBe(0);
});

test('sign out ends the licensing session and the kernel one, and forgets both', async () => {
  mockFetch.mockResolvedValueOnce(answer(200, VERIFIED));
  await useAccount.getState().verify('+221771234567', '123456');
  mockFetch.mockResolvedValue(answer(204, undefined));
  await useAccount.getState().signOut();

  const calls = mockFetch.mock.calls.slice(1).map(([url, init]) => `${init.method} ${url}`);
  expect(calls).toEqual(expect.arrayContaining(['DELETE https://hub.test/licensing/v1/session', 'POST https://hub.test/api/v1/auth/logout']));
  expect(mockStore.size).toBe(0);
  expect(useAccount.getState().household()).toBeNull();
});
