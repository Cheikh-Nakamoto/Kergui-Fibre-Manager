/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Never cut this phone off the box (#113, UX audit 2026-11-26 C1). The sheet
// of this phone or of an owner's phone offers neither « Bloquer » nor
// « Couper l’accès » and says why; the Wi-Fi tab warns on the network this
// phone is on, keeps its switch and the last network's off, and says before
// a new password that this phone will be disconnected. The core refuses all
// of it anyway (409 self_lockout), in words the household understands.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Alert, Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

let mockMac = 'fa:4b:47:00:00:86';
jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() },
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
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));

import KerguiCore from '../../modules/kergui-core';
import WiFi from '../app/(tabs)/wifi';
import DeviceScreen from '../app/device/[mac]';
import { ApiError } from '../core/client';
import { RemoteCommandError } from '../core/remote';
import type { Device } from '../core/types';
import { errorMessage } from '../i18n';
import { useEntitlement } from '../licensing/entitlement';
import { useSession } from '../session/store';

const request = KerguiCore.request as jest.Mock;
const SELF = 'fa:4b:47:00:00:86';
const OWNER = 'fa:4b:47:00:00:87';
const PC = 'ac:bb:cc:00:00:22';
const AP1 = 'DEV.WIFI.AP1';
const AP5 = 'DEV.WIFI.AP5';

const THIS_PHONE = 'C’est ce téléphone : Kergui ne le bloque jamais.';
const OWNER_PHONE = 'C’est un téléphone du propriétaire : Kergui ne le bloque jamais.';
const ON_THIS_NETWORK =
  'Ce téléphone est connecté à ce réseau. Si vous l’éteignez, Kergui ne pourra plus parler à la box : il faudra un câble pour le rallumer.';
// The warning of #123 names this phone, then #113's next step.
const PASSWORD_THIS_PHONE =
  'Tous les appareils de la maison vont se déconnecter, ce téléphone aussi. Il faudra taper le nouveau mot de passe sur chacun. Reconnectez ce téléphone avec le nouveau mot de passe, puis rouvrez Kergui.';

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

type SSID = { id: string; alias: string; band: string; name: string; enabled: boolean; hidden: boolean; security: string };

/** The core as the screens see it: this phone on MonWiFi-2G, two networks. */
function box(opts: { devices?: object[]; ssids?: Partial<Record<string, Partial<SSID>>>; denied?: string[] } = {}) {
  const devices = opts.devices ?? [
    device(SELF, 'Mon-Telephone', { Mark: 'Self', Approved: true }),
    device(OWNER, 'Telephone-Proprio', { Mark: 'Owner', Approved: true, SSID: 'MonWiFi-5G' }),
    device(PC, 'Laptop-Bureau', { SSID: 'MonWiFi-5G' }),
  ];
  const ssids: SSID[] = [
    { id: AP1, alias: 'SSID1', band: '2.4GHz', name: 'MonWiFi-2G', enabled: true, hidden: false, security: 'WPA2-PSK-AES' },
    { id: AP5, alias: 'SSID5', band: '5GHz', name: 'MonWiFi-5G', enabled: true, hidden: false, security: 'WPA2-PSK-AES' },
  ].map((s) => ({ ...s, ...opts.ssids?.[s.id] }));
  request.mockImplementation(async (method: string, path: string, body: string) => {
    if (method === 'GET' && path === '/api/devices') return { status: 200, body: JSON.stringify(devices) };
    if (method === 'GET' && path === '/api/wifi/blocked') return { status: 200, body: JSON.stringify(opts.denied ?? []) };
    if (method === 'GET' && path === '/api/wifi/ssids') return { status: 200, body: JSON.stringify(ssids) };
    if (method === 'POST' && path === '/api/usage/sample') return { status: 200, body: '{}' };
    const m = /^\/api\/wifi\/ssids\/([^/]+)$/.exec(path);
    if (method === 'PATCH' && m) {
      const s = ssids.find((x) => x.id === decodeURIComponent(m[1]))!;
      const change = JSON.parse(body) as { enabled?: boolean };
      if (change.enabled !== undefined) s.enabled = change.enabled;
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

async function render(ui: React.ReactElement, until: (r: Root) => boolean) {
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
  await settle(() => until(root!));
  return root;
}

const all = (r: Root, id: string) => r.container.queryAll((n) => n.props.testID === id);
const byId = (r: Root, id: string) => all(r, id)[0];
const text = (r: Root) => texts(r.container).join('\n');
const disabled = (r: Root, id: string) => all(r, id).some((n) => n.props.disabled === true || n.props.accessibilityState?.disabled === true);
const press = async (r: Root, id: string) => {
  const node = byId(r, id);
  if (!node) throw new Error(`no ${id}`);
  await act(async () => void node.props.onClick({ nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined }));
};
const type = async (r: Root, id: string, value: string) => {
  const node = all(r, id).find((n) => typeof n.props.onChangeText === 'function');
  if (!node) throw new Error(`no input ${id}`);
  await act(async () => void node.props.onChangeText(value));
};
const flip = async (r: Root, id: string, value: boolean) => {
  const node = all(r, id).find((n) => typeof n.props.onChange === 'function');
  if (!node) throw new Error(`no switch ${id}`);
  await act(async () => void node.props.onChange({ nativeEvent: { value } }));
};
const writes = () => request.mock.calls.filter(([m, p]) => m !== 'GET' && p !== '/api/usage/sample');

let alert: jest.SpyInstance;
const sayYes = () => alert.mockImplementation((_t, _m, buttons?: { onPress?: () => void }[]) => buttons?.[1]?.onPress?.());

beforeEach(() => {
  request.mockReset();
  mockMac = SELF;
  alert = jest.spyOn(Alert, 'alert').mockImplementation(() => undefined);
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
  const now = Math.floor(Date.now() / 1000);
  useEntitlement.setState({
    boxId: 'ZTEG00000001',
    token: 'x.y',
    claims: { account: 'acc_1', boxId: 'ZTEG00000001', tier: 'max', expiresAt: now + 86400, issuedAt: now, keyId: 'k' },
  });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = qc = null;
  alert.mockRestore();
});

const sheetReady = (r: Root) => !!byId(r, 'device-rename-input');

describe('the sheet of a phone Kergui never blocks', () => {
  // Since #108: « Bloqué » is a choice, off for these phones, with the reason.
  const blockedChoice = (r: Root) => byId(r, 'device-trust-blocked');

  test('this phone: « Bloqué » off with « C’est ce téléphone », and the reason', async () => {
    box();
    const r = await render(<DeviceScreen />, sheetReady);
    await settle(() => text(r).includes(THIS_PHONE) && !!blockedChoice(r));
    expect(blockedChoice(r).props.accessibilityState).toMatchObject({ disabled: true });
    expect(text(r)).toContain('C’est ce téléphone');
    expect(byId(r, 'device-toggle-wifi-cut')).toBeUndefined();
    // No Wi-Fi switch under a « Bloqué » it cannot take.
    expect(byId(r, 'device-trust-more')).toBeUndefined();
    expect(byId(r, 'device-never-blocked')).toBeDefined();
    // Its name can still be changed.
    expect(byId(r, 'device-rename-input')).toBeDefined();
  });

  test('an owner’s phone: the same, in its own words', async () => {
    mockMac = OWNER;
    box();
    const r = await render(<DeviceScreen />, sheetReady);
    await settle(() => text(r).includes(OWNER_PHONE) && !!blockedChoice(r));
    expect(blockedChoice(r).props.accessibilityState).toMatchObject({ disabled: true });
    expect(text(r)).toContain('C’est un téléphone du propriétaire');
    expect(text(r)).not.toContain(THIS_PHONE);
  });

  test('blocked or cut before it was known: letting it back stays, free', async () => {
    box({ devices: [device(SELF, 'Mon-Telephone', { Mark: 'Self', Blocked: true })], denied: [SELF] });
    const r = await render(<DeviceScreen />, sheetReady);
    await settle(() => !!byId(r, 'device-blocked-badge'));
    expect(text(r)).toContain('Ne peut plus se connecter');
    expect(byId(r, 'device-trust-check').props.accessibilityState).toMatchObject({ disabled: false });
    expect(byId(r, 'device-trust-authorised').props.accessibilityState).toMatchObject({ disabled: false });
    expect(text(r)).not.toContain('Couper l’accès');
  });

  test('any other device can be blocked, and refused at the Wi-Fi too', async () => {
    mockMac = PC;
    box();
    const r = await render(<DeviceScreen />, sheetReady);
    await settle(() => !!byId(r, 'device-trust-more'));
    expect(blockedChoice(r).props.accessibilityState).toMatchObject({ disabled: false });
    expect(byId(r, 'device-never-blocked')).toBeUndefined();
  });
});

const wifiReady = (r: Root) => !!byId(r, `wifi-ssid-${AP5}`);

describe('the Wi-Fi tab and this phone', () => {
  test('the network this phone is on says so, and its switch stays on', async () => {
    box();
    const r = await render(<WiFi />, wifiReady);
    await settle(() => !!byId(r, `wifi-ssid-this-phone-${AP1}`));
    expect(texts(byId(r, `wifi-ssid-this-phone-${AP1}`)).join('')).toBe(ON_THIS_NETWORK);
    expect(byId(r, `wifi-ssid-this-phone-${AP5}`)).toBeUndefined();
    expect(disabled(r, `wifi-ssid-switch-${AP1}`)).toBe(true);
    expect(disabled(r, `wifi-ssid-switch-${AP5}`)).toBe(false);
  });

  test('the last network on cannot be turned off', async () => {
    // This phone is elsewhere: only the rule of the last network holds.
    box({ devices: [device(PC, 'Laptop-Bureau')], ssids: { [AP5]: { enabled: false } } });
    const r = await render(<WiFi />, wifiReady);
    await settle(() => !!byId(r, `wifi-ssid-last-${AP1}`));
    expect(disabled(r, `wifi-ssid-switch-${AP1}`)).toBe(true);
    // Turning the other one back on is always possible.
    expect(disabled(r, `wifi-ssid-switch-${AP5}`)).toBe(false);
    expect(byId(r, `wifi-ssid-this-phone-${AP1}`)).toBeUndefined();
    // A flip that would get through anyway sends nothing.
    await flip(r, `wifi-ssid-switch-${AP1}`, false);
    expect(writes()).toEqual([]);
  });

  test('another network still turns off, as before', async () => {
    box();
    const r = await render(<WiFi />, wifiReady);
    sayYes();
    await flip(r, `wifi-ssid-switch-${AP5}`, false);
    await settle(() => writes().length > 0);
    expect(writes().map(([m, p, b]) => [m, decodeURIComponent(p), JSON.parse(b)])).toEqual([
      ['PATCH', `/api/wifi/ssids/${AP5}`, { enabled: false }],
    ]);
  });

  test('a new password for this phone’s network says it will be disconnected', async () => {
    box();
    const r = await render(<WiFi />, wifiReady);
    await press(r, `wifi-ssid-password-open-${AP1}`);
    await type(r, `wifi-ssid-password-${AP1}`, 'nouveau-mdp-123');
    await press(r, `wifi-ssid-password-save-${AP1}`);
    expect(alert).toHaveBeenCalledTimes(1);
    expect(alert.mock.calls[0][1]).toBe(PASSWORD_THIS_PHONE);
  });

  test('a new password for another network does not', async () => {
    box();
    const r = await render(<WiFi />, wifiReady);
    await press(r, `wifi-ssid-password-open-${AP5}`);
    await type(r, `wifi-ssid-password-${AP5}`, 'nouveau-mdp-123');
    await press(r, `wifi-ssid-password-save-${AP5}`);
    expect(alert).toHaveBeenCalledTimes(1);
    expect(alert.mock.calls[0][1]).not.toContain('ce téléphone');
  });
});

describe('the core’s refusal, in words', () => {
  test.each([
    ['self_device', THIS_PHONE],
    ['self_network', 'Ce téléphone est connecté à ce réseau : Kergui ne l’éteint pas.'],
    ['last_network', 'C’est le dernier réseau Wi-Fi allumé : Kergui ne l’éteint pas.'],
    // Closing the Wi-Fi with neither this phone nor a relay known (RFC-007).
    [
      'allow_list',
      'Kergui ne trouve ni ce téléphone ni le relais sur la box : en réservant le Wi-Fi à la maison, plus personne de la maison ne pourrait s’y connecter. Rien n’a changé.',
    ],
  ])('409 self_lockout %s', (reason, sentence) => {
    expect(errorMessage(new ApiError(409, 'refusing…', 'self_lockout', { reason }))).toBe(sentence);
  });

  test('through the relay', () => {
    // There it is the relay's phone the box would lose.
    expect(errorMessage(new RemoteCommandError('self_lockout', undefined, 409, 'self_network'))).toBe(
      'Le relais est connecté à ce réseau : Kergui ne l’éteint pas. Rien n’a changé sur la box.',
    );
    expect(errorMessage(new RemoteCommandError('self_lockout', undefined, 409, 'allow_list'))).toBe(
      'La box ne connaît pas le relais : en réservant le Wi-Fi à la maison, il ne pourrait plus s’y connecter. Rien n’a changé sur la box.',
    );
  });
});
