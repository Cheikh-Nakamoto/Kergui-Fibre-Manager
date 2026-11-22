/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Auto-reject in Settings (#35, Max). Turning it on shows the devices on the
// box with one "Tout accepter et activer"; the setting is the core's (PUT
// /api/autoreject), which the watch service reads too. Without Max the
// switch is locked with the one neutral sentence; turning it off is free,
// even once the pass lapsed.
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
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKey: null, publicKeyBase64: '' } }));

import KerguiCore from '../../modules/kergui-core';
import { useEntitlement } from '../licensing/entitlement';
import { useSession } from '../session/store';
import { AutoRejectSetting } from '../ui/AutoRejectSetting';

const NEUTRAL = 'Cette fonctionnalité n’est pas disponible pour votre compte.';
const request = KerguiCore.request as jest.Mock;

const DEVICES = [
  { MAC: 'ac:bb:cc:00:00:11', Hostname: 'Phone-Salon', CustomName: '', IP: '192.168.1.10', Connected: true, Blocked: false },
  { MAC: 'ac:bb:cc:00:00:22', Hostname: 'Laptop-Bureau', CustomName: '', IP: '192.168.1.11', Connected: true, Blocked: false },
  { MAC: 'ac:bb:cc:00:00:33', Hostname: 'TV-Salon', CustomName: '', IP: '192.168.1.12', Connected: true, Blocked: false },
];

/** The core as the setting sees it: the device list and the rule's state. */
function box(enabled = false) {
  let status = { enabled, since: '', delay_minutes: 15, devices: [] as { mac: string; state: string }[] };
  if (enabled) status.devices = DEVICES.map((d) => ({ mac: d.MAC, state: 'accepted' }));
  request.mockImplementation(async (method: string, path: string, body: string) => {
    if (method === 'GET' && path === '/api/devices') return { status: 200, body: JSON.stringify(DEVICES) };
    if (method === 'POST' && path === '/api/usage/sample') return { status: 200, body: '{}' };
    if (method === 'GET' && path === '/api/autoreject') return { status: 200, body: JSON.stringify(status) };
    if (method === 'PUT' && path === '/api/autoreject') {
      const on = (JSON.parse(body) as { enabled: boolean }).enabled;
      status = {
        ...status,
        enabled: on,
        devices: on ? DEVICES.map((d) => ({ mac: d.MAC, state: 'accepted', first_seen: '', deadline: '' })) : [],
      };
      return { status: 200, body: JSON.stringify(status) };
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

const all = (r: Root, id: string) => r.container.queryAll((n) => n.props.testID === id);
const byId = (r: Root, id: string) => all(r, id)[0];
const withProp = (r: Root, id: string, prop: string) => all(r, id).find((n) => typeof n.props[prop] === 'function');
const text = (r: Root) => texts(r.container).join('\n');
const press = async (r: Root, id: string) => {
  const node = byId(r, id);
  if (!node) throw new Error(`no ${id}`);
  await act(async () => void node.props.onClick({ nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined }));
};
const flip = async (r: Root, id: string, value: boolean) => {
  const node = withProp(r, id, 'onChange');
  if (!node) throw new Error(`no switch ${id}`);
  await act(async () => void node.props.onChange({ nativeEvent: { value } }));
};
const puts = () => request.mock.calls.filter(([m]) => m === 'PUT').map(([, p, b]) => [p, JSON.parse(b as string)]);
const switchDisabled = (r: Root) =>
  all(r, 'settings-auto-reject').some((n) => n.props.disabled === true || n.props.accessibilityState?.disabled === true);

async function render(onEnabled?: () => void) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false, gcTime: 0 } } });
  qc = client;
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={client}>
        <PaperProvider settings={{ icon: iconAsText }}>
          <AutoRejectSetting onEnabled={onEnabled} />
        </PaperProvider>
      </QueryClientProvider>,
    );
  });
  await settle(() => request.mock.calls.some(([m, p]) => m === 'GET' && p === '/api/autoreject') && !switchDisabled(root!));
  await settle(() => request.mock.calls.some(([m, p]) => m === 'GET' && p === '/api/devices'));
  return root;
}

function tier(t: 'free' | 'basic' | 'max', expired = false) {
  const now = Math.floor(Date.now() / 1000);
  const expiresAt = expired ? now - 10 * 86400 : now + 86400;
  useEntitlement.setState(
    t === 'free'
      ? { boxId: 'ZTEG00000001', token: null, claims: null }
      : {
          boxId: 'ZTEG00000001',
          token: 'x.y',
          claims: { account: 'acc_1', boxId: 'ZTEG00000001', tier: t, expiresAt, issuedAt: expiresAt - 30 * 86400, keyId: 'k' },
        },
  );
}

let alert: jest.SpyInstance;

beforeEach(() => {
  request.mockReset();
  alert = jest.spyOn(Alert, 'alert').mockImplementation(() => undefined);
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
  tier('max');
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = qc = null;
  alert.mockRestore();
});

test('with Max, turning it on shows the present devices, and "Tout accepter" turns it on', async () => {
  box();
  const onEnabled = jest.fn();
  const r = await render(onEnabled);
  expect(byId(r, 'settings-auto-reject-offer')).toBeUndefined();

  await flip(r, 'settings-auto-reject', true);
  // Nothing sent yet: the household sees what will be accepted first.
  expect(puts()).toEqual([]);
  expect(byId(r, 'settings-auto-reject-offer')).toBeDefined();
  expect(text(r)).toContain('Appareils présents sur la box (3)');
  for (const name of ['Phone-Salon', 'Laptop-Bureau', 'TV-Salon']) expect(text(r)).toContain(name);

  await press(r, 'settings-auto-reject-accept-all');
  await settle(() => !!byId(r, 'settings-auto-reject-state'));
  expect(puts()).toEqual([['/api/autoreject', { enabled: true }]]);
  expect(text(r)).toContain('Actif · 3 appareils acceptés');
  expect(byId(r, 'settings-auto-reject-offer')).toBeUndefined();
  expect(onEnabled).toHaveBeenCalled();
});

test('cancelling the list sends nothing', async () => {
  box();
  const r = await render();
  await flip(r, 'settings-auto-reject', true);
  await press(r, 'settings-auto-reject-cancel');
  expect(byId(r, 'settings-auto-reject-offer')).toBeUndefined();
  expect(puts()).toEqual([]);
});

test('turning it off sends enabled=false', async () => {
  box(true);
  const r = await render();
  await settle(() => !!byId(r, 'settings-auto-reject-state'));
  await flip(r, 'settings-auto-reject', false);
  await settle(() => puts().length > 0);
  expect(puts()).toEqual([['/api/autoreject', { enabled: false }]]);
  await settle(() => !byId(r, 'settings-auto-reject-state'));
  expect(byId(r, 'settings-auto-reject-state')).toBeUndefined();
});

test.each(['free', 'basic'] as const)('with %s, the switch is locked with the neutral sentence', async (t) => {
  tier(t);
  box();
  const r = await render();
  expect(byId(r, 'settings-auto-reject-locked')).toBeDefined();
  expect(text(r)).toContain(NEUTRAL);
  expect(switchDisabled(r)).toBe(true);
  await flip(r, 'settings-auto-reject', true);
  expect(byId(r, 'settings-auto-reject-offer')).toBeUndefined();
  expect(puts()).toEqual([]);
  // No payment surface: no button besides the switch.
  expect(text(r)).not.toMatch(/abonne|payer|acheter|prix/i);
});

test('once Max lapsed, it says it stopped, and can still be turned off for free', async () => {
  tier('max', true);
  box(true);
  const r = await render();
  await settle(() => !!byId(r, 'settings-auto-reject-state'));
  expect(text(r)).toContain('En pause');
  expect(text(r)).toContain(NEUTRAL);
  expect(switchDisabled(r)).toBe(false);
  await flip(r, 'settings-auto-reject', false);
  await settle(() => puts().length > 0);
  expect(puts()).toEqual([['/api/autoreject', { enabled: false }]]);
});

test('a box without Wi-Fi access control hides the setting', async () => {
  request.mockImplementation(async (method: string, path: string) => {
    if (path === '/api/autoreject') return { status: 501, body: '{"error":"not implemented"}' };
    if (path === '/api/devices') return { status: 200, body: JSON.stringify(DEVICES) };
    return { status: 200, body: '{}' };
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  qc = client;
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={client}>
        <PaperProvider settings={{ icon: iconAsText }}>
          <AutoRejectSetting />
        </PaperProvider>
      </QueryClientProvider>,
    );
  });
  await settle(() => request.mock.calls.some(([, p]) => p === '/api/autoreject'));
  await settle();
  expect(byId(root, 'settings-auto-reject')).toBeUndefined();
});
