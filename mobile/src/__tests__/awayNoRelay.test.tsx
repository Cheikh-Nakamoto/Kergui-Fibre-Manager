/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Away from home without a relay (#124, audit M1): the box does not answer
// and the household has no relay. Instead of an amber « Box injoignable »
// over an empty list, the Devices tab says where the phone is, what a relay
// is for, offers « Installer un relais », and shows the last list it read,
// dated. That list is kept on the phone between launches: names and states
// only, with the time it was read.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() },
  Stack: { Screen: () => null },
  useLocalSearchParams: () => ({}),
}));
const mockStore = new Map<string, string>();
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async (k: string) => mockStore.get(k) ?? null,
  setItemAsync: async (k: string, v: string) => void mockStore.set(k, v),
  deleteItemAsync: async (k: string) => void mockStore.delete(k),
}));
jest.mock('../alerts/alerts', () => ({ checkNewDevices: async () => 0, enforceAutoReject: async () => 0 }));
jest.mock('../features/relayPolicy', () => ({ syncRelayPolicy: async () => undefined }));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));

import { router } from 'expo-router';

import KerguiCore from '../../modules/kergui-core';
import Devices from '../app/(tabs)/devices';
import { LAST_LIST_KEY } from '../features/lastList';
import { t } from '../i18n';
import { useAccount } from '../licensing/account';
import { useSession } from '../session/store';

const request = KerguiCore.request as jest.Mock;

function texts(node: TestInstance): string[] {
  const out: string[] = [];
  for (const c of node.children) out.push(...(typeof c === 'string' ? [c] : texts(c)));
  return out;
}

const iconAsText = ({ name }: { name: string }) => <Text>{`icon:${name}`}</Text>;
let root: Root | null = null;
let qc: QueryClient | null = null;

async function settle(until: () => boolean = () => false) {
  for (let i = 0; i < 50 && !until(); i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 10))));
}

async function render() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false, gcTime: 0 } } });
  qc = client;
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={client}>
        <PaperProvider settings={{ icon: iconAsText }}>
          <Devices />
        </PaperProvider>
      </QueryClientProvider>,
    );
  });
}

const all = (id: string) => root!.container.queryAll((n) => n.props.testID === id);
const text = () => texts(root!.container).join('\n');
const press = async (id: string) => {
  const node = all(id).find((n) => typeof n.props.onClick === 'function' || typeof n.props.onPress === 'function');
  if (!node) throw new Error(`no ${id}`);
  const ev = { nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined };
  await act(async () => void (node.props.onClick ?? node.props.onPress)(ev));
};

const device = (mac: string, name: string, extra: object = {}) => ({
  MAC: mac,
  IP: '192.168.1.42',
  Hostname: name,
  CustomName: '',
  Vendor: '',
  SSID: 'MonWiFi-2G',
  Connected: true,
  Blocked: false,
  NewlySeen: false,
  ...extra,
});

/** Away: nothing on the box's address answers. */
function away(body = '{"error":"router unreachable"}') {
  request.mockImplementation(async () => ({ status: 502, body }));
}

/** At home: the box lists these devices. */
function home(devices: object[]) {
  request.mockImplementation(async (method: string, path: string) => {
    if (method === 'GET' && path === '/api/devices') return { status: 200, body: JSON.stringify(devices) };
    if (method === 'GET' && path === '/api/wifi/blocked') return { status: 200, body: '[]' };
    return { status: 501, body: '{"error":"not implemented"}' };
  });
}

/** Yesterday at 21 h 05, in the phone's time. */
function yesterdayAt2105(): Date {
  const d = new Date();
  d.setDate(d.getDate() - 1);
  d.setHours(21, 5, 0, 0);
  return d;
}

beforeEach(() => {
  mockStore.clear();
  request.mockReset();
  (router.push as jest.Mock).mockReset();
  useSession.setState({ session: { mode: 'router', routerUrl: 'https://192.168.1.1', pin: 'ab:cd' } });
  // Not signed in: no household, so no relay.
  useAccount.setState({ account: null });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = null;
});

test('away without a relay: the words, « Installer un relais », and no amber error', async () => {
  away();
  await render();
  await settle(() => all('devices-away').length > 0);
  expect(all('devices-away').length).toBeGreaterThan(0);
  const s = text();
  expect(s).toContain('Vous n’êtes pas à la maison. Kergui voit la box seulement sur son Wi-Fi. Pour la voir de partout, installez un relais.');
  expect(s).not.toContain(t('error.unreachable'));
  // Nothing was ever read on this phone: no list, and no date made up.
  expect(all('devices-away-last')).toHaveLength(0);
  expect(s).not.toMatch(/Liste d/);

  await press('devices-away-install-relay');
  expect(router.push).toHaveBeenCalledWith('/relay');
});

test('away without a relay: the last list read, dated, names and states only', async () => {
  mockStore.set(
    LAST_LIST_KEY,
    JSON.stringify({
      at: yesterdayAt2105().toISOString(),
      devices: [
        { name: 'Laptop-Bureau', connected: true, blocked: false },
        { name: 'TV-Salon', connected: false, blocked: false },
        { name: 'Phone-Salon', connected: true, blocked: true },
      ],
    }),
  );
  away();
  await render();
  await settle(() => all('devices-away-last').length > 0);
  const s = text();
  expect(s).toContain('Liste d’hier à 21 h 05');
  expect(s).toContain('Laptop-Bureau');
  expect(s).toContain('TV-Salon');
  expect(s).toContain('Hors ligne');
  expect(s).toContain('Phone-Salon');
  expect(s).toContain('Bloqué');
});

test('at home, the list read is kept with its time: names and states, never a MAC nor an IP', async () => {
  home([
    device('ac:bb:cc:00:00:22', 'Laptop-Bureau'),
    device('ac:bb:cc:00:00:33', 'TV-Salon', { Connected: false }),
    // This phone stays out, as it does out of the list.
    device('fa:4b:47:00:00:86', 'Mon-Telephone', { Mark: 'Self' }),
  ]);
  const before = Date.now();
  await render();
  await settle(() => mockStore.has(LAST_LIST_KEY));
  const raw = mockStore.get(LAST_LIST_KEY)!;
  expect(raw).not.toContain('ac:bb:cc');
  expect(raw).not.toContain('192.168');
  const kept = JSON.parse(raw) as { at: string; devices: { name: string; connected: boolean; blocked: boolean }[] };
  expect(new Date(kept.at).getTime()).toBeGreaterThanOrEqual(before - 1000);
  expect(kept.devices).toEqual([
    { name: 'Laptop-Bureau', connected: true, blocked: false },
    { name: 'TV-Salon', connected: false, blocked: false },
  ]);
  expect(all('devices-away')).toHaveLength(0);
});

test('another box error keeps its banner: only « not there » is « not at home »', async () => {
  away('{"error":"router unreachable: router certificate does not match the pinned fingerprint: got ab"}');
  await render();
  await settle(() => text().includes(t('error.pin')));
  expect(text()).toContain(t('error.pin'));
  expect(all('devices-away')).toHaveLength(0);
});
