/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Remote mode on screen (#46): the Devices tab shows the list the relay
// signed with its age, warns when it is old, and says when the relay is
// offline; the device sheet shows ✅ only once the core verified the relay's
// answer, and an offline relay is a sentence, never a login to the box.
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
  command: jest.fn(),
};
jest.mock('../licensing/hub', () => ({
  get hub() {
    return mockHub;
  },
}));

import KerguiCore from '../../modules/kergui-core';
import Devices from '../app/(tabs)/devices';
import DeviceScreen from '../app/device/[mac]';
import { useDemoRemote } from '../features/demoRemote';
import { useAccount } from '../licensing/account';
import { useEntitlement } from '../licensing/entitlement';
import { useSession } from '../session/store';

const core = KerguiCore as unknown as Record<string, jest.Mock>;
const PC = 'ac:bb:cc:00:00:22';
const BUNDLE = { payload: 'UA==', sig: 'Uw==', version: 3 };

let blocked = false;
const listAsOf = (minutesAgo: number) => new Date(Date.now() - minutesAgo * 60_000).toISOString();
let asOf = listAsOf(2);

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

const byId = (r: Root, id: string) => r.container.queryAll((n) => n.props.testID === id)[0];
const text = (r: Root) => texts(r.container).join('\n');
const press = async (node: TestInstance) =>
  act(async () => void node.props.onClick({ nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined }));

let alert: jest.SpyInstance;

beforeEach(() => {
  blocked = false;
  asOf = listAsOf(2);
  for (const m of Object.values(mockHub)) m.mockReset();
  for (const k of ['request', 'remoteRelays', 'openDeviceSnapshot', 'signDeviceCommand', 'verifyCommandResult']) core[k].mockReset();
  // The box itself answers nothing: in remote mode it must not be asked.
  core.request.mockResolvedValue({ status: 500, body: '{"error":"the box must not be asked"}' });
  alert = jest.spyOn(Alert, 'alert');
  // "Oui, bloquer": the confirmation's second button.
  alert.mockImplementation((_t, _m, buttons?: { onPress?: () => void }[]) => buttons?.[1]?.onPress?.());
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
  useAccount.setState({ account: { accountId: 'acc', phone: '+221770000001', sessionToken: 'kgs_x', expiresAt: '' } });
  const now = Math.floor(Date.now() / 1000);
  useEntitlement.setState({
    boxId: 'ZTEG00000001',
    token: 'x.y',
    claims: { account: 'acc', boxId: 'ZTEG00000001', tier: 'basic', expiresAt: now + 86400, issuedAt: now, keyId: 'k' },
  });

  mockHub.trustBundle.mockResolvedValue(BUNDLE);
  mockHub.routerDevices.mockImplementation(async () => ({ snapshot: { payload: 'cA==', sig: 'cw==' }, agentId: 'A1', asOf }));
  mockHub.relayStatus.mockResolvedValue({ state: 'active', agentId: 'A1', lastSeenAt: new Date().toISOString(), ageSeconds: 5 });
  core.remoteRelays.mockResolvedValue(JSON.stringify({ owner: true, relays: [{ agentId: 'A1', kind: 'pc' }] }));
  core.openDeviceSnapshot.mockImplementation(async () =>
    JSON.stringify({
      agentId: 'A1',
      asOf,
      devices: [{ MAC: PC, IP: '192.168.1.10', Hostname: 'Laptop-Bureau', CustomName: '', Vendor: '', SSID: 'MonWiFi-2G', Connected: true, Blocked: blocked, NewlySeen: false }],
    }),
  );
  core.signDeviceCommand.mockResolvedValue(JSON.stringify({ payload: 'Q01E', sig: 'U0lH', cmdId: 'c1', mac: PC, expiresAt: new Date(Date.now() + 60_000).toISOString() }));
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = qc = null;
  alert.mockRestore();
});

// What reached the core's box API. GET /api/autoreject reads the core's own
// database (the device sheet's pending badge), never the box; so do the
// household's names (/api/labels, #95).
const boxCalls = () => core.request.mock.calls.map(([m, p]) => `${m} ${p}`).filter((c) => c !== 'GET /api/autoreject' && !c.endsWith(' /api/labels'));

test('the remote list says how old it is, and the relay is active', async () => {
  const r = await render(<Devices />);
  await settle(() => text(r).includes('Laptop-Bureau'));
  expect(byId(r, 'remote-banner')).toBeDefined();
  expect(byId(r, 'remote-as-of')).toBeDefined();
  expect(text(r)).toContain('Via le relais · mis à jour il y a 2 min');
  expect(byId(r, 'remote-relay-active')).toBeDefined();
  expect(byId(r, 'remote-stale')).toBeUndefined();
  // Signature checked by the core against the owner's bundle.
  expect(core.openDeviceSnapshot).toHaveBeenCalledWith('T1', JSON.stringify(BUNDLE), JSON.stringify({ payload: 'cA==', sig: 'cw==' }));
  expect(boxCalls()).toEqual([]);
});

test('an old list is flagged, and an offline relay is said with since when', async () => {
  asOf = listAsOf(25);
  mockHub.relayStatus.mockResolvedValue({ state: 'offline', agentId: 'A1', lastSeenAt: listAsOf(25), ageSeconds: 25 * 60 });
  const r = await render(<Devices />);
  await settle(() => !!byId(r, 'remote-stale'));
  expect(text(r)).toContain('Liste peut-être dépassée : le relais l’a lue il y a 25 min.');
  expect(byId(r, 'remote-relay-offline')).toBeDefined();
  expect(text(r)).toMatch(/Relais hors ligne depuis \d\d:\d\d \(il y a 25 min\)/);
  expect(boxCalls()).toEqual([]);
});

test('block: ✅ only once the core verified the relay’s answer', async () => {
  mockHub.deviceCommand.mockImplementation(async () => {
    blocked = true;
    return { commandId: 'c1', status: 'done', result: { payload: 'cg==', sig: 'cw==' } };
  });
  let verify: (v: string) => void = () => undefined;
  core.verifyCommandResult.mockImplementation(() => new Promise<string>((ok) => (verify = ok)));

  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-toggle-block'));
  await press(byId(r, 'device-toggle-block'));
  await settle(() => core.verifyCommandResult.mock.calls.length > 0);
  // The hub said done; the core has not checked the relay's signature yet.
  expect(byId(r, 'device-remote-done')).toBeUndefined();
  expect(core.signDeviceCommand).toHaveBeenCalledWith('T1', PC, 'block', 'ZTEG00000001');

  await act(async () => verify(JSON.stringify({ agentId: 'A1', status: 200, executedAt: new Date().toISOString() })));
  await settle(() => !!byId(r, 'device-remote-done'));
  expect(text(r)).toContain('Bloqué ✅ — confirmé par le relais.');
  await settle(() => text(r).includes('Débloquer'));
  expect(boxCalls()).toEqual([]);
});

test('an offline relay: a clear message, nothing sent, no session on the box', async () => {
  mockHub.relayStatus.mockResolvedValue({ state: 'offline', agentId: 'A1', lastSeenAt: listAsOf(10), ageSeconds: 600 });
  const r = await render(<DeviceScreen />);
  await settle(() => !!byId(r, 'device-toggle-block'));
  await press(byId(r, 'device-toggle-block'));
  await settle(() => text(r).includes('hors ligne depuis'));
  expect(text(r)).toMatch(/Le relais est hors ligne depuis \d\d:\d\d : rien n’a été envoyé\. Kergui ne se connecte pas à la box à sa place\./);
  expect(core.signDeviceCommand).not.toHaveBeenCalled();
  expect(mockHub.deviceCommand).not.toHaveBeenCalled();
  expect(byId(r, 'device-remote-done')).toBeUndefined();
  expect(boxCalls()).toEqual([]);
});

test('without a relay in the owner’s bundle, the app stays local and reads the box', async () => {
  core.remoteRelays.mockResolvedValue(JSON.stringify({ owner: true, relays: [] }));
  core.request.mockImplementation(async (method: string, path: string) =>
    method === 'GET' && path === '/api/devices'
      ? { status: 200, body: JSON.stringify([{ MAC: PC, IP: '', Hostname: 'Laptop-Bureau', CustomName: '', Vendor: '', SSID: '', Connected: true, Blocked: false, NewlySeen: false }]) }
      : { status: 200, body: '[]' },
  );
  const r = await render(<Devices />);
  await settle(() => text(r).includes('Laptop-Bureau'));
  expect(byId(r, 'remote-banner')).toBeUndefined();
  expect(boxCalls()).toContain('GET /api/devices');
  expect(mockHub.routerDevices).not.toHaveBeenCalled();
});

test('the demo shows the remote banner, marked as the demo, once its relay is paired', async () => {
  useSession.setState({ session: { mode: 'demo' } });
  core.request.mockImplementation(async (method: string, path: string) =>
    method === 'GET' && path === '/api/devices'
      ? { status: 200, body: JSON.stringify([{ MAC: PC, IP: '', Hostname: 'Laptop-Bureau', CustomName: '', Vendor: '', SSID: '', Connected: true, Blocked: false, NewlySeen: false }]) }
      : { status: 200, body: '[]' },
  );
  let r = await render(<Devices />);
  await settle(() => text(r).includes('Laptop-Bureau'));
  expect(byId(r, 'remote-banner')).toBeUndefined();
  await act(async () => root?.unmount());

  useDemoRemote.getState().paired('PC du salon (démo)');
  r = await render(<Devices />);
  await settle(() => !!byId(r, 'remote-demo'));
  expect(text(r)).toContain('Démo · Via le relais « PC du salon (démo) » · mis à jour il y a moins d’une minute');
  expect(text(r)).toContain('Simulé');
  // The demo has no hub: nothing was asked of one.
  expect(mockHub.trustBundle).not.toHaveBeenCalled();
});
