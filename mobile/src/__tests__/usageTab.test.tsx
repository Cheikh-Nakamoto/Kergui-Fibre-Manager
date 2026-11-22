/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The Conso tab (#32, free): the current month per device, most first, with
// sent / received, the device list's search, and the honest "minimum" label.
// Each foreground refresh samples the box's counters.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

const mockPush = jest.fn();
jest.mock('expo-router', () => ({
  router: { push: (...a: unknown[]) => mockPush(...a), replace: jest.fn(), back: jest.fn() },
}));
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('../alerts/alerts', () => ({ checkNewDevices: async () => 0, enforceAutoReject: async () => 0 }));

import KerguiCore from '../../modules/kergui-core';
import UsageTab from '../app/(tabs)/usage';
import { fr } from '../i18n/fr';

const request = KerguiCore.request as jest.Mock;
const PC = 'AC:BB:CC:00:00:22';
const PHONE = 'AC:BB:CC:00:00:33';
const TV = 'AC:BB:CC:00:00:44';

const device = (mac: string, name: string) => ({
  ID: mac, MAC: mac, IP: '192.168.1.10', Hostname: name, CustomName: '', Vendor: '', SSID: '',
  FirstSeen: '', LastSeen: '', Connected: true, Blocked: false, NewlySeen: false,
});

function box(opts: { counters: boolean }) {
  request.mockImplementation(async (method: string, path: string) => {
    if (method === 'GET' && path === '/api/devices') {
      return { status: 200, body: JSON.stringify([device(PC, 'PC-Bureau'), device(PHONE, 'Phone-Salon'), device(TV, 'TV-Salon')]) };
    }
    if (method === 'POST' && path === '/api/usage/sample') {
      return opts.counters
        ? { status: 200, body: '{"devices":3,"bytes":0,"baseline":false}' }
        : { status: 501, body: '{"error":"not implemented: adapter zte_f660 cannot measure traffic"}' };
    }
    if (method === 'GET' && path === '/api/usage') {
      const rows = opts.counters
        ? [
            { mac: PHONE.toLowerCase(), period: '2026-11', bytes: 2_500_000_000, sent: 500_000_000, received: 2_000_000_000 },
            { mac: PC.toLowerCase(), period: '2026-11', bytes: 12_000_000, sent: 2_000_000, received: 10_000_000 },
          ]
        : [];
      return { status: 200, body: JSON.stringify(rows) };
    }
    return { status: 404, body: '{"error":"not found"}' };
  });
}

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
  qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc!}>
        <PaperProvider settings={{ icon: iconAsText }}>
          <UsageTab />
        </PaperProvider>
      </QueryClientProvider>,
    );
  });
  return root;
}

const byId = (r: Root, id: string) => r.container.queryAll((n) => n.props.testID === id)[0];
const rowIds = (r: Root) =>
  r.container.queryAll((n) => typeof n.props.testID === 'string' && n.props.testID.startsWith('usage-row-')).map((n) => n.props.testID as string);
const uniq = (a: string[]) => a.filter((v, i) => a.indexOf(v) === i);
const text = (r: Root) => texts(r.container).join('\n');

// The sampler reaches the box at most every 10 s: each test starts a minute
// after the previous one.
let clock = Date.now();
let nowSpy: jest.SpyInstance;
beforeEach(() => {
  request.mockReset();
  mockPush.mockReset();
  clock += 60_000;
  nowSpy = jest.spyOn(Date, 'now').mockImplementation(() => clock);
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = qc = null;
  nowSpy.mockRestore();
});

test('lists the month most first, with sent / received, and says it is a minimum', async () => {
  box({ counters: true });
  const r = await render();
  await settle(() => text(r).includes('2,5 Go'));
  expect(uniq(rowIds(r))).toEqual([`usage-row-${PHONE}`, `usage-row-${PC}`, `usage-row-${TV}`]);
  expect(text(r)).toContain('↑ 500 Mo envoyés · ↓ 2 Go reçus');
  expect(text(r)).toContain('12 Mo');
  expect(text(r)).toContain('Ce mois-ci : 2,5 Go pour toute la maison');
  expect(byId(r, 'usage-estimate')).toBeDefined();
  expect(text(r)).toContain(fr['usage.estimate']);
  expect(fr['usage.estimate']).toMatch(/pendant que Kergui tourne/);
  // The refresh sampled the box before reading the month.
  expect(request.mock.calls.some(([m, p]) => m === 'POST' && p === '/api/usage/sample')).toBe(true);
});

test('the search filters by name like the device list', async () => {
  box({ counters: true });
  const r = await render();
  await settle(() => text(r).includes('2,5 Go'));
  await act(async () => byId(r, 'usage-search').props.onChangeText('bureau'));
  expect(uniq(rowIds(r))).toEqual([`usage-row-${PC}`]);
  await act(async () => byId(r, 'usage-search').props.onChangeText('cuisine'));
  expect(rowIds(r)).toEqual([]);
  expect(text(r)).toContain(fr['usage.noMatch']);
});

test('a box without counters still lists its devices, and says it cannot measure', async () => {
  box({ counters: false });
  const r = await render();
  await settle(() => !!byId(r, 'usage-unsupported'));
  expect(text(r)).toContain(fr['usage.unsupported']);
  expect(uniq(rowIds(r)).length).toBe(3);
  expect(byId(r, 'usage-estimate')).toBeDefined();
});
