/* eslint-disable import/first -- the mocks must come before the modules they replace */
// « Supprimer mon compte » (#37, Play's account-deletion policy): the hub
// erases the account (DELETE /licensing/v1/account with the kgs_ session),
// then the phone forgets the account, both sessions, the box and the
// caches. The demo has no account: refused, nothing sent.

const mockStore = new Map<string, string>();
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async (k: string) => mockStore.get(k) ?? null,
  setItemAsync: async (k: string, v: string) => void mockStore.set(k, v),
  deleteItemAsync: async (k: string) => void mockStore.delete(k),
}));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));
const mockDisableAlerts = jest.fn(async () => undefined);
const mockForgetKnown = jest.fn(async () => undefined);
jest.mock('../alerts/alerts', () => ({
  disableAlerts: () => mockDisableAlerts(),
  forgetKnown: () => mockForgetKnown(),
}));
jest.mock('../core/client', () => ({ core: { stopDemo: async () => undefined }, api: {} }));
const mockFetch = jest.fn();
global.fetch = mockFetch as unknown as typeof fetch;

import { useInbox } from '../alerts/inbox';
import { useAccount } from '../licensing/account';
import { DemoAccountError, deleteAccount, DELETE_WORD } from '../licensing/deleteAccount';
import { useEntitlement } from '../licensing/entitlement';
import { HUB_TOKENS_KEY } from '../licensing/hubTokens';
import { setHubSession } from '../licensing/hub';
import { useSession } from '../session/store';

function answer(status: number, body?: unknown) {
  return new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const ACCOUNT = { accountId: 'acc-1', phone: '+221771234567', sessionToken: 'kgs_abc', expiresAt: 'x' };

beforeEach(() => {
  mockStore.clear();
  mockFetch.mockReset();
  mockDisableAlerts.mockClear();
  mockForgetKnown.mockClear();
  // A signed-in phone on a real box, with what it keeps.
  mockStore.set('kergui.hub.account', JSON.stringify(ACCOUNT));
  mockStore.set(HUB_TOKENS_KEY, JSON.stringify({ userId: 'u1', tenantId: 't1', accessToken: 'a', refreshToken: 'r' }));
  mockStore.set('kergui.session', JSON.stringify({ mode: 'router', routerUrl: 'https://192.168.1.1', pin: 'p' }));
  mockStore.set('kergui.hub.entitlement', JSON.stringify({ boxId: 'ZTEG00000001', token: 't' }));
  mockStore.set('kergui.hub.backup', '{}');
  mockStore.set('kergui.remote.tenant', 't1');
  mockStore.set('kergui.inbox', '[]');
  useAccount.setState({ ready: true, account: ACCOUNT });
  setHubSession(ACCOUNT.sessionToken);
  useSession.setState({ session: { mode: 'router', routerUrl: 'https://192.168.1.1', pin: 'p' } });
  useEntitlement.setState({ boxId: 'ZTEG00000001' });
  useInbox.setState({ items: [{ id: 'n1', at: 1, title: 'x', body: 'y' } as never] });
});

test('the word to type', () => {
  expect(DELETE_WORD).toBe('SUPPRIMER');
});

test('the hub erases the account, then the phone is emptied and back to the welcome screen', async () => {
  mockFetch.mockResolvedValueOnce(answer(204));
  await deleteAccount();

  expect(mockFetch).toHaveBeenCalledTimes(1);
  const [url, init] = mockFetch.mock.calls[0];
  expect(url).toBe('https://hub.test/licensing/v1/account');
  expect(init.method).toBe('DELETE');
  expect(init.headers.Authorization).toBe('Bearer kgs_abc');

  expect(useAccount.getState().account).toBeNull();
  expect(useSession.getState().session).toBeNull(); // the welcome screen
  expect(useEntitlement.getState().boxId).toBeNull();
  expect(useInbox.getState().items).toEqual([]);
  expect(mockDisableAlerts).toHaveBeenCalled();
  expect(mockForgetKnown).toHaveBeenCalled();
  for (const k of ['kergui.hub.account', HUB_TOKENS_KEY, 'kergui.session', 'kergui.hub.backup', 'kergui.remote.tenant']) {
    expect(mockStore.has(k)).toBe(false);
  }
});

test('the session was already gone on the hub (deleted from another phone): the phone is emptied too', async () => {
  mockFetch.mockResolvedValueOnce(answer(401, { error: { code: 'SESSION_INVALID', message: 'sign in again' } }));
  await deleteAccount();
  expect(useAccount.getState().account).toBeNull();
  expect(mockStore.has('kergui.hub.account')).toBe(false);
});

test('the hub cannot be reached: nothing is erased, the error is told', async () => {
  mockFetch.mockRejectedValueOnce(new Error('offline'));
  await expect(deleteAccount()).rejects.toMatchObject({ code: 'NETWORK' });
  expect(useAccount.getState().account).toEqual(ACCOUNT);
  expect(mockStore.has('kergui.hub.account')).toBe(true);
  expect(useSession.getState().session).not.toBeNull();
});

test('the demo has no account: refused, nothing sent, nothing erased', async () => {
  useSession.setState({ session: { mode: 'demo' } });
  await expect(deleteAccount()).rejects.toBeInstanceOf(DemoAccountError);
  expect(mockFetch).not.toHaveBeenCalled();
  expect(useSession.getState().session).toEqual({ mode: 'demo' });
  expect(mockStore.has('kergui.hub.account')).toBe(true);
});
