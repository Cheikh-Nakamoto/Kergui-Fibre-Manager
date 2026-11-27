/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Devices named in words, the technical folded away (#121, audit H5): a
// type icon and a readable title on each row, never a MAC nor an IP; on the
// sheet, « Donnez-lui un nom » at the top of an unnamed device, « Adresse
// privée : peut changer » under a private address (RFC-007 D10), and MAC,
// IP and Wi-Fi in a « Détails techniques » section closed at the bottom.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { readFileSync } from 'fs';
import { join } from 'path';
import { act } from 'react';
import { Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

let mockMac = '02:4b:47:00:00:58';
const mockTitle = jest.fn();
jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() },
  Stack: { Screen: ({ options }: { options?: { title?: string } }) => (mockTitle(options?.title), null) },
  useLocalSearchParams: () => ({ mac: mockMac }),
}));
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('../alerts/alerts', () => ({ checkNewDevices: async () => 0, enforceAutoReject: async () => 0 }));
jest.mock('../licensing/config', () => ({ config: { hubUrl: '', publicKeys: [] } }));

import KerguiCore from '../../modules/kergui-core';
import Devices from '../app/(tabs)/devices';
import DeviceScreen from '../app/device/[mac]';
import { deviceIcon, deviceSubline, deviceTitle, guessDevice, isPrivateMac, seenLine } from '../lib/deviceName';
import { notificationName } from '../lib/format';
import { useSession } from '../session/store';

// --- The guess: the hub's words (internal/hub/alerts/usecase/name.go) ---

type Case = { host: string; noun: string; fem: boolean };
const shared: { cases: Case[] } = JSON.parse(
  readFileSync(join(__dirname, '../../../internal/hub/alerts/usecase/testdata/device_names.json'), 'utf8'),
);

test('the app guesses a device as the hub’s alerts do, from the same cases', () => {
  expect(shared.cases.length).toBeGreaterThan(10);
  for (const c of shared.cases) {
    const g = guessDevice(c.host);
    expect({ host: c.host, noun: g.noun, fem: g.fem }).toEqual(c);
  }
});

test('the maker the box gives (its OUI) helps when the name does not', () => {
  expect(guessDevice('', 'Samsung Electronics Co.,Ltd').noun).toBe('téléphone Samsung');
  expect(guessDevice('', 'Apple, Inc.').noun).toBe('appareil Apple');
});

// --- The title ---

const dev = (o: { CustomName?: string; Hostname?: string; Vendor?: string; MAC?: string }) => ({
  CustomName: '',
  Hostname: '',
  Vendor: '',
  MAC: 'aa:bb:cc:dd:ee:ff',
  ...o,
});

test('the title is the household’s name first', () => {
  expect(deviceTitle(dev({ CustomName: 'Télé de Mami', Hostname: 'android-3f2a9c01d4' }))).toBe('Télé de Mami');
});

test('a readable name from the box stays the title', () => {
  for (const h of ['Laptop-Bureau', 'TV-Salon', 'Phone-Salon', 'Awa-iPhone', 'TECNO-POP-10', 'Galaxy-A12']) {
    expect(deviceTitle(dev({ Hostname: h }))).toBe(h);
  }
});

test('a name made by a machine becomes a readable guess', () => {
  expect(deviceTitle(dev({ Hostname: 'android-3f2a9c01d4' }))).toBe('Téléphone Android');
  expect(deviceTitle(dev({ Hostname: 'SM-A125F' }))).toBe('Téléphone Samsung');
  expect(deviceTitle(dev({ Hostname: 'DESKTOP-4F2K9QX' }))).toBe('Ordinateur');
  expect(deviceTitle(dev({ Hostname: 'EPSON3C2AF1' }))).toBe('Imprimante Epson');
});

test('a device nothing tells about is « Appareil sans nom », never its MAC nor its IP', () => {
  for (const h of ['', 'ESP_1A2B3C', 'aa:bb:cc:dd:ee:ff', 'AABBCCDDEEFF', '192.168.1.57', 'localhost', '*']) {
    const title = deviceTitle(dev({ Hostname: h }));
    expect(title).toBe('Appareil sans nom');
  }
});

test('a notification names a device the same way, with its article', () => {
  expect(notificationName({ Hostname: 'SM-A125F' }, 'Un appareil')).toBe('Un téléphone Samsung');
  expect(notificationName({ Hostname: 'android-3f2a9c01d4' }, 'Un appareil')).toBe('Un téléphone Android');
  expect(notificationName({ Hostname: 'EPSON3C2AF1' }, 'Un appareil')).toBe('Une imprimante Epson');
  expect(notificationName({ Hostname: 'TECNO-POP-10' }, 'Un appareil')).toBe('TECNO-POP-10');
  expect(notificationName({ Hostname: '' }, 'Un appareil')).toBe('Un appareil');
  expect(notificationName({ CustomName: 'Tablette de Awa', Hostname: 'x' }, 'Un appareil')).toBe('Tablette de Awa');
});

// --- The icon ---

test('the icon says the type of device, and so does the screen reader', () => {
  expect(deviceIcon(dev({ Hostname: 'Phone-Salon' }))).toEqual({ icon: 'cellphone', label: 'Téléphone' });
  expect(deviceIcon(dev({ Hostname: 'TV-Salon' }))).toEqual({ icon: 'television', label: 'Télévision' });
  expect(deviceIcon(dev({ Hostname: 'Laptop-Bureau' }))).toEqual({ icon: 'laptop', label: 'Ordinateur' });
  expect(deviceIcon(dev({ Hostname: 'Galaxy-Tab-A' }))).toEqual({ icon: 'tablet', label: 'Tablette' });
  expect(deviceIcon(dev({ Hostname: '' }))).toEqual({ icon: 'devices', label: 'Type d’appareil non reconnu' });
  // An Apple device that says nothing more is not a phone.
  expect(deviceIcon(dev({ Hostname: 'Apple-1' })).label).toBe('Type d’appareil non reconnu');
});

// --- The sub-line ---

const NOW = new Date(2026, 10, 27, 10, 0);

test('« Vu … »: today, yesterday, or the day', () => {
  expect(seenLine(new Date(2026, 10, 27, 9, 5).toISOString(), NOW)).toBe('Vu aujourd’hui à 9 h 05');
  expect(seenLine(new Date(2026, 10, 26, 21, 0).toISOString(), NOW)).toBe('Vu hier à 21 h 00');
  expect(seenLine(new Date(2026, 8, 1, 8, 0).toISOString(), NOW)).toBe('Vu le 01/09/2026');
  expect(seenLine('', NOW)).toBeNull();
  expect(seenLine('nope', NOW)).toBeNull();
});

test('the row’s sub-line says « Hors ligne », and nothing it does not know', () => {
  // The box gives no time of connection: no « Connecté depuis » is made up.
  expect(deviceSubline({ Connected: true })).toBeNull();
  expect(deviceSubline({ Connected: false })).toBe('Hors ligne');
  expect(deviceSubline({ Connected: false }, true)).toBeNull();
});

// --- Private addresses (RFC-007 D10) ---

test('a locally administered address (bit 0x02 of the first octet) is private', () => {
  expect(isPrivateMac('02:4b:47:00:00:58')).toBe(true);
  expect(isPrivateMac('DA:A1:19:00:00:01')).toBe(true);
  expect(isPrivateMac('ac:bb:cc:00:00:22')).toBe(false);
  expect(isPrivateMac('00-11-22-33-44-55')).toBe(false);
  expect(isPrivateMac('')).toBe(false);
});

// --- On screen ---

const request = KerguiCore.request as jest.Mock;
const PRIVATE = '02:4b:47:00:00:58';
const PC = 'ac:bb:cc:00:00:22';

function device(mac: string, hostname: string, extra: Record<string, unknown> = {}) {
  return {
    MAC: mac,
    IP: mac === PC ? '192.168.1.11' : '192.168.1.58',
    Hostname: hostname,
    CustomName: '',
    Vendor: '',
    SSID: 'MonWiFi-2G',
    FirstSeen: '',
    LastSeen: '',
    Connected: true,
    Blocked: false,
    NewlySeen: false,
    ...extra,
  };
}

function core(devices: unknown[]) {
  request.mockImplementation(async (method: string, path: string) => {
    const ok = (b: unknown) => ({ status: 200, body: JSON.stringify(b) });
    if (method === 'GET' && path === '/api/devices') return ok(devices);
    if (method === 'GET' && path === '/api/approvals') return ok({ macs: [], unsynced: [] });
    if (method === 'GET' && path === '/api/autoreject') return ok({ enabled: false, since: '', delay_minutes: 15, devices: [] });
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
const text = (n: TestInstance) => texts(n).join('\n');
const press = async (node: TestInstance) =>
  act(async () => void node.props.onClick({ nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined }));

beforeEach(() => {
  request.mockReset();
  mockTitle.mockReset();
  mockMac = PRIVATE;
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = qc = null;
});

test('a row: the type icon, a readable title, no MAC nor IP', async () => {
  core([device(PC, 'Laptop-Bureau'), device(PRIVATE, 'android-3f2a9c01d4', { Connected: false })]);
  const r = await render(<Devices />);
  await settle(() => !!byId(r, `device-${PC}`));
  const pc = text(byId(r, `device-${PC}`));
  expect(pc).toContain('Laptop-Bureau');
  expect(pc).toContain('icon:laptop');
  const phone = text(byId(r, `device-${PRIVATE}`));
  expect(phone).toContain('Téléphone Android');
  expect(phone).toContain('icon:cellphone');
  expect(phone).toContain('Hors ligne');
  for (const leak of [PC, PRIVATE, '192.168.1.11', '192.168.1.58', 'android-3f2a']) {
    expect(text(r.container)).not.toContain(leak);
  }
  // The screen reader hears the type.
  expect(r.container.queryAll((n) => n.props.accessibilityLabel === 'Ordinateur').length).toBeGreaterThan(0);
});

test('the sheet of an unnamed device: its guess as title, « Donnez-lui un nom » on top, details closed', async () => {
  core([device(PRIVATE, 'android-3f2a9c01d4')]);
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-tech'));
  expect(mockTitle).toHaveBeenLastCalledWith('Téléphone Android');
  const all = text(r.container);
  expect(all).toContain('Donnez-lui un nom');
  expect(all).toContain('Adresse privée : peut changer');
  // The name field comes before the state block and the actions.
  const ids = r.container.queryAll((n) => typeof n.props.testID === 'string').map((n) => n.props.testID as string);
  expect(ids.indexOf('device-rename-input')).toBeLessThan(ids.indexOf('device-approve'));
  expect(ids.indexOf('device-tech')).toBeGreaterThan(ids.indexOf('device-toggle-block'));
  // Closed: no MAC, no IP, no Wi-Fi.
  expect(all).toContain('Détails techniques');
  expect(byId(r, 'device-details')).toBeUndefined();
  for (const leak of [PRIVATE, '192.168.1.58', 'MonWiFi-2G']) expect(all).not.toContain(leak);

  await press(byId(r, 'device-tech'));
  await settle(() => !!byId(r, 'device-details'));
  const details = text(byId(r, 'device-details'));
  expect(details).toContain(PRIVATE);
  expect(details).toContain('192.168.1.58');
  expect(details).toContain('MonWiFi-2G');
});

test('the sheet of a named device keeps its name below, and says nothing of a fixed address', async () => {
  mockMac = PC;
  core([device(PC, 'Laptop-Bureau', { CustomName: 'PC de Moussa', Approved: true })]);
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-tech'));
  expect(mockTitle).toHaveBeenLastCalledWith('PC de Moussa');
  const all = text(r.container);
  expect(all).not.toContain('Donnez-lui un nom');
  expect(all).not.toContain('Adresse privée');
  expect(all).toContain('Nom personnalisé');
});

test('a device the box no longer lists, cut off the Wi-Fi: never its MAC as the title', async () => {
  request.mockImplementation(async (method: string, path: string) => {
    const ok = (b: unknown) => ({ status: 200, body: JSON.stringify(b) });
    if (method === 'GET' && path === '/api/devices') return ok([]);
    if (method === 'GET' && path === '/api/wifi/blocked') return ok([PRIVATE]);
    return { status: 404, body: '{"error":"not found"}' };
  });
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-toggle-wifi-cut'));
  expect(mockTitle).toHaveBeenLastCalledWith('Appareil sans nom');
  expect(mockTitle).not.toHaveBeenCalledWith(PRIVATE);
  expect(byId(r, 'device-tech')).toBeDefined();
});
