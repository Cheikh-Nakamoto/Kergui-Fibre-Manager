/* eslint-disable import/first -- the mocks must come before the modules they replace */
// « Liste pleine » (RFC-007 D6, #102): when the box refuses a block because
// its list is full, the app says how many devices it takes and offers the
// blocked devices that are away, instead of the box's raw answer.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Alert, Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

const mockNavigate = jest.fn();
let mockParams: Record<string, string> = {};
jest.mock('expo-router', () => ({
  router: { push: jest.fn(), navigate: (...a: unknown[]) => mockNavigate(...a), replace: jest.fn(), back: jest.fn() },
  Stack: { Screen: () => null },
  useLocalSearchParams: () => mockParams,
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
import Devices from '../app/(tabs)/devices';
import DeviceScreen from '../app/device/[mac]';
import { ApiError, api, call } from '../core/client';
import { errorMessage } from '../i18n';
import { useEntitlement } from '../licensing/entitlement';
import { useSession } from '../session/store';

const PC = 'ac:bb:cc:00:00:22';
const GONE = '02:aa:bb:00:00:61';
const ENC = (m: string) => encodeURIComponent(m);
const request = KerguiCore.request as jest.Mock;
const FULL = 'La box accepte 3 appareils au plus. Retirez un appareil que vous n’utilisez plus.';

const device = (mac: string, name: string) => ({
  MAC: mac,
  IP: '192.168.1.10',
  Hostname: name,
  CustomName: '',
  Vendor: '',
  SSID: 'MonWiFi-2G',
  Connected: true,
  Blocked: false,
  NewlySeen: false,
});

const fullBody = JSON.stringify({ error: 'router: access list full (firewall, 3/3)', code: 'access_list_full', kind: 'firewall', limit: 3, used: 3 });

/** The core with a full list: every block is refused, GONE is blocked and away. */
function fullBox() {
  let blocked = [{ mac: GONE, name: 'Vieux-Telephone', last_seen: '2026-09-01T10:00:00Z', present: false, firewall: true, wifi: false, by_kergui: true }];
  request.mockImplementation(async (method: string, path: string) => {
    if (method === 'GET' && path === '/api/devices') return { status: 200, body: JSON.stringify([device(PC, 'PC-Bureau')]) };
    if (method === 'GET' && path === '/api/wifi/blocked') return { status: 200, body: '[]' };
    if (method === 'GET' && path === '/api/blocked') return { status: 200, body: JSON.stringify({ devices: blocked }) };
    if (method === 'POST' && path === `/api/devices/${ENC(PC)}/block`) return { status: 409, body: fullBody };
    if (method === 'POST' && path === `/api/devices/${ENC(GONE)}/unblock`) {
      blocked = [];
      return { status: 200, body: '{}' };
    }
    return { status: 404, body: '{"error":"not found"}' };
  });
}

function texts(node: TestInstance): string[] {
  const out: string[] = [];
  for (const c of node.children) {
    if (typeof c === 'string') out.push(c);
    else out.push(...texts(c));
  }
  return out;
}

const iconAsText = ({ name }: { name: string }) => <Text>{`icon:${name}`}</Text>;

let root: Root | null = null;
let qc: QueryClient | null = null;

async function settle(until: () => boolean = () => false) {
  for (let i = 0; i < 50 && !until(); i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 10))));
}

async function render(ui: React.ReactElement) {
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
  await settle();
  return root;
}

const byId = (r: Root, id: string) => r.container.queryAll((n) => n.props.testID === id)[0];
const text = (r: Root) => texts(r.container).join('\n');
const press = async (node: TestInstance) =>
  act(async () => void node.props.onClick({ nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined }));

let alert: jest.SpyInstance;

beforeEach(() => {
  request.mockReset();
  mockNavigate.mockReset();
  mockParams = { mac: PC };
  alert = jest.spyOn(Alert, 'alert').mockImplementation((_t, _m, buttons?: { onPress?: () => void }[]) => buttons?.[1]?.onPress?.());
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
  const now = Math.floor(Date.now() / 1000);
  useEntitlement.setState({
    boxId: 'ZTEG00000001',
    token: 'x.y',
    claims: { account: 'acc_1', boxId: 'ZTEG00000001', tier: 'basic', expiresAt: now + 86400, issuedAt: now, keyId: 'k' },
  });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = qc = null;
  alert.mockRestore();
});

test('a full list is an ApiError with the box’s limit, said in plain French', async () => {
  request.mockResolvedValue({ status: 409, body: fullBody });
  let err: unknown;
  await call('POST', `/api/devices/${ENC(PC)}/block`).catch((e) => (err = e));
  expect(err).toBeInstanceOf(ApiError);
  expect((err as ApiError).accessListFull).toBe(true);
  expect((err as ApiError).listLimit).toBe(3);
  expect(errorMessage(err)).toBe(FULL);

  // A box that never said its limit: the same advice, without a number.
  request.mockResolvedValue({ status: 409, body: JSON.stringify({ error: 'full', code: 'access_list_full', kind: 'wifi', limit: 0, used: 7 }) });
  await call('POST', '/api/devices/x/wifi-block').catch((e) => (err = e));
  expect(errorMessage(err)).toBe('La box n’accepte plus d’autre appareil. Retirez un appareil que vous n’utilisez plus.');
});

test('the client reads the blocked devices, away ones included', async () => {
  fullBox();
  await expect(api.blocked()).resolves.toHaveLength(1);
  expect(request).toHaveBeenLastCalledWith('GET', '/api/blocked', '');
});

test('blocking on a full list shows the sentence and a link to the blocked devices that are away', async () => {
  fullBox();
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-toggle-block'));
  await press(byId(r, 'device-toggle-block'));
  await settle(() => !!byId(r, 'list-full'));
  expect(text(r)).toContain(FULL);
  expect(text(r)).not.toContain('access list full');
  await press(byId(r, 'list-full-link'));
  expect(mockNavigate).toHaveBeenCalledWith({ pathname: '/devices', params: { show: 'absent' } });
});

test('the « Bloqués » screen lists the blocked devices that are away, and frees one for free', async () => {
  fullBox();
  mockParams = { show: 'absent' };
  const r = await render(<Devices />);
  await settle(() => !!byId(r, 'devices-absent-section'));
  expect(byId(r, 'devices-absent-section')).toBeDefined();
  expect(text(r)).toContain('Vieux-Telephone');
  expect(text(r)).toContain('Vu le 01/09/2026');
  await press(byId(r, `absent-unblock-${GONE}`));
  await settle(() => !byId(r, 'devices-absent-section'));
  expect(request).toHaveBeenCalledWith('POST', `/api/devices/${ENC(GONE)}/unblock`, '');
  expect(byId(r, 'devices-absent-section')).toBeUndefined();
});
