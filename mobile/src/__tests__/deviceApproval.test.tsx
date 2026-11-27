/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Approvals (#58), worded « Autoriser » since #118: on the device sheet and on
// the notifications screen's signal of an arrival, the « Autorisé » badge,
// « Retirer l'autorisation », and a name given that authorises the device. The demo's visitor shows an
// arrival without a real one.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

const mockPush = jest.fn();
let mockMac = '02:4b:47:00:00:58';
let mockPending = false;
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
// No hub: the core is the household's list (approvals.test.ts covers the hub).
jest.mock('../licensing/config', () => ({ config: { hubUrl: '', publicKeys: [] } }));

import KerguiCore from '../../modules/kergui-core';
import { useArrivals } from '../alerts/arrivals';
import { useInbox } from '../alerts/inbox';
import Devices from '../app/(tabs)/devices';
import DeviceScreen from '../app/device/[mac]';
import Notifications from '../app/notifications';
import { useSession } from '../session/store';

const VISITOR = '02:4b:47:00:00:58';
const ENC = encodeURIComponent(VISITOR);
const request = KerguiCore.request as jest.Mock;

/** The phone's core, as approvals.go answers. */
function core() {
  const approved = new Set<string>();
  let name = '';
  request.mockImplementation(async (method: string, path: string, body: string) => {
    const ok = (b: unknown = {}) => ({ status: 200, body: JSON.stringify(b) });
    if (method === 'GET' && path === '/api/devices')
      return ok([
        {
          MAC: VISITOR,
          IP: '192.168.1.58',
          Hostname: 'Tablette-Visiteur',
          CustomName: name,
          Vendor: '',
          SSID: 'MonWiFi-2G',
          Connected: true,
          Blocked: false,
          NewlySeen: false,
          Approved: approved.has(VISITOR),
        },
      ]);
    if (method === 'GET' && path === '/api/approvals') return ok({ macs: [...approved], unsynced: [...approved] });
    if (method === 'PUT' && path === `/api/approvals/${ENC}`) return approved.add(VISITOR), ok();
    if (method === 'DELETE' && path === `/api/approvals/${ENC}`) return approved.delete(VISITOR), ok();
    if (method === 'PATCH' && path === `/api/devices/${ENC}`) {
      name = JSON.parse(body).custom_name;
      if (name) approved.add(VISITOR); // the core approves a name too
      return ok();
    }
    if (method === 'GET' && path === '/api/autoreject')
      return ok({
        enabled: mockPending,
        since: '',
        delay_minutes: 15,
        devices: mockPending ? [{ mac: VISITOR, state: 'pending', first_seen: '', deadline: new Date(2026, 10, 27, 14, 32).toISOString() }] : [],
      });
    if (method === 'POST' && path === '/api/demo/visitor') return ok(JSON.parse(body));
    if (method === 'POST' && path.endsWith('/accept')) return ok();
    return { status: 404, body: '{"error":"not found"}' };
  });
  return approved;
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
const calls = (method: string) => request.mock.calls.filter(([m]) => m === method).map(([, p]) => p as string);

beforeEach(() => {
  request.mockReset();
  mockPush.mockReset();
  mockMac = VISITOR;
  mockPending = false;
  useArrivals.getState().clear();
  useInbox.setState({ items: [], loaded: true });
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = qc = null;
});

test('« Autoriser » on the sheet authorises the device; « Retirer l’autorisation » undoes it', async () => {
  core();
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-approve'));
  expect(byId(r, 'device-not-approved-badge')).toBeDefined();
  expect(byId(r, 'device-approved-badge')).toBeUndefined();

  await press(byId(r, 'device-approve'));
  await settle(() => !!byId(r, 'device-approved-badge'));
  expect(calls('PUT')).toEqual([`/api/approvals/${ENC}`]);
  expect(text(r)).toContain('Autorisé');
  expect(byId(r, 'device-approve')).toBeUndefined();

  await press(byId(r, 'device-unapprove'));
  await settle(() => !!byId(r, 'device-approve'));
  expect(calls('DELETE')).toEqual([`/api/approvals/${ENC}`]);
  expect(byId(r, 'device-not-approved-badge')).toBeDefined();
});

test('giving the device a name authorises it, and the sheet says so', async () => {
  core();
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-rename-input'));
  await act(async () => byId(r, 'device-rename-input').props.onChangeText('Tablette de Awa'));
  await press(byId(r, 'device-rename-save'));
  await settle(() => !!byId(r, 'device-approved-badge'));
  expect(calls('PATCH')).toEqual([`/api/devices/${ENC}`]);
  expect(calls('PUT')).toContain(`/api/approvals/${ENC}`);
  expect(byId(r, 'device-approved-badge')).toBeDefined();
  await settle(() => !!byId(r, 'device-renamed'));
  expect(texts(byId(r, 'device-renamed')).join('')).toBe('Nommé et autorisé ✓');
});

test('under auto-reject, the one block carries the deadline and one « Autoriser » (#118)', async () => {
  core();
  mockPending = true;
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-pending-state'));
  expect(texts(byId(r, 'device-not-approved-badge')).join('')).toContain('À vérifier');
  expect(texts(byId(r, 'device-pending-state')).join('')).toBe('· sera refusé à 14:32 si personne ne l’autorise');
  const buttons = r.container.queryAll((n) => n.props.testID === 'device-approve' || n.props.testID === 'device-accept');
  expect(new Set(buttons.map((n) => n.props.testID))).toEqual(new Set(['device-approve']));
  expect(text(r)).not.toMatch(/Accepter|acceptation|approuv/i);

  await press(byId(r, 'device-approve'));
  await settle(() => !!byId(r, 'device-approved-badge'));
  // Authorised, and accepted under auto-reject: one gesture writes both.
  expect(calls('PUT')).toContain(`/api/approvals/${ENC}`);
  expect(calls('POST')).toContain(`/api/devices/${ENC}/accept`);
  expect(byId(r, 'device-pending-state')).toBeUndefined();
});

// Arrivals live in the notifications screen (the bell), not on the Devices
// tab, whose list stays where it is (owner request, 2026-11-23).
const ARRIVAL = { id: `arrival:${VISITOR}:1`, kind: 'arrival' as const, mac: VISITOR, name: 'Tablette-Visiteur', at: Date.now() };

test('the Devices tab shows no arrival card above the list', async () => {
  core();
  useArrivals.getState().add([{ mac: VISITOR, name: 'Tablette-Visiteur' }]);
  useInbox.getState().add([ARRIVAL]);
  const r = await render(<Devices />);
  await settle(() => text(r).includes('Tablette-Visiteur'));
  expect(byId(r, 'arrival-banner')).toBeUndefined();
  expect(text(r)).not.toContain('Appareil à vérifier');
});

test('the notifications screen signals an arrival nobody authorised, with « Autoriser »', async () => {
  core();
  useInbox.getState().add([ARRIVAL]);
  const r = await render(<Notifications />);
  await settle(() => !!byId(r, `inbox-approve-${VISITOR}`));
  expect(text(r)).toContain('Appareil à vérifier');

  await press(byId(r, `inbox-approve-${VISITOR}`));
  await settle(() => !!byId(r, `inbox-approved-${VISITOR}`));
  expect(calls('PUT')).toEqual([`/api/approvals/${ENC}`]);
  expect(byId(r, `inbox-approve-${VISITOR}`)).toBeUndefined();
});

test('a notification opens the device sheet, where it can be blocked', async () => {
  core();
  useInbox.getState().add([ARRIVAL]);
  const r = await render(<Notifications />);
  await settle(() => !!byId(r, `inbox-${ARRIVAL.id}`));
  await press(byId(r, `inbox-${ARRIVAL.id}`));
  expect(mockPush).toHaveBeenCalledWith({ pathname: '/device/[mac]', params: { mac: VISITOR } });
});

test('an arrival already authorised elsewhere offers no « Autoriser »', async () => {
  const approved = core();
  approved.add(VISITOR);
  useInbox.getState().add([ARRIVAL]);
  const r = await render(<Notifications />);
  await settle(() => !!byId(r, `inbox-approved-${VISITOR}`));
  expect(byId(r, `inbox-approve-${VISITOR}`)).toBeUndefined();
});

test('the demo makes its visitor join and leave; a real box has no such button', async () => {
  core();
  let r = await render(<Devices />);
  await settle(() => text(r).includes('Tablette-Visiteur'));
  expect(byId(r, 'demo-visitor-leave')).toBeUndefined();
  expect(byId(r, 'demo-visitor-join')).toBeUndefined();
  await act(async () => root?.unmount());

  useSession.setState({ session: { mode: 'demo' } });
  r = await render(<Devices />);
  // The visitor is on the box: the demo offers to make it leave.
  await settle(() => !!byId(r, 'demo-visitor-leave'));
  await press(byId(r, 'demo-visitor-leave'));
  await settle(() => request.mock.calls.some(([m, p]) => m === 'POST' && p === '/api/demo/visitor'));
  const call = request.mock.calls.find(([m, p]) => m === 'POST' && p === '/api/demo/visitor');
  expect(JSON.parse(call![2])).toEqual({ present: false });
});
