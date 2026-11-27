/* eslint-disable import/first -- the mocks must come before the modules they replace */
// This phone (#86, parent #69): the core marks the device that is the phone
// running the app "Self". The Devices tab leaves it out of the list, the
// counts and the filter tabs, and says so with one discreet line under the
// list, so nobody thinks it was forgotten.
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
import { arrivals } from '../alerts/diff';
import type { Device } from '../core/types';
import { useSession } from '../session/store';

const request = KerguiCore.request as jest.Mock;
const SELF = 'fa:4b:47:00:00:86';

const device = (mac: string, name: string, extra: Partial<Device> = {}) => ({
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

const PC = device('ac:bb:cc:00:00:22', 'Laptop-Bureau');
const TV = device('ac:bb:cc:00:00:33', 'TV-Salon', { NewlySeen: true });
const SALON = device('ac:bb:cc:00:00:11', 'Phone-Salon', { Blocked: true });
// The core marks it Self, approved and never new; NewlySeen and Blocked are
// set here only to show they are not counted either.
const ME = device(SELF, 'Mon-Telephone', { Mark: 'Self', Approved: true, NewlySeen: true });

function box(devices: object[]) {
  request.mockImplementation(async (method: string, path: string) => {
    if (method === 'GET' && path === '/api/devices') return { status: 200, body: JSON.stringify(devices) };
    if (method === 'GET' && path === '/api/wifi/blocked') return { status: 200, body: '[]' };
    if (method === 'POST' && path === '/api/usage/sample') return { status: 200, body: '{}' };
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
  await settle(() => text(root!).includes('Laptop-Bureau'));
  return root;
}

const byId = (r: Root, id: string) => r.container.queryAll((n) => n.props.testID === id)[0];
const text = (r: Root) => texts(r.container).join('\n');

beforeEach(() => {
  request.mockReset();
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = qc = null;
});

test('this phone is left out of the list, the counts and the filter tabs', async () => {
  box([PC, TV, SALON, ME]);
  const r = await render();
  expect(text(r)).not.toContain('Mon-Telephone');
  expect(byId(r, `device-${SELF}`)).toBeUndefined();
  // Laptop and TV connected (the blocked Phone-Salon is listed connected by
  // the box too): this phone is not one of them, nor one of the new ones.
  expect(text(r)).toContain('3 connectés · 1 bloqué · 1 nouveau');
  expect(text(r)).toContain('Appareils (2)');
  expect(text(r)).toContain('Bloqués (1)');
});

test('a discreet line under the list says this phone is hidden', async () => {
  box([PC, TV, ME]);
  const r = await render();
  const line = byId(r, 'devices-hidden');
  expect(line).toBeDefined();
  expect(texts(line).join('')).toBe('Ce téléphone n’est pas dans la liste : Kergui ne le bloque jamais.');
});

test('no hidden line when this phone is not on the box', async () => {
  box([PC, TV]);
  const r = await render();
  expect(byId(r, 'devices-hidden')).toBeUndefined();
  expect(text(r)).not.toContain('pas dans la liste');
});

test('this phone never rings, even when the core did not approve it', () => {
  const me = { ...ME, Approved: false } as Device;
  const r = arrivals([], [me, PC as Device]);
  expect(r.fresh.map((d) => d.MAC)).toEqual([PC.MAC]);
});

// The owner's phones (#87): the core marks them Owner, from the hub's list.
// They leave the list, the counts and the filter tabs like this phone, and
// the hidden line counts them.
const PAPA = device('fa:4b:47:00:00:87', 'Tel-Papa', { Mark: 'Owner', Approved: true });
const PAPA2 = device('fa:4b:47:00:00:88', 'Tel-Papa-2', { Mark: 'Owner', Approved: true, Blocked: true });

test("the owner's phones are left out of the list, the counts and the filter tabs", async () => {
  box([PC, TV, SALON, ME, PAPA, PAPA2]);
  const r = await render();
  expect(text(r)).not.toContain('Tel-Papa');
  expect(byId(r, `device-${PAPA.MAC}`)).toBeUndefined();
  expect(text(r)).toContain('3 connectés · 1 bloqué · 1 nouveau');
  expect(text(r)).toContain('Appareils (2)');
  expect(text(r)).toContain('Bloqués (1)');
});

test('the hidden line counts this phone and the owner’s phones', async () => {
  box([PC, ME, PAPA]);
  let r = await render();
  expect(texts(byId(r, 'devices-hidden')).join('')).toBe('Ce téléphone et 1 téléphone du propriétaire ne sont pas dans la liste : Kergui ne les bloque jamais.');
  await act(async () => root?.unmount());
  qc?.clear();

  box([PC, ME, PAPA, PAPA2]);
  r = await render();
  expect(texts(byId(r, 'devices-hidden')).join('')).toBe('Ce téléphone et 2 téléphones du propriétaire ne sont pas dans la liste : Kergui ne les bloque jamais.');
});

test('on a phone that is not on the box, the line counts the owner’s phones alone', async () => {
  box([PC, PAPA]);
  let r = await render();
  expect(texts(byId(r, 'devices-hidden')).join('')).toBe('1 téléphone du propriétaire n’est pas dans la liste : Kergui ne le bloque jamais.');
  await act(async () => root?.unmount());
  qc?.clear();

  box([PC, PAPA, PAPA2]);
  r = await render();
  expect(texts(byId(r, 'devices-hidden')).join('')).toBe('2 téléphones du propriétaire ne sont pas dans la liste : Kergui ne les bloque jamais.');
});

test("the owner's phone never rings", () => {
  const r = arrivals([], [{ ...PAPA, Approved: false } as Device, PC as Device]);
  expect(r.fresh.map((d) => d.MAC)).toEqual([PC.MAC]);
});
