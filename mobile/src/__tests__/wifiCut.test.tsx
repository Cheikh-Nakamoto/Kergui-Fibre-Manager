/* eslint-disable import/first -- the mocks must come before the modules they replace */
// "Couper l'accès" (#31): the device sheet's Basic action that makes the box's
// Wi-Fi refuse the device (unlike "Bloquer", which only cuts Internet), and
// its free inverse "Rendre l'accès". The deny list the core reads from the box
// feeds the "Accès coupé" badge and section.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Alert, Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

const mockPush = jest.fn();
let mockMac = 'ac:bb:cc:00:00:22';
jest.mock('expo-router', () => ({
  router: { push: (...a: unknown[]) => mockPush(...a), replace: jest.fn(), back: jest.fn() },
  Stack: { Screen: () => null },
  useLocalSearchParams: () => ({ mac: mockMac }),
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
import { api } from '../core/client';
import { useEntitlement } from '../licensing/entitlement';
import { useSession } from '../session/store';

const NEUTRAL = 'Cette fonctionnalité n’est pas disponible pour votre compte.';
const PC = 'ac:bb:cc:00:00:22';
const PHONE = 'ac:bb:cc:00:00:33';
const ENC = (m: string) => encodeURIComponent(m);
const request = KerguiCore.request as jest.Mock;

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

/** The core as the screens see it: devices, a Wi-Fi deny list, the writes. */
function box(opts: { devices: ReturnType<typeof device>[]; denied: string[] | 'unsupported' }) {
  let denied = opts.denied;
  request.mockImplementation(async (method: string, path: string) => {
    if (method === 'GET' && path === '/api/devices') return { status: 200, body: JSON.stringify(opts.devices) };
    if (method === 'GET' && path === '/api/wifi/blocked') {
      if (denied === 'unsupported') return { status: 501, body: '{"error":"not implemented: no Wi-Fi access control"}' };
      // The core answers canonical (lower case) MACs.
      return { status: 200, body: JSON.stringify(denied.map((m) => m.toLowerCase())) };
    }
    const m = /^\/api\/devices\/([^/]+)\/wifi-(block|unblock)$/.exec(path);
    if (method === 'POST' && m && denied !== 'unsupported') {
      const mac = decodeURIComponent(m[1]);
      denied = m[2] === 'block' ? [...denied, mac] : denied.filter((d) => d.toLowerCase() !== mac.toLowerCase());
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

// The writes to the box. The usage sample is a POST too, but it only reads
// the box's counters (each refresh takes one, #32).
const posts = () =>
  request.mock.calls.filter(([m, p]) => m === 'POST' && p !== '/api/usage/sample').map(([, p]) => p as string);

function tier(t: 'free' | 'basic') {
  const now = Math.floor(Date.now() / 1000);
  useEntitlement.setState(
    t === 'free'
      ? { boxId: 'ZTEG00000001', token: null, claims: null }
      : {
          boxId: 'ZTEG00000001',
          token: 'x.y',
          claims: { account: 'acc_1', boxId: 'ZTEG00000001', tier: 'basic', expiresAt: now + 86400, issuedAt: now, keyId: 'k' },
        },
  );
}

let alert: jest.SpyInstance;
const sayYes = () => alert.mockImplementation((_t, _m, buttons?: { onPress?: () => void }[]) => buttons?.[1]?.onPress?.());

beforeEach(() => {
  request.mockReset();
  mockPush.mockReset();
  mockMac = PC;
  alert = jest.spyOn(Alert, 'alert').mockImplementation(() => undefined);
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
  tier('basic');
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = qc = null;
  alert.mockRestore();
});

test('the client calls the core’s Wi-Fi access control routes', async () => {
  request.mockResolvedValue({ status: 200, body: '["ac:bb:cc:00:00:22"]' });
  await expect(api.wifiBlocked()).resolves.toEqual(['ac:bb:cc:00:00:22']);
  expect(request).toHaveBeenLastCalledWith('GET', '/api/wifi/blocked', '');
  await api.wifiBlock(PC);
  expect(request).toHaveBeenLastCalledWith('POST', `/api/devices/${ENC(PC)}/wifi-block`, '');
  await api.wifiUnblock(PC);
  expect(request).toHaveBeenLastCalledWith('POST', `/api/devices/${ENC(PC)}/wifi-unblock`, '');
});

test('with Basic, "Couper l’accès" confirms that the password will not help, then cuts', async () => {
  box({ devices: [device(PC, 'PC-Bureau')], denied: [] });
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-toggle-wifi-cut'));
  const button = byId(r, 'device-toggle-wifi-cut');
  expect(button).toBeDefined();
  expect(text(r)).toContain('Couper l’accès');
  // "Bloquer" is still there, distinct.
  expect(byId(r, 'device-toggle-block')).toBeDefined();

  sayYes();
  await press(button);
  const [, message, buttons] = alert.mock.calls[0];
  expect(message).toContain('PC-Bureau');
  expect(message).toMatch(/Wi-Fi/);
  expect(message).toMatch(/même avec le mot de passe/);
  expect(buttons[1].style).toBe('destructive');
  await settle(() => text(r).includes('Rendre l’accès'));
  expect(posts()).toEqual([`/api/devices/${ENC(PC)}/wifi-block`]);
  expect(text(r)).toContain('Rendre l’accès');
});

test('without Basic, "Couper l’accès" shows the neutral sentence and reaches nothing', async () => {
  tier('free');
  box({ devices: [device(PC, 'PC-Bureau')], denied: [] });
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-toggle-wifi-cut'));
  await press(byId(r, 'device-toggle-wifi-cut'));
  expect(alert).toHaveBeenCalledTimes(1);
  expect(alert.mock.calls[0][1]).toBe(NEUTRAL);
  expect(posts()).toEqual([]);
  expect(mockPush).not.toHaveBeenCalled();
});

test('"Rendre l’accès" is free: it works with no pass at all', async () => {
  tier('free');
  box({ devices: [device(PC, 'PC-Bureau')], denied: [PC] });
  const r = await render(<DeviceScreen />);
  await settle(() => text(r).includes('Rendre l’accès'));
  expect(text(r)).toContain('Accès coupé');
  sayYes();
  await press(byId(r, 'device-toggle-wifi-cut'));
  expect(alert.mock.calls[0][1]).not.toBe(NEUTRAL);
  await settle(() => text(r).includes('Couper l’accès'));
  expect(posts()).toEqual([`/api/devices/${ENC(PC)}/wifi-unblock`]);
});

test('a cut device the box no longer lists still has a sheet to give access back', async () => {
  tier('free');
  mockMac = PHONE;
  box({ devices: [device(PC, 'PC-Bureau')], denied: [PHONE] });
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-toggle-wifi-cut'));
  expect(text(r)).not.toContain('Appareil introuvable');
  expect(text(r)).toContain('Rendre l’accès');
  // Nothing to block on a device that is not on the network.
  expect(byId(r, 'device-toggle-block')).toBeUndefined();
  sayYes();
  await press(byId(r, 'device-toggle-wifi-cut'));
  await settle(() => posts().length > 0);
  expect(posts()).toEqual([`/api/devices/${ENC(PHONE)}/wifi-unblock`]);
});

test('a box without Wi-Fi access control shows no "Couper l’accès"', async () => {
  box({ devices: [device(PC, 'PC-Bureau')], denied: 'unsupported' });
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-toggle-block'));
  await settle();
  expect(byId(r, 'device-toggle-block')).toBeDefined();
  expect(byId(r, 'device-toggle-wifi-cut')).toBeUndefined();
  expect(text(r)).not.toMatch(/Pas encore pris en charge/);
});

test('the device list has an "Accès coupé" section fed by the deny list, with its badge', async () => {
  box({ devices: [device(PC, 'PC-Bureau'), device('ac:bb:cc:00:00:44', 'TV-Salon')], denied: [PC, PHONE] });
  const r = await render(<Devices />);
  await settle(() => text(r).includes('Bloqués (2)'));
  await press(byId(r, 'devices-filter-blocked'));
  await settle(() => !!byId(r, 'devices-cut-section'));
  const section = byId(r, 'devices-cut-section');
  expect(section).toBeDefined();
  const inSection = texts(section).join('\n');
  // A known device by its name, one the box no longer lists by its MAC.
  expect(inSection).toContain('PC-Bureau');
  expect(inSection).toContain(PHONE);
  expect(inSection).not.toContain('TV-Salon');
  expect(byId(r, `device-cut-badge-${PC}`)).toBeDefined();
  expect(byId(r, 'device-cut-badge-ac:bb:cc:00:00:44')).toBeUndefined();
  // Each row opens the sheet, where access is given back.
  await press(byId(r, `device-${PHONE}`));
  expect(mockPush).toHaveBeenCalledWith({ pathname: '/device/[mac]', params: { mac: PHONE } });
});

test('no "Accès coupé" section when nothing is cut or the box cannot cut', async () => {
  box({ devices: [device(PC, 'PC-Bureau')], denied: [] });
  let r = await render(<Devices />);
  await settle(() => text(r).includes('PC-Bureau'));
  expect(byId(r, 'devices-cut-section')).toBeUndefined();
  await act(async () => r.unmount());

  box({ devices: [device(PC, 'PC-Bureau')], denied: 'unsupported' });
  r = await render(<Devices />);
  await settle(() => text(r).includes('PC-Bureau'));
  expect(byId(r, 'devices-cut-section')).toBeUndefined();
  expect(text(r)).not.toMatch(/Pas encore pris en charge/);
});

test('a filter under the search bar switches between the connected devices and the blocked ones', async () => {
  const TV = 'ac:bb:cc:00:00:44';
  const blockedPhone = { ...device('ac:bb:cc:00:00:55', 'Tel-Inconnu'), Blocked: true };
  box({ devices: [device(PC, 'PC-Bureau'), device(TV, 'TV-Salon'), blockedPhone], denied: [PC] });
  const r = await render(<Devices />);
  await settle(() => text(r).includes('TV-Salon'));

  // Connected (the default): only the devices with access.
  expect(text(r)).toContain('Connectés (1)');
  expect(text(r)).toContain('Bloqués (2)');
  expect(text(r)).not.toContain('Tel-Inconnu');
  expect(text(r)).not.toContain('PC-Bureau');
  expect(byId(r, 'devices-cut-section')).toBeUndefined();

  // Blocked: the filter's blocks (definitive) and the Wi-Fi cuts, apart.
  await press(byId(r, 'devices-filter-blocked'));
  await settle(() => !!byId(r, 'devices-blocked-section'));
  const blocked = texts(byId(r, 'devices-blocked-section')).join('\n');
  expect(blocked).toContain('Bloqués par le filtre (1)');
  expect(blocked).toContain('Tel-Inconnu');
  const cut = texts(byId(r, 'devices-cut-section')).join('\n');
  expect(cut).toContain('Accès coupé au Wi-Fi (1)');
  expect(cut).toContain('PC-Bureau');
  expect(text(r)).not.toContain('TV-Salon');

  // And back.
  await press(byId(r, 'devices-filter-connected'));
  await settle(() => text(r).includes('TV-Salon'));
  expect(byId(r, 'devices-blocked-section')).toBeUndefined();
});

test('nothing blocked: the Bloqués screen says so', async () => {
  box({ devices: [device(PC, 'PC-Bureau')], denied: [] });
  const r = await render(<Devices />);
  await settle(() => text(r).includes('PC-Bureau'));
  expect(text(r)).toContain('Bloqués (0)');
  await press(byId(r, 'devices-filter-blocked'));
  await settle(() => text(r).includes('Aucun appareil bloqué.'));
  expect(text(r)).not.toContain('PC-Bureau');
});
