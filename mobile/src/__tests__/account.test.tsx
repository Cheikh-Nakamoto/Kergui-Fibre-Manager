/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The Account screen (RFC-006, decision of 2026-11-21): the Play app has no
// payment surface. It shows the account, the box's tier and end date, the
// grace notice, box transfer, refresh and sign out; never a price, an offer
// or a way to pay.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn(), canGoBack: () => false },
  Stack: { Screen: () => null },
  useFocusEffect: () => undefined,
}));
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('../core/client', () => ({ api: { inspect: jest.fn() }, core: {} }));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKey: null } }));
const mockSubscriptions = jest.fn();
jest.mock('../licensing/hub', () => ({ hub: { subscriptions: () => mockSubscriptions() } }));

import AccountScreen from '../app/account';
import { useAccount } from '../licensing/account';
import { useEntitlement } from '../licensing/entitlement';
import { useSession } from '../session/store';

const NOW = Date.UTC(2026, 10, 21, 12);
const DAY = 86400;
const PAYMENT_WORDING =
  /NabooPay|Wave|Orange Money|FCFA|F CFA|XOF|prix|abonne|payer|acheter|achat|s’abonner|site web|https?:\/\/|offre|essai|souscri|€|\$/i;

function texts(node: TestInstance): string[] {
  const out: string[] = [];
  for (const c of node.children) {
    if (typeof c === 'string') out.push(c);
    else out.push(...texts(c));
  }
  return out;
}

let root: Root | null = null;

async function render(): Promise<string> {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <PaperProvider settings={{ icon: () => null }}>
          <AccountScreen />
        </PaperProvider>
      </QueryClientProvider>,
    );
  });
  // Let the hub query resolve (react-query batches its updates on a timer).
  for (let i = 0; i < 3; i++) {
    await act(async () => {
      jest.advanceTimersByTime(10);
      await Promise.resolve();
    });
  }
  return texts(root.container).join('\n');
}

const signedIn = () =>
  useAccount.setState({ ready: true, account: { accountId: 'acc_1', phone: '+221771234567', sessionToken: 'kgs_1', expiresAt: 'x' } });

beforeEach(() => {
  jest.useFakeTimers({ now: NOW, doNotFake: ['nextTick', 'setImmediate', 'queueMicrotask'] });
  mockSubscriptions.mockReset().mockResolvedValue([]);
  useSession.setState({ session: null });
  useAccount.setState({ ready: true, account: null });
  useEntitlement.setState({
    boxId: 'ZTEG00000001',
    token: null,
    claims: null,
    error: null,
    refreshing: false,
    lastRefresh: NOW,
    refresh: jest.fn(async () => undefined),
  });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
  jest.useRealTimers();
});

test('signed out: a sign-in button and nothing about paying', async () => {
  const text = await render();
  expect(text).toContain('Se connecter');
  expect(text).not.toMatch(PAYMENT_WORDING);
});

test('signed in on an active box: number, tier, end date, sign out; no price, offer or link', async () => {
  signedIn();
  const now = NOW / 1000;
  useEntitlement.setState({ claims: { account: 'acc_1', boxId: 'ZTEG00000001', tier: 'max', expiresAt: now + 10 * DAY, issuedAt: now, keyId: '65b60673d6ed884b' } });
  const text = await render();
  expect(text).toContain('+221771234567');
  expect(text).toContain('Max');
  expect(text).toContain('01/12/2026');
  expect(text).toContain('Se déconnecter');
  expect(text).toContain('Actualiser');
  expect(text).not.toMatch(PAYMENT_WORDING);
  expect(text).not.toMatch(/\bPro\b/);
});

test('offline past the end date: the grace notice, still nothing about paying', async () => {
  signedIn();
  const now = NOW / 1000;
  useEntitlement.setState({ claims: { account: 'acc_1', boxId: 'ZTEG00000001', tier: 'basic', expiresAt: now - DAY, issuedAt: now - 30 * DAY, keyId: '65b60673d6ed884b' } });
  const text = await render();
  expect(text).toContain('Basic');
  expect(text).toMatch(/hors ligne/i);
  expect(text).not.toMatch(PAYMENT_WORDING);
});

test('box transfer is offered when the account has a pass on another box', async () => {
  signedIn();
  mockSubscriptions.mockResolvedValue([
    { boxId: 'ZTEG000000AA', tier: 'max', effectiveTier: 'max', active: true, expiresAt: '2026-12-20T00:00:00Z', channel: 'prepaid' },
  ]);
  const text = await render();
  expect(text).toContain('ZTEG000000AA');
  expect(text).toMatch(/Changer de box/);
  expect(text).not.toMatch(PAYMENT_WORDING);
});

test('demo mode: everything is open, and the screen says so without selling', async () => {
  useSession.setState({ session: { mode: 'demo' } });
  const text = await render();
  expect(text).toMatch(/Démo/);
  expect(text).not.toMatch(PAYMENT_WORDING);
});
