/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Usage history (#33, Max): the Conso tab's month selector and the device
// sheet's 6-month curve. Without Max they show the lock and the one neutral
// sentence (RFC-006: no payment surface), and ask the core nothing; the demo
// opens them.
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
import UsageTab from '../app/(tabs)/usage';
import DeviceScreen from '../app/device/[mac]';
import { fr } from '../i18n/fr';
import { useEntitlement } from '../licensing/entitlement';
import { monthLabel, periodOf, shiftPeriod } from '../lib/usage';
import { useSession } from '../session/store';

const request = KerguiCore.request as jest.Mock;
const PC = 'AC:BB:CC:00:00:22';
const NOT_ENTITLED = JSON.stringify({ error: 'not entitled', code: 'not_entitled', feature: 'consumption_history', required: 'max' });

const device = (mac: string, name: string) => ({
  ID: mac, MAC: mac, IP: '192.168.1.10', Hostname: name, CustomName: '', Vendor: '', SSID: '',
  FirstSeen: '', LastSeen: '', Connected: true, Blocked: false, NewlySeen: false,
});

const thisMonth = () => periodOf(new Date(Date.now()));

function box(opts: { history?: 'ok' | 'refused' } = {}) {
  request.mockImplementation(async (method: string, path: string) => {
    if (method === 'GET' && path === '/api/devices') return { status: 200, body: JSON.stringify([device(PC, 'Laptop-Bureau')]) };
    if (method === 'GET' && path === '/api/wifi/blocked') return { status: 200, body: '[]' };
    if (method === 'POST' && path === '/api/usage/sample') return { status: 200, body: '{"devices":1,"bytes":0,"baseline":false}' };
    if (method === 'GET' && path.startsWith('/api/usage/devices/')) {
      if (opts.history === 'refused') return { status: 403, body: NOT_ENTITLED };
      const rows = [-5, -4, -3, -2, -1, 0].map((d, i) => ({
        mac: PC.toLowerCase(), period: shiftPeriod(thisMonth(), d), bytes: i === 2 ? 0 : (i + 1) * 1_000_000_000, sent: 0, received: 0,
      }));
      return { status: 200, body: JSON.stringify(rows) };
    }
    if (method === 'GET' && path.startsWith('/api/usage')) {
      const past = path.includes('period=');
      return { status: 200, body: JSON.stringify([{ mac: PC.toLowerCase(), period: thisMonth(), bytes: past ? 7_000_000_000 : 12_000_000, sent: 0, received: 0 }]) };
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

async function render(ui: React.ReactElement) {
  qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false, gcTime: 0 } } });
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc!}>
        <PaperProvider settings={{ icon: iconAsText }}>{ui}</PaperProvider>
      </QueryClientProvider>,
    );
  });
  return root;
}

const byId = (r: Root, id: string) => r.container.queryAll((n) => n.props.testID === id)[0];
const text = (r: Root) => texts(r.container).join('\n');
const press = async (node: TestInstance) =>
  act(async () => void node.props.onClick({ nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined }));
const asked = (prefix: string) => request.mock.calls.some(([m, p]) => m === 'GET' && (p as string).startsWith(prefix));

function as(who: 'demo' | 'free' | 'max') {
  const now = Math.floor(Date.now() / 1000);
  useSession.setState({
    session: who === 'demo' ? { mode: 'demo' } : { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' },
  });
  useEntitlement.setState(
    who === 'max'
      ? {
          boxId: 'ZTEG00000001',
          token: 'x.y',
          claims: { account: 'acc_1', boxId: 'ZTEG00000001', tier: 'max', expiresAt: now + 86400, issuedAt: now, keyId: 'k' },
        }
      : { boxId: 'ZTEG00000001', token: null, claims: null },
  );
}

// The sampler reaches the box at most every 10 s: each test starts a minute
// after the previous one.
let clock = Date.now();
let nowSpy: jest.SpyInstance;
let alert: jest.SpyInstance;
beforeEach(() => {
  request.mockReset();
  mockPush.mockReset();
  clock += 60_000;
  nowSpy = jest.spyOn(Date, 'now').mockImplementation(() => clock);
  alert = jest.spyOn(Alert, 'alert').mockImplementation(() => undefined);
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = qc = null;
  nowSpy.mockRestore();
  alert.mockRestore();
});

test('Max picks a past month in the Conso tab, and comes back to this one', async () => {
  as('max');
  box();
  const r = await render(<UsageTab />);
  await settle(() => text(r).includes('12 Mo'));
  expect(texts(byId(r, 'usage-month')).join('')).toBe(monthLabel(thisMonth()));
  // Nothing after this month.
  expect(byId(r, 'usage-month-next').props.accessibilityState?.disabled).toBe(true);

  await press(byId(r, 'usage-month-prev'));
  const last = shiftPeriod(thisMonth(), -1);
  await settle(() => text(r).includes('7 Go'));
  expect(request.mock.calls.some(([m, p]) => m === 'GET' && p === `/api/usage?period=${last}`)).toBe(true);
  expect(texts(byId(r, 'usage-month')).join('')).toBe(monthLabel(last));
  expect(text(r)).toContain(`En ${monthLabel(last)} : 7 Go pour toute la maison`);
  expect(byId(r, 'usage-month-past')).toBeDefined();

  await press(byId(r, 'usage-month-next'));
  await settle(() => text(r).includes('12 Mo'));
  expect(text(r)).toContain('Ce mois-ci : 12 Mo pour toute la maison');
  expect(byId(r, 'usage-month-past')).toBeUndefined();
});

test('the month selector stops 12 months back: 13 months are kept', async () => {
  as('demo');
  box();
  const r = await render(<UsageTab />);
  await settle(() => text(r).includes('12 Mo'));
  for (let i = 0; i < 15; i++) await press(byId(r, 'usage-month-prev'));
  expect(texts(byId(r, 'usage-month')).join('')).toBe(monthLabel(shiftPeriod(thisMonth(), -12)));
  expect(byId(r, 'usage-month-prev').props.accessibilityState?.disabled).toBe(true);
  // 15 presses re-render the tab each time: slow on a busy machine or in CI.
}, 20_000);

test('without Max the month selector shows the lock and the neutral sentence, and asks nothing', async () => {
  as('free');
  box();
  const r = await render(<UsageTab />);
  await settle(() => text(r).includes('12 Mo'));
  expect(text(r)).toContain('icon:lock');
  await press(byId(r, 'usage-month-prev'));
  expect(alert).toHaveBeenCalledWith(fr['usage.prevMonth'], fr['pro.locked'], expect.anything());
  expect(request.mock.calls.some(([, p]) => (p as string).includes('period='))).toBe(false);
  expect(texts(byId(r, 'usage-month')).join('')).toBe(monthLabel(thisMonth()));
});

test('the device sheet shows its last 6 months in the demo', async () => {
  as('demo');
  box();
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-usage-chart'));
  expect(text(r)).toContain(fr['device.usageTitle']);
  expect(request.mock.calls.some(([m, p]) => m === 'GET' && p === `/api/usage/devices/${encodeURIComponent(PC)}?months=6`)).toBe(true);
  for (let d = -5; d <= 0; d++) expect(byId(r, `device-usage-bar-${shiftPeriod(thisMonth(), d)}`)).toBeDefined();
  // The busiest month is the last one: 6 Go.
  expect(text(r)).toContain('6 Go');
  expect(byId(r, 'locked')).toBeUndefined();
});

test('without Max the device sheet shows the lock instead of the curve, and asks nothing', async () => {
  as('free');
  box();
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-usage-locked'));
  expect(text(r)).toContain(fr['pro.locked']);
  expect(byId(r, 'device-usage-chart')).toBeUndefined();
  expect(asked('/api/usage/devices/')).toBe(false);
});

test('a refusal by the core shows the lock too, never an error', async () => {
  as('max');
  box({ history: 'refused' });
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-usage-locked'));
  expect(text(r)).toContain(fr['pro.locked']);
  expect(byId(r, 'device-usage-chart')).toBeUndefined();
});
