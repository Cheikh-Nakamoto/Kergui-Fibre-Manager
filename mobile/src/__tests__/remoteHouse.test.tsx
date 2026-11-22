/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Remote consumption, SSIDs and cut / restore (#50): the Conso and Wi-Fi
// tabs are the local screens, fed by what the relay read, with its as_of;
// an endpoint this box model has not had validated stays disabled with a
// sentence; "Internet de la maison" cuts through the relay, which spares the
// relays and the protected devices. The box itself is never asked.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Alert, Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() },
  Stack: { Screen: () => null },
  useLocalSearchParams: () => ({ mac: 'ac:bb:cc:00:00:22' }),
}));
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('../alerts/alerts', () => ({ checkNewDevices: async () => 0, enforceAutoReject: async () => 0 }));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));
jest.mock('../licensing/household', () => ({
  withHousehold: (run: (tok: string, id: string) => Promise<unknown>) => run('tok', 'T1'),
}));
const mockHub = {
  trustBundle: jest.fn(),
  routerDevices: jest.fn(),
  relayStatus: jest.fn(),
  deviceCommand: jest.fn(),
  routerCommand: jest.fn(),
  command: jest.fn(),
};
jest.mock('../licensing/hub', () => ({
  get hub() {
    return mockHub;
  },
}));

import KerguiCore from '../../modules/kergui-core';
import Devices from '../app/(tabs)/devices';
import UsageTab from '../app/(tabs)/usage';
import WiFi from '../app/(tabs)/wifi';
import DeviceScreen from '../app/device/[mac]';
import { useAccount } from '../licensing/account';
import { useEntitlement } from '../licensing/entitlement';
import { useSession } from '../session/store';

const core = KerguiCore as unknown as Record<string, jest.Mock>;
const PC = 'ac:bb:cc:00:00:22';
const BUNDLE = { payload: 'UA==', sig: 'Uw==', version: 3 };
const minutesAgo = (m: number) => new Date(Date.now() - m * 60_000).toISOString();

// What the relay answers, per "METHOD path": [status, body].
let answers: Record<string, [number, unknown]> = {};
let readAt = minutesAgo(3);

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
  for (let i = 0; i < 80 && !until(); i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 10))));
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

const all = (r: Root, id: string) => r.container.queryAll((n) => n.props.testID === id);
const byId = (r: Root, id: string) => all(r, id)[0];
const flip = async (r: Root, id: string, value: boolean) => {
  const node = all(r, id).find((n) => typeof n.props.onChange === 'function');
  if (!node) throw new Error(`no switch ${id}`);
  await act(async () => void node.props.onChange({ nativeEvent: { value } }));
};
const isDisabled = (r: Root, id: string) => all(r, id).some((n) => n.props.disabled === true || n.props.accessibilityState?.disabled === true);
const text = (r: Root) => texts(r.container).join('\n');
const press = async (node: TestInstance) =>
  act(async () => void node.props.onClick({ nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined }));
const signed = () => core.signRemoteRequest.mock.calls.map(([, , m, p, b]) => `${m} ${p}${b ? ' ' + b : ''}`);
const boxCalls = () => core.request.mock.calls.map(([m, p]) => `${m} ${p}`).filter((c) => c !== 'GET /api/autoreject');

let alert: jest.SpyInstance;

beforeEach(() => {
  readAt = minutesAgo(3);
  answers = {
    'GET /api/usage': [200, [{ mac: PC, period: '2026-11', bytes: 3_000_000_000, sent: 1_000_000_000, received: 2_000_000_000 }]],
    'GET /api/wifi/ssids': [200, [{ id: 'DEV.WIFI.AP5', alias: 'SSID5', band: '5GHz', name: 'MonWiFi-5G', enabled: true, hidden: false, security: 'WPA2' }]],
    'GET /api/wifi/blocked': [200, []],
  };
  for (const m of Object.values(mockHub)) m.mockReset();
  for (const k of ['request', 'remoteRelays', 'openDeviceSnapshot', 'signRemoteRequest', 'verifyCommandResult']) core[k].mockReset();
  core.request.mockResolvedValue({ status: 500, body: '{"error":"the box must not be asked"}' });
  alert = jest.spyOn(Alert, 'alert');
  alert.mockImplementation((_t, _m, buttons?: { onPress?: () => void }[]) => buttons?.[1]?.onPress?.());
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
  useAccount.setState({ account: { accountId: 'acc', phone: '+221770000001', sessionToken: 'kgs_x', expiresAt: '' } });
  const now = Math.floor(Date.now() / 1000);
  useEntitlement.setState({
    boxId: 'ZTEG00000001',
    token: 'x.y',
    claims: { account: 'acc', boxId: 'ZTEG00000001', tier: 'max', expiresAt: now + 86400, issuedAt: now, keyId: 'k' },
  });
  mockHub.trustBundle.mockResolvedValue(BUNDLE);
  mockHub.relayStatus.mockResolvedValue({ state: 'active', agentId: 'A1', lastSeenAt: new Date().toISOString(), ageSeconds: 5 });
  mockHub.routerDevices.mockResolvedValue({ snapshot: { payload: 'cA==', sig: 'cw==' }, agentId: 'A1', asOf: minutesAgo(1) });
  core.remoteRelays.mockResolvedValue(JSON.stringify({ owner: true, relays: [{ agentId: 'A1', kind: 'phone' }] }));
  core.openDeviceSnapshot.mockResolvedValue(
    JSON.stringify({
      agentId: 'A1',
      asOf: minutesAgo(1),
      devices: [{ MAC: PC, IP: '192.168.1.10', Hostname: 'Laptop-Bureau', CustomName: '', Vendor: '', SSID: 'DEV.WIFI.AP5', Connected: true, Blocked: false, NewlySeen: false }],
    }),
  );
  // The core signs: the command id names the request, so the relay's
  // answer can be looked up by it.
  core.signRemoteRequest.mockImplementation(async (_t: string, _b: string, method: string, path: string, body: string) =>
    JSON.stringify({ payload: 'UA==', sig: 'Uw==', body: body ? 'Yg==' : '', cmdId: `${method} ${path}`, method, path, expiresAt: new Date(Date.now() + 60_000).toISOString() }),
  );
  mockHub.routerCommand.mockImplementation(async () => ({ commandId: 'x', status: 'done', result: { payload: 'cg==', sig: 'cw==' } }));
  core.verifyCommandResult.mockImplementation(async (_t: string, cmdId: string) => {
    const [status, body] = answers[cmdId] ?? [200, {}];
    return JSON.stringify({ agentId: 'A1', status, body, executedAt: readAt });
  });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = qc = null;
  alert.mockRestore();
});

test('Conso: the local screen, with what the relay measured and when', async () => {
  const r = await render(<UsageTab />);
  await settle(() => !!byId(r, `usage-row-${PC}`));
  expect(byId(r, 'usage-remote')).toBeDefined();
  expect(text(r)).toContain('Via le relais · mesuré il y a 3 min');
  expect(byId(r, `usage-row-${PC}`)).toBeDefined();
  expect(text(r)).toContain('Laptop-Bureau');
  // The past months are read at home: the arrow is off and says so.
  expect(isDisabled(r, 'usage-month-prev')).toBe(true);
  expect(byId(r, 'usage-remote-history')).toBeDefined();
  expect(signed()).toContain('GET /api/usage');
  expect(core.signRemoteRequest).toHaveBeenCalledWith('T1', JSON.stringify(BUNDLE), 'GET', '/api/usage', '', 'ZTEG00000001');
  expect(boxCalls()).toEqual([]);
});

test('Conso: an old measure is flagged', async () => {
  readAt = minutesAgo(20);
  const r = await render(<UsageTab />);
  await settle(() => text(r).includes('dépassée'));
  expect(text(r)).toContain('Liste peut-être dépassée : le relais l’a lue il y a 20 min.');
});

test('Wi-Fi: the networks the relay read; off goes through the relay; filter and password stay at home', async () => {
  const r = await render(<WiFi />);
  await settle(() => !!byId(r, 'wifi-ssid-DEV.WIFI.AP5'));
  expect(text(r)).toContain('Via le relais · lu il y a 3 min');
  expect(byId(r, 'wifi-filter-local')).toBeDefined();
  expect(byId(r, 'wifi-ssid-password-local-DEV.WIFI.AP5')).toBeDefined();
  expect(byId(r, 'wifi-ssid-password-open-DEV.WIFI.AP5')).toBeUndefined();

  await flip(r, 'wifi-ssid-switch-DEV.WIFI.AP5', false);
  await settle(() => signed().some((s) => s.startsWith('PATCH')));
  expect(signed()).toContain('PATCH /api/wifi/ssids/DEV.WIFI.AP5 {"enabled":false}');
  expect(mockHub.routerCommand).toHaveBeenCalledWith('tok', 'T1', { payload: 'UA==', sig: 'Uw==', body: 'Yg==' });
  expect(boxCalls()).toEqual([]);
});

test('Wi-Fi: the relay refusing an unverified write leaves the controls off, and says why', async () => {
  answers['PATCH /api/wifi/ssids/DEV.WIFI.AP5'] = [501, { error: 'not validated', code: 'unverified' }];
  const r = await render(<WiFi />);
  await settle(() => !!byId(r, 'wifi-ssid-DEV.WIFI.AP5'));
  await flip(r, 'wifi-ssid-switch-DEV.WIFI.AP5', false);
  await settle(() => text(r).includes('Pas encore validé'));
  expect(text(r)).toContain('Pas encore validé sur ce modèle de box');
  expect(isDisabled(r, 'wifi-ssid-switch-DEV.WIFI.AP5')).toBe(true);
});

test('device sheet: Wi-Fi access not validated on this model is disabled, with a sentence', async () => {
  answers['GET /api/wifi/blocked'] = [501, { error: 'not validated', code: 'unverified' }];
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-wifi-cut-unverified'));
  expect(text(r)).toContain('Couper l’accès Wi-Fi n’est pas encore validé sur ce modèle de box : désactivé à distance.');
  expect(isDisabled(r, 'device-toggle-wifi-cut')).toBe(true);
  expect(boxCalls()).toEqual([]);
});

test('device sheet: Wi-Fi access cut through the relay', async () => {
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-toggle-wifi-cut'));
  await press(byId(r, 'device-toggle-wifi-cut'));
  await settle(() => signed().some((s) => s.includes('wifi-block')));
  expect(signed()).toContain(`POST /api/devices/${PC}/wifi-block`);
  expect(boxCalls()).toEqual([]);
});

test('Internet de la maison: cut and restore through the relay, the relays spared', async () => {
  answers['POST /api/internet/cut'] = [200, { cut: [PC], spared: ['02:00:00:00:00:99', '02:00:00:00:00:97'] }];
  answers['POST /api/internet/restore'] = [200, { restored: [PC] }];
  const r = await render(<Devices />);
  await settle(() => !!byId(r, 'house-internet'));
  await press(byId(r, 'house-cut'));
  await settle(() => !!byId(r, 'house-cut-done'));
  expect(text(r)).toContain('Internet coupé pour 1 appareil ✅ — 2 épargnés (relais, appareils protégés).');
  await press(byId(r, 'house-restore'));
  await settle(() => !!byId(r, 'house-restore-done'));
  expect(text(r)).toContain('Internet rétabli pour 1 appareil ✅');
  expect(signed()).toEqual(expect.arrayContaining(['POST /api/internet/cut', 'POST /api/internet/restore']));
  expect(boxCalls()).toEqual([]);
});

test('Internet de la maison: cutting is Basic, a lapsed pass gets the neutral lock', async () => {
  useEntitlement.setState({ boxId: 'ZTEG00000001', token: null, claims: null });
  const r = await render(<Devices />);
  await settle(() => !!byId(r, 'house-internet'));
  await press(byId(r, 'house-cut'));
  expect(alert).toHaveBeenCalledWith('', 'Cette fonctionnalité n’est pas disponible pour votre compte.', expect.anything());
  expect(signed()).not.toContain('POST /api/internet/cut');
});

test('without a relay, the screens stay local and no house card shows', async () => {
  core.remoteRelays.mockResolvedValue(JSON.stringify({ owner: true, relays: [] }));
  core.request.mockImplementation(async (method: string, path: string) =>
    method === 'GET' && path === '/api/devices' ? { status: 200, body: '[]' } : { status: 200, body: '[]' },
  );
  const r = await render(<Devices />);
  await settle(() => boxCalls().includes('GET /api/devices'));
  expect(byId(r, 'house-internet')).toBeUndefined();
  expect(core.signRemoteRequest).not.toHaveBeenCalled();
});
