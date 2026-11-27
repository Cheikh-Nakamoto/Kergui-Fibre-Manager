/* eslint-disable import/first -- the mocks must come before the modules they replace */
// #115 (audit C3): the Wi-Fi tab has no « Filtrage MAC » switch any more;
// Kergui keeps the box's filter on itself. In its place, « Débloquer tous les
// appareils bloqués ({n}) » lists the devices before confirming, gives them
// internet back (free, RFC-006) and lets the ones refused at the Wi-Fi join
// it again. It never touches a rule the household made in the box's page,
// nor this phone (#113).
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Alert, Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() },
  Stack: { Screen: () => null },
  useLocalSearchParams: () => ({}),
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
import WiFi from '../app/(tabs)/wifi';
import type { BlockedDevice } from '../core/types';
import { useEntitlement } from '../licensing/entitlement';
import { useSession } from '../session/store';

const request = KerguiCore.request as jest.Mock;

const PC = 'ac:bb:cc:00:00:22'; // on the network, blocked by kergui
const TAB = 'ac:bb:cc:00:00:44'; // away, blocked and refused at the Wi-Fi by kergui
const STRANGER = '02:aa:bb:00:00:01'; // away, never seen by the app
const THEIRS = 'ac:bb:cc:00:00:11'; // blocked in the box's own page
const SELF = 'fa:4b:47:00:00:86'; // this phone

const device = (mac: string, name: string, extra: object = {}) => ({
  MAC: mac,
  IP: '192.168.1.10',
  Hostname: name,
  CustomName: '',
  Vendor: '',
  SSID: 'MonWiFi-2G',
  Connected: true,
  Blocked: false,
  NewlySeen: false,
  ...extra,
});

const held = (mac: string, over: Partial<BlockedDevice> = {}): BlockedDevice => ({
  mac,
  name: '',
  last_seen: '',
  present: false,
  firewall: true,
  wifi: false,
  by_kergui: true,
  ...over,
});

const ALL: BlockedDevice[] = [
  held(PC, { name: 'Laptop-Bureau', present: true }),
  held(TAB, { name: 'Tablette de Awa', wifi: true, last_seen: '2026-11-20T10:00:00Z' }),
  held(STRANGER),
  held(THEIRS, { name: 'Phone-Salon', by_kergui: false }),
  held(SELF, { name: 'Mon-Telephone', present: true }),
];

/** The core as the Wi-Fi tab sees it: the networks, the devices, the box's lists. */
function box(blocked: BlockedDevice[] = ALL) {
  let list = [...blocked];
  const devices = [
    device(PC, 'Laptop-Bureau', { Blocked: true }),
    device(THEIRS, 'Phone-Salon', { Blocked: true }),
    device(SELF, 'Mon-Telephone', { Mark: 'Self' }),
  ];
  const ssids = [{ id: 'DEV.WIFI.AP1', alias: 'SSID1', band: '2.4GHz', name: 'MonWiFi-2G', enabled: true, hidden: false, security: 'WPA2' }];
  request.mockImplementation(async (method: string, path: string) => {
    if (method === 'GET' && path === '/api/wifi/ssids') return { status: 200, body: JSON.stringify(ssids) };
    if (method === 'GET' && path === '/api/devices') return { status: 200, body: JSON.stringify(devices) };
    if (method === 'GET' && path === '/api/blocked') return { status: 200, body: JSON.stringify({ devices: list }) };
    if (method === 'POST' && path === '/api/usage/sample') return { status: 200, body: '{}' };
    const m = /^\/api\/devices\/([^/]+)\/(unblock|wifi-unblock)$/.exec(path);
    if (method === 'POST' && m) {
      const mac = decodeURIComponent(m[1]);
      list = list
        .map((b) => (b.mac === mac ? { ...b, firewall: m[2] === 'unblock' ? false : b.firewall, wifi: m[2] === 'wifi-unblock' ? false : b.wifi } : b))
        .filter((b) => b.firewall || b.wifi);
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

async function render(until: (r: Root) => boolean = (r) => !!byId(r, 'wifi-unblock-all')) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false, gcTime: 0 } } });
  qc = client;
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={client}>
        <PaperProvider settings={{ icon: iconAsText }}>
          <WiFi />
        </PaperProvider>
      </QueryClientProvider>,
    );
  });
  await settle(() => !!byId(root!, 'wifi-ssid-DEV.WIFI.AP1') && until(root!));
  return root;
}

const byId = (r: Root, id: string) => r.container.queryAll((n) => n.props.testID === id)[0];
const text = (r: Root) => texts(r.container).join('\n');
const press = async (r: Root, id: string) => {
  const node = byId(r, id);
  if (!node) throw new Error(`no ${id}`);
  await act(async () => void node.props.onClick({ nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined }));
};
/** What was sent to the box: every POST but the usage sample. */
const writes = () =>
  request.mock.calls
    .filter(([m, p]) => m === 'POST' && p !== '/api/usage/sample')
    .map(([, p]) => decodeURIComponent(p as string));

let alert: jest.SpyInstance;
const sayYes = () => alert.mockImplementation((_t, _m, buttons?: { onPress?: () => void }[]) => buttons?.[1]?.onPress?.());
const sayNo = () => alert.mockImplementation((_t, _m, buttons?: { onPress?: () => void }[]) => buttons?.[0]?.onPress?.());

beforeEach(() => {
  request.mockReset();
  alert = jest.spyOn(Alert, 'alert').mockImplementation(() => undefined);
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
  // No pass: giving access back is free (RFC-006).
  useEntitlement.setState({ boxId: 'ZTEG00000001', token: null, claims: null });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = qc = null;
  alert.mockRestore();
});

test('the Wi-Fi tab has no filter switch, and never asks the box for it', async () => {
  box();
  const r = await render();
  expect(text(r)).not.toMatch(/filtrage/i);
  expect(r.container.queryAll((n) => /filtrage/i.test(String(n.props.accessibilityLabel ?? '')))).toEqual([]);
  expect(request.mock.calls.some(([, p]) => String(p).startsWith('/api/filter'))).toBe(false);
});

test('« Débloquer tous » counts the devices kergui blocked, never the household’s rules nor this phone', async () => {
  box();
  const r = await render();
  expect(text(r)).toContain('Débloquer tous les appareils bloqués (3)');
  // The household's own rules stay, and the screen says so.
  expect(text(r)).toContain('Phone-Salon');
});

test('it lists the devices before confirming, then gives each its access back, for free', async () => {
  box();
  const r = await render();
  sayYes();
  await press(r, 'wifi-unblock-all');
  expect(alert).toHaveBeenCalledTimes(1);
  const [, message, buttons] = alert.mock.calls[0];
  expect(message).toContain('Les 3 appareils bloqués retrouveront internet tout de suite. Continuer ?');
  expect(message).toContain('Laptop-Bureau');
  expect(message).toContain('Tablette de Awa');
  expect(message).toContain(STRANGER);
  // The Wi-Fi refusal goes too, and the list says so before confirming.
  expect(message).toMatch(/Tablette de Awa.*refusé au Wi-Fi/);
  expect(message).toContain('L’appareil refusé au Wi-Fi pourra de nouveau se connecter au Wi-Fi.');
  expect(message).not.toContain('Phone-Salon');
  expect(message).not.toContain('Mon-Telephone');
  expect(buttons[1].text).toBe('Oui, débloquer');

  await settle(() => !!byId(r, 'wifi-unblock-all-done'));
  expect(writes().sort()).toEqual(
    [`/api/devices/${PC}/unblock`, `/api/devices/${TAB}/unblock`, `/api/devices/${TAB}/wifi-unblock`, `/api/devices/${STRANGER}/unblock`].sort(),
  );
  expect(text(r)).toContain('3 appareils débloqués ✅');
  await settle(() => !byId(r, 'wifi-unblock-all'));
  expect(byId(r, 'wifi-unblock-all')).toBeUndefined();
});

test('cancelling sends nothing', async () => {
  box();
  const r = await render();
  sayNo();
  await press(r, 'wifi-unblock-all');
  await settle();
  expect(writes()).toEqual([]);
});

test('one device: the sentence is in the singular', async () => {
  box([held(PC, { name: 'Laptop-Bureau', present: true })]);
  const r = await render();
  expect(text(r)).toContain('Débloquer tous les appareils bloqués (1)');
  await press(r, 'wifi-unblock-all');
  expect(alert.mock.calls[0][1]).toContain('L’appareil bloqué retrouvera internet tout de suite. Continuer ?');
});

test('with nothing kergui blocked, there is nothing to offer', async () => {
  box([held(THEIRS, { name: 'Phone-Salon', by_kergui: false })]);
  const r = await render((x) => text(x).includes('Phone-Salon'));
  expect(byId(r, 'wifi-unblock-all')).toBeUndefined();
});
