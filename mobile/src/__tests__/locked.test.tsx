/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The Go core refuses a paid write by itself (403, code "not_entitled"),
// whatever the screen's own gate thought. The app shows that as a lock and
// the one neutral sentence of RFC-006: no error wording, nothing to tap, no
// screen opened.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Alert, Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

const mockPush = jest.fn();
jest.mock('expo-router', () => ({
  router: { push: (...a: unknown[]) => mockPush(...a), replace: jest.fn(), back: jest.fn() },
  Stack: { Screen: () => null },
  useLocalSearchParams: () => ({ mac: 'AC:BB:CC:00:00:22' }),
}));
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('../alerts/alerts', () => ({ checkNewDevices: async () => 0, enforceAutoReject: async () => 0 }));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));

import KerguiCore from '../../modules/kergui-core';
import DeviceScreen from '../app/device/[mac]';
import { ApiError, call } from '../core/client';
import { errorMessage } from '../i18n';
import { useEntitlement } from '../licensing/entitlement';
import { useSession } from '../session/store';
import { ActionError } from '../ui/ActionError';

const NEUTRAL = 'Cette fonctionnalité n’est pas disponible pour votre compte.';
const NOT_ENTITLED_BODY = JSON.stringify({
  error: 'not entitled: block needs basic (other_box)',
  code: 'not_entitled',
  feature: 'block',
  required: 'basic',
});
const request = KerguiCore.request as jest.Mock;

function texts(node: TestInstance): string[] {
  const out: string[] = [];
  for (const c of node.children) {
    if (typeof c === 'string') out.push(c);
    else out.push(...texts(c));
  }
  return out;
}

// Icons render as their name, so the test can see the lock.
const iconAsText = ({ name }: { name: string }) => <Text>{`icon:${name}`}</Text>;

let root: Root | null = null;
let qc: QueryClient | null = null;

async function render(ui: React.ReactElement) {
  // gcTime 0: no cache timer left behind to keep Jest from exiting.
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false, gcTime: 0 } } });
  qc = client;
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={client}>
        <PaperProvider settings={{ icon: iconAsText }}>{ui}</PaperProvider>
      </QueryClientProvider>,
    );
  });
  for (let i = 0; i < 3; i++) await act(async () => void (await Promise.resolve()));
  return root;
}

let alert: jest.SpyInstance;
beforeEach(() => {
  request.mockReset();
  mockPush.mockReset();
  alert = jest.spyOn(Alert, 'alert').mockImplementation(() => undefined);
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
  // The screen's own gate believes Basic: only the core's answer locks it.
  const now = Math.floor(Date.now() / 1000);
  useEntitlement.setState({
    boxId: 'ZTEG00000001',
    token: 'x.y',
    claims: { account: 'acc_1', boxId: 'ZTEG00000001', tier: 'basic', expiresAt: now + 86400, issuedAt: now, keyId: '65b60673d6ed884b' },
  });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = qc = null;
  alert.mockRestore();
});

test('the core’s not-entitled answer becomes a typed ApiError with the neutral message', async () => {
  request.mockResolvedValue({ status: 403, body: NOT_ENTITLED_BODY });
  const err = await call('POST', '/api/devices/x/block').catch((e: unknown) => e);
  expect(err).toBeInstanceOf(ApiError);
  expect((err as ApiError).notEntitled).toBe(true);
  expect((err as ApiError).code).toBe('not_entitled');
  expect(errorMessage(err)).toBe(NEUTRAL);
  // Another 403 is not a lock.
  expect(new ApiError(403, 'forbidden').notEntitled).toBe(false);
});

test('ActionError: a lock and the neutral sentence, nothing to tap', async () => {
  const r = await render(<ActionError error={new ApiError(403, 'not entitled', 'not_entitled')} />);
  const text = texts(r.container).join('\n');
  expect(text).toContain('icon:lock');
  expect(text).toContain(NEUTRAL);
  expect(text).not.toMatch(/not entitled|erreur/i);
  expect(r.container.queryAll((n) => typeof n.props.onPress === 'function')).toHaveLength(0);
});

test('ActionError: any other failure stays an error, without a lock', async () => {
  const r = await render(<ActionError error={new ApiError(502, 'router unreachable')} />);
  const text = texts(r.container).join('\n');
  expect(text).not.toContain('icon:lock');
  expect(text).not.toContain(NEUTRAL);
});

test('blocking a device the core refuses shows the lock and the neutral message', async () => {
  request.mockImplementation(async (method: string, path: string) => {
    if (method === 'GET' && path === '/api/devices') {
      return { status: 200, body: JSON.stringify([{ MAC: 'AC:BB:CC:00:00:22', Hostname: 'pc', CustomName: '', Blocked: false }]) };
    }
    if (method === 'POST' && path.endsWith('/block')) return { status: 403, body: NOT_ENTITLED_BODY };
    return { status: 404, body: '{"error":"not found"}' };
  });
  const r = await render(<DeviceScreen />);
  // The sheet's 6-month curve is Max, so a Basic pass already shows one
  // lock there: the refused block must add its own, under the button.
  const locks = () => texts(r.container).filter((s) => s === NEUTRAL).length;

  const find = () => r.container.queryAll((n) => n.props.testID === 'device-trust-blocked')[0];
  for (let i = 0; i < 50 && !find(); i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 10))));
  const button = find();
  expect(button).toBeDefined();
  const before = locks();
  // No question first since #108: an « Annuler » follows instead.
  alert.mockImplementation((_t, _m, buttons?: { onPress?: () => void }[]) => buttons?.[1]?.onPress?.());
  // A host view: Pressability answers its click as a press.
  await act(async () => void button.props.onClick({ nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined }));
  const shown = () => locks() > before;
  for (let i = 0; i < 50 && !shown(); i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 10))));

  expect(request).toHaveBeenCalledWith('POST', '/api/devices/AC%3ABB%3ACC%3A00%3A00%3A22/block', '');
  const text = texts(r.container).join('\n');
  expect(text).toContain('icon:lock');
  expect(text).toContain(NEUTRAL);
  expect(locks()).toBe(before + 1);
  expect(mockPush).not.toHaveBeenCalled();
});
