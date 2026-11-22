/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The Wi-Fi tab's network management (#34): turning a network off, renaming
// it and changing its password are Max; turning it back on is free, so an
// expired pass never leaves the Wi-Fi off. A locked action shows the one
// neutral sentence and sends nothing to the box.
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
import { useEntitlement } from '../licensing/entitlement';
import { useSession } from '../session/store';

const NEUTRAL = 'Cette fonctionnalité n’est pas disponible pour votre compte.';
const AP1 = 'DEV.WIFI.AP1';
const AP5 = 'DEV.WIFI.AP5';
const request = KerguiCore.request as jest.Mock;

type SSID = { id: string; alias: string; band: string; name: string; enabled: boolean; hidden: boolean; security: string };

/** The core as the Wi-Fi tab sees it: two networks, the filter, the PATCHes. */
function box(over: Partial<Record<string, Partial<SSID>>> = {}) {
  const ssids: SSID[] = [
    { id: AP1, alias: 'SSID1', band: '2.4GHz', name: 'MonWiFi-2G', enabled: true, hidden: false, security: 'WPA2-PSK-AES' },
    { id: AP5, alias: 'SSID5', band: '5GHz', name: 'MonWiFi-5G', enabled: true, hidden: false, security: 'WPA2-PSK-AES' },
  ].map((s) => ({ ...s, ...over[s.id] }));
  request.mockImplementation(async (method: string, path: string, body: string) => {
    if (method === 'GET' && path === '/api/wifi/ssids') return { status: 200, body: JSON.stringify(ssids) };
    if (method === 'GET' && path === '/api/filter') return { status: 200, body: '{"enabled":false}' };
    const m = /^\/api\/wifi\/ssids\/([^/]+)$/.exec(path);
    if (method === 'PATCH' && m) {
      const s = ssids.find((x) => x.id === decodeURIComponent(m[1]))!;
      const change = JSON.parse(body) as { enabled?: boolean; name?: string; password?: string };
      if (change.enabled !== undefined) s.enabled = change.enabled;
      if (change.name !== undefined) s.name = change.name;
      return { status: 200, body: JSON.stringify(s) };
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

async function render() {
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
  await settle(() => !!byId(root!, `wifi-ssid-${AP5}`));
  return root;
}

const all = (r: Root, id: string) => r.container.queryAll((n) => n.props.testID === id);
const byId = (r: Root, id: string) => all(r, id)[0];
/** The node under testID that carries the prop (the wrapper may not). */
const withProp = (r: Root, id: string, prop: string) => all(r, id).find((n) => typeof n.props[prop] === 'function');
const text = (r: Root) => texts(r.container).join('\n');
const press = async (r: Root, id: string) => {
  const node = byId(r, id);
  if (!node) throw new Error(`no ${id}`);
  await act(async () => void node.props.onClick({ nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined }));
};
// The native switch reports a flip through onChange({ nativeEvent: { value } }).
const flip = async (r: Root, id: string, value: boolean) => {
  const node = withProp(r, id, 'onChange');
  if (!node) throw new Error(`no switch ${id}`);
  await act(async () => void node.props.onChange({ nativeEvent: { value } }));
};
const type = async (r: Root, id: string, value: string) => {
  const node = withProp(r, id, 'onChangeText');
  if (!node) throw new Error(`no input ${id}`);
  await act(async () => void node.props.onChangeText(value));
};

const patches = () =>
  request.mock.calls.filter(([m]) => m === 'PATCH').map(([, p, b]) => [decodeURIComponent(p as string), JSON.parse(b as string)]);

function tier(t: 'free' | 'basic' | 'max', expired = false) {
  const now = Math.floor(Date.now() / 1000);
  // Past expiry and the 3-day offline grace.
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
const sayYes = () => alert.mockImplementation((_t, _m, buttons?: { onPress?: () => void }[]) => buttons?.[1]?.onPress?.());
const neutralShown = () => alert.mock.calls.some(([, m]) => m === NEUTRAL);

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

test('with Max, turning a network off asks first, then sends enabled=false', async () => {
  box();
  const r = await render();
  sayYes();
  await flip(r, `wifi-ssid-switch-${AP5}`, false);
  const [, message, buttons] = alert.mock.calls[0];
  expect(message).toContain('MonWiFi-5G');
  expect(message).toMatch(/seront coupés/);
  expect(buttons[1].style).toBe('destructive');
  await settle(() => patches().length > 0);
  expect(patches()).toEqual([[`/api/wifi/ssids/${AP5}`, { enabled: false }]]);
  await settle(() => text(r).includes('désactivé'));
  expect(text(r)).toContain('désactivé');
});

test.each(['free', 'basic'] as const)('with %s, turning a network off shows the neutral sentence and sends nothing', async (t) => {
  tier(t);
  box();
  const r = await render();
  await flip(r, `wifi-ssid-switch-${AP5}`, false);
  expect(alert).toHaveBeenCalledTimes(1);
  expect(alert.mock.calls[0][1]).toBe(NEUTRAL);
  expect(patches()).toEqual([]);
});

test.each([
  ['no pass', 'free', false],
  ['an expired Max pass', 'max', true],
] as const)('turning a network back on is free, with %s', async (_label, t, expired) => {
  tier(t, expired);
  box({ [AP5]: { enabled: false } });
  const r = await render();
  sayYes();
  await flip(r, `wifi-ssid-switch-${AP5}`, true);
  expect(neutralShown()).toBe(false);
  // Turning on does not warn that devices will be cut.
  expect(alert.mock.calls[0][1]).not.toMatch(/coupés/);
  await settle(() => patches().length > 0);
  expect(patches()).toEqual([[`/api/wifi/ssids/${AP5}`, { enabled: true }]]);
});

test('with Max, renaming a network confirms the new name, then sends it', async () => {
  box();
  const r = await render();
  // Nothing to rename until the name changes.
  expect(all(r, `wifi-ssid-rename-${AP1}`).some((n) => n.props.accessibilityState?.disabled === true)).toBe(true);
  await type(r, `wifi-ssid-name-${AP1}`, '  Maison  ');
  sayYes();
  await press(r, `wifi-ssid-rename-${AP1}`);
  expect(alert.mock.calls[0][1]).toContain('« MonWiFi-2G » en « Maison »');
  expect(alert.mock.calls[0][2][1].text).toBe('Oui, renommer');
  await settle(() => patches().length > 0);
  expect(patches()).toEqual([[`/api/wifi/ssids/${AP1}`, { name: 'Maison' }]]);
  await settle(() => text(r).includes('Maison'));
  expect(text(r)).not.toContain('MonWiFi-2G');
});

test.each(['free', 'basic'] as const)('with %s, renaming shows the neutral sentence and sends nothing', async (t) => {
  tier(t);
  box();
  const r = await render();
  await type(r, `wifi-ssid-name-${AP1}`, 'Maison');
  await press(r, `wifi-ssid-rename-${AP1}`);
  expect(alert).toHaveBeenCalledTimes(1);
  expect(alert.mock.calls[0][1]).toBe(NEUTRAL);
  expect(patches()).toEqual([]);
});

test('with Max, the password editor opens, confirms, sends the new password, then closes', async () => {
  box();
  const r = await render();
  expect(byId(r, `wifi-ssid-password-${AP1}`)).toBeUndefined();
  await press(r, `wifi-ssid-password-open-${AP1}`);
  expect(byId(r, `wifi-ssid-password-${AP1}`)).toBeDefined();
  await type(r, `wifi-ssid-password-${AP1}`, 'nouveau-secret');
  sayYes();
  await press(r, `wifi-ssid-password-save-${AP1}`);
  expect(alert.mock.calls[0][1]).toMatch(/mot de passe du réseau « MonWiFi-2G »/);
  expect(alert.mock.calls[0][2][1].text).toBe('Oui, changer');
  await settle(() => !!byId(r, `wifi-ssid-password-done-${AP1}`));
  expect(patches()).toEqual([[`/api/wifi/ssids/${AP1}`, { password: 'nouveau-secret' }]]);
  expect(byId(r, `wifi-ssid-password-${AP1}`)).toBeUndefined();
  expect(text(r)).toContain('Mot de passe changé.');
  // The password is never shown back.
  expect(text(r)).not.toContain('nouveau-secret');
});

test('cancelling the password editor sends nothing', async () => {
  box();
  const r = await render();
  await press(r, `wifi-ssid-password-open-${AP1}`);
  await type(r, `wifi-ssid-password-${AP1}`, 'nouveau-secret');
  await press(r, `wifi-ssid-password-cancel-${AP1}`);
  expect(byId(r, `wifi-ssid-password-${AP1}`)).toBeUndefined();
  expect(patches()).toEqual([]);
});

test.each(['free', 'basic'] as const)('with %s, the password editor stays shut behind the neutral sentence', async (t) => {
  tier(t);
  box();
  const r = await render();
  await press(r, `wifi-ssid-password-open-${AP1}`);
  expect(alert).toHaveBeenCalledTimes(1);
  expect(alert.mock.calls[0][1]).toBe(NEUTRAL);
  expect(byId(r, `wifi-ssid-password-${AP1}`)).toBeUndefined();
  expect(patches()).toEqual([]);
});

test('in the demo every network action is open without a pass', async () => {
  tier('free');
  useSession.setState({ session: { mode: 'demo' } });
  box();
  const r = await render();
  sayYes();
  await flip(r, `wifi-ssid-switch-${AP5}`, false);
  await press(r, `wifi-ssid-password-open-${AP1}`);
  expect(neutralShown()).toBe(false);
  expect(byId(r, `wifi-ssid-password-${AP1}`)).toBeDefined();
});

test('a refusal from the core shows the lock, not an error', async () => {
  box();
  const base = request.getMockImplementation()!;
  request.mockImplementation(async (method: string, path: string, body: string) =>
    method === 'PATCH'
      ? { status: 403, body: '{"error":"not entitled","code":"not_entitled","feature":"ssid_manage","required":"max"}' }
      : base(method, path, body),
  );
  const r = await render();
  sayYes();
  await flip(r, `wifi-ssid-switch-${AP5}`, false);
  await settle(() => !!byId(r, 'locked'));
  expect(byId(r, 'locked')).toBeDefined();
  expect(text(r)).toContain(NEUTRAL);
});
