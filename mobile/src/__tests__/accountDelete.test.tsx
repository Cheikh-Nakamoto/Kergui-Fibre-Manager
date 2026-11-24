/* eslint-disable import/first -- the mocks must come before the modules they replace */
// « Supprimer mon compte » on the Compte screen (#37): two confirmations
// (an alert, then the word SUPPRIMER typed), the hub's deletion, then the
// welcome screen. The demo refuses it in a sentence.
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
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));
jest.mock('../licensing/hub', () => ({ hub: { subscriptions: async () => [] } }));
const mockConfirm = jest.fn();
jest.mock('../ui/confirm', () => ({ confirm: (m: string) => mockConfirm(m) }));
const mockDelete = jest.fn();
jest.mock('../licensing/deleteAccount', () => ({
  DELETE_WORD: 'SUPPRIMER',
  DemoAccountError: class DemoAccountError extends Error {},
  deleteAccount: () => mockDelete(),
}));

import { router } from 'expo-router';

import AccountScreen from '../app/account';
import { fr } from '../i18n/fr';
import { HubError } from '../licensing/api';
import { useAccount } from '../licensing/account';
import { DemoAccountError } from '../licensing/deleteAccount';
import { useEntitlement } from '../licensing/entitlement';
import { useSession } from '../session/store';

function texts(node: TestInstance): string[] {
  const out: string[] = [];
  for (const c of node.children) {
    if (typeof c === 'string') out.push(c);
    else out.push(...texts(c));
  }
  return out;
}

let root: Root | null = null;
const all = (id: string) => root!.container.queryAll((n) => n.props.testID === id);
const text = () => texts(root!.container).join('\n');

async function settle() {
  for (let i = 0; i < 5; i++) await act(async () => void (await Promise.resolve()));
}

async function press(id: string) {
  let node: TestInstance | null | undefined = all(id)[0];
  while (node && typeof node.props.onClick !== 'function' && typeof node.props.onPress !== 'function') node = node.parent;
  if (!node) throw new Error(`no ${id}`);
  const ev = { nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined };
  await act(async () => void (node.props.onClick ?? node.props.onPress)(ev));
  await settle();
}

async function type(id: string, value: string) {
  const input = all(id).find((n) => typeof n.props.onChangeText === 'function');
  if (!input) throw new Error(`no input ${id}`);
  await act(async () => input.props.onChangeText(value));
}

const disabled = (id: string) => {
  let node: TestInstance | null | undefined = all(id)[0];
  while (node) {
    if (node.props.accessibilityState?.disabled !== undefined) return node.props.accessibilityState.disabled;
    if (node.props['aria-disabled'] !== undefined) return node.props['aria-disabled'];
    node = node.parent;
  }
  return undefined;
};

async function render() {
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
  await settle();
}

beforeEach(() => {
  mockConfirm.mockReset().mockResolvedValue(true);
  mockDelete.mockReset().mockResolvedValue(undefined);
  (router.replace as jest.Mock).mockClear();
  useSession.setState({ session: { mode: 'router', routerUrl: 'https://192.168.1.1', pin: 'p' } });
  useAccount.setState({ ready: true, account: { accountId: 'acc_1', phone: '+221771234567', sessionToken: 'kgs_1', expiresAt: 'x' } });
  useEntitlement.setState({ boxId: 'ZTEG00000001', token: null, claims: null, error: null, refreshing: false, refresh: jest.fn(async () => undefined) });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
});

test('two confirmations, then the hub deletes and the app goes back to the welcome screen', async () => {
  await render();
  expect(text()).toContain('Supprimer mon compte');
  await press('account-delete');
  expect(mockConfirm).toHaveBeenCalledTimes(1);
  expect(mockConfirm.mock.calls[0][0]).toBe(fr['account.deleteConfirm']);
  expect(mockDelete).not.toHaveBeenCalled();

  // Second step: the word, typed. Anything else keeps the button off.
  expect(disabled('account-delete-final')).toBe(true);
  await type('account-delete-word', 'supprime');
  expect(disabled('account-delete-final')).toBe(true);
  await type('account-delete-word', ' supprimer ');
  expect(disabled('account-delete-final')).toBe(false);
  await press('account-delete-final');

  expect(mockDelete).toHaveBeenCalledTimes(1);
  expect(router.replace).toHaveBeenCalledWith('/');
});

test('the first alert cancelled: nothing more is asked, nothing deleted', async () => {
  mockConfirm.mockResolvedValue(false);
  await render();
  await press('account-delete');
  expect(all('account-delete-word')).toHaveLength(0);
  expect(mockDelete).not.toHaveBeenCalled();
});

test('the hub fails: the error is told and the app stays', async () => {
  mockDelete.mockRejectedValue(new HubError(0, 'NETWORK', 'offline'));
  await render();
  await press('account-delete');
  await type('account-delete-word', 'SUPPRIMER');
  await press('account-delete-final');
  expect(all('account-delete-error').length).toBeGreaterThan(0);
  expect(router.replace).not.toHaveBeenCalled();
});

test('the demo: refused in a sentence, no alert, nothing sent', async () => {
  useSession.setState({ session: { mode: 'demo' } });
  mockDelete.mockRejectedValue(new DemoAccountError());
  await render();
  await press('account-delete');
  expect(text()).toContain(fr['account.deleteDemo']);
  expect(mockConfirm).not.toHaveBeenCalled();
  expect(mockDelete).not.toHaveBeenCalled();
});

test('signed out on a real box: no account to delete, no button', async () => {
  useAccount.setState({ account: null });
  await render();
  expect(all('account-delete')).toHaveLength(0);
});
