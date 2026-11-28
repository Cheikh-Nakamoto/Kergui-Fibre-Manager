/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Refusing a device at the Wi-Fi (#31, since #108 the switch « Ne plus le
// laisser se connecter du tout (même au Wi-Fi) » under « Bloqué »): Basic,
// unlike letting it back, free. The deny list the core reads from the box
// feeds « Ne peut plus se connecter » and its section.
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
  Blocked: false as boolean,
  NewlySeen: false,
});

/** The core as the screens see it: devices, a Wi-Fi deny list, the writes. */
function box(opts: { devices: ReturnType<typeof device>[]; denied: string[] | 'unsupported' }) {
  let denied = opts.denied;
  request.mockImplementation(async (method: string, path: string, body?: string) => {
    if (method === 'GET' && path === '/api/devices') return { status: 200, body: JSON.stringify(opts.devices) };
    if (method === 'GET' && path === '/api/wifi/blocked') {
      if (denied === 'unsupported') return { status: 501, body: '{"error":"not implemented: no Wi-Fi access control"}' };
      // The core answers canonical (lower case) MACs.
      return { status: 200, body: JSON.stringify(denied.map((m) => m.toLowerCase())) };
    }
    const fw = /^\/api\/devices\/([^/]+)\/(block|unblock)$/.exec(path);
    if (method === 'POST' && fw) {
      const mac = decodeURIComponent(fw[1]).toLowerCase();
      const d = opts.devices.find((x) => x.MAC.toLowerCase() === mac);
      if (d) d.Blocked = fw[2] === 'block';
      if (fw[2] === 'block' && body && JSON.parse(body).cut_wifi && denied !== 'unsupported') denied = [...denied, mac];
      return { status: 200, body: '{}' };
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

// #108 (RFC-007 D5): « Couper l’accès » became the switch « Ne plus le
// laisser se connecter du tout (même au Wi-Fi) », folded under « Bloqué »
// (#111 R7). No question first: an « Annuler » follows (R6).
const openMore = async (r: Root) => {
  await settle(() => !!byId(r, 'device-trust-more'));
  await press(byId(r, 'device-trust-more'));
  await settle(() => !!byId(r, 'device-trust-cut-row'));
};

test('with Basic, the switch under « Bloqué » blocks and refuses at the Wi-Fi too', async () => {
  box({ devices: [device(PC, 'PC-Bureau')], denied: [] });
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-trust-blocked'));
  expect(text(r)).not.toContain('Couper l’accès');
  expect(byId(r, 'device-toggle-wifi-cut')).toBeUndefined();
  await openMore(r);
  expect(text(r)).toContain('Ne plus le laisser se connecter du tout (même au Wi-Fi)');
  await press(byId(r, 'device-trust-cut-row'));
  // Only a choice for now: « Bloqué » sends it.
  expect(posts()).toEqual([]);
  await press(byId(r, 'device-trust-blocked'));
  await settle(() => posts().length > 0);
  expect(alert).not.toHaveBeenCalled();
  expect(posts()).toEqual([`/api/devices/${ENC(PC)}/block`]);
  const call = request.mock.calls.find(([m, p]) => m === 'POST' && p === `/api/devices/${ENC(PC)}/block`)!;
  expect(JSON.parse(call[2])).toEqual({ cut_wifi: true });
  await settle(() => !!byId(r, 'device-cut-badge'));
  expect(texts(byId(r, 'device-cut-badge'))).toEqual(['Ne peut plus se connecter']);
});

test('without Basic, the switch shows the neutral sentence and reaches nothing', async () => {
  tier('free');
  box({ devices: [device(PC, 'PC-Bureau')], denied: [] });
  const r = await render(<DeviceScreen />);
  await openMore(r);
  expect(text(r)).toContain('Pas disponible pour votre compte');
  await press(byId(r, 'device-trust-cut-row'));
  expect(alert).toHaveBeenCalledTimes(1);
  expect(alert.mock.calls[0][1]).toBe(NEUTRAL);
  expect(posts()).toEqual([]);
  expect(mockPush).not.toHaveBeenCalled();
});

test('letting a refused device back is free: « À vérifier » with no pass at all', async () => {
  tier('free');
  box({ devices: [device(PC, 'PC-Bureau')], denied: [PC] });
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-cut-badge'));
  expect(texts(byId(r, 'device-blocked-badge')).join('')).toContain('Bloqué');
  await press(byId(r, 'device-trust-check'));
  await settle(() => posts().length > 1);
  expect(alert).not.toHaveBeenCalled();
  // An old « Couper l’accès » the core does not track: its refusal goes too.
  expect(posts()).toEqual([`/api/devices/${ENC(PC)}/unblock`, `/api/devices/${ENC(PC)}/wifi-unblock`]);
});

test('a cut device the box no longer lists still has a sheet to let it back', async () => {
  tier('free');
  mockMac = PHONE;
  box({ devices: [device(PC, 'PC-Bureau')], denied: [PHONE] });
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-trust-check'));
  expect(text(r)).not.toContain('Appareil introuvable');
  expect(texts(byId(r, 'device-cut-badge'))).toEqual(['Ne peut plus se connecter']);
  await press(byId(r, 'device-trust-check'));
  await settle(() => posts().length > 1);
  expect(posts()).toEqual([`/api/devices/${ENC(PHONE)}/unblock`, `/api/devices/${ENC(PHONE)}/wifi-unblock`]);
});

test('a box without Wi-Fi access control offers « Bloqué » without the switch', async () => {
  box({ devices: [device(PC, 'PC-Bureau')], denied: 'unsupported' });
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-trust-blocked'));
  await settle();
  expect(byId(r, 'device-trust-more')).toBeUndefined();
  expect(byId(r, 'device-trust-cut')).toBeUndefined();
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
  expect(text(r)).toContain('Appareils (1)');
  expect(text(r)).toContain('Bloqués (2)');
  expect(text(r)).not.toContain('Tel-Inconnu');
  expect(text(r)).not.toContain('PC-Bureau');
  expect(byId(r, 'devices-cut-section')).toBeUndefined();

  // Blocked: the filter's blocks (definitive) and the Wi-Fi cuts, apart.
  await press(byId(r, 'devices-filter-blocked'));
  await settle(() => !!byId(r, 'devices-blocked-section'));
  const blocked = texts(byId(r, 'devices-blocked-section')).join('\n');
  expect(blocked).toContain('Bloqués · pas d’internet (1)');
  expect(blocked).toContain('Tel-Inconnu');
  const cut = texts(byId(r, 'devices-cut-section')).join('\n');
  expect(cut).toContain('Bloqués · ne peuvent plus se connecter (1)');
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
