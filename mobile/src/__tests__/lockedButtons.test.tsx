/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Locked actions show their lock before the tap (#120, audit H4, M10): the
// button is outlined with a lock, keeps the action's name, and says « Pas
// disponible pour votre compte » under it, which the screen reader hears
// too. A tap still explains, in an alert titled with the action. Neutral:
// no price, no link, no « Max » (RFC-006, the Play app has no payment).
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Alert, Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

const mockPush = jest.fn();
let mockMac = 'AC:BB:CC:00:00:22';
jest.mock('expo-router', () => ({
  router: { push: (...a: unknown[]) => mockPush(...a), replace: jest.fn(), back: jest.fn(), navigate: jest.fn() },
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
jest.mock('../features/relayPolicy', () => ({ syncRelayPolicy: async () => 'published' }));
// « Internet de la maison » shows only with a relay: here, always.
const mockHouseRun = jest.fn();
jest.mock('../features/queries', () => ({
  ...jest.requireActual('../features/queries'),
  useHouseInternet: () => ({ available: true, run: { mutate: mockHouseRun, isPending: false, isSuccess: false, error: null } }),
}));

import KerguiCore from '../../modules/kergui-core';
import UsageTab from '../app/(tabs)/usage';
import WiFi from '../app/(tabs)/wifi';
import DeviceScreen from '../app/device/[mac]';
import { fr } from '../i18n/fr';
import { useEntitlement } from '../licensing/entitlement';
import { useSession } from '../session/store';
import { AutoRejectSetting } from '../ui/AutoRejectSetting';
import { HouseInternet } from '../ui/HouseInternet';

const NEUTRAL = 'Cette fonctionnalité n’est pas disponible pour votre compte.';
const SHORT = 'Pas disponible pour votre compte';
// What the Play app must never say (scripts/check-play-bundle.sh, and no
// « Max » call to action).
const PAYMENT = /prix|payer|paiement|acheter|achat|abonne|FCFA|XOF|Wave|Orange Money|https?:|\bMax\b|\bBasic\b/i;
const PC = 'AC:BB:CC:00:00:22';
const AP1 = 'DEV.WIFI.AP1';
const AP5 = 'DEV.WIFI.AP5';
const request = KerguiCore.request as jest.Mock;

const device = (mac: string, name: string, over: Record<string, unknown> = {}) => ({
  ID: mac, MAC: mac, IP: '192.168.1.10', Hostname: name, CustomName: '', Vendor: '', SSID: '',
  FirstSeen: '', LastSeen: '', Connected: true, Blocked: false, NewlySeen: false, Approved: true, ...over,
});

function box(opts: { blocked?: boolean } = {}) {
  request.mockImplementation(async (method: string, path: string) => {
    if (method === 'GET' && path === '/api/devices') {
      return { status: 200, body: JSON.stringify([device(PC, 'PC-Bureau', { Blocked: !!opts.blocked })]) };
    }
    if (method === 'GET' && path === '/api/wifi/blocked') return { status: 200, body: '[]' };
    if (method === 'GET' && path === '/api/wifi/ssids') {
      return {
        status: 200,
        body: JSON.stringify([
          { id: AP1, alias: 'SSID1', band: '2.4GHz', name: 'MonWiFi-2G', enabled: true, hidden: false, security: 'WPA2' },
          { id: AP5, alias: 'SSID5', band: '5GHz', name: 'MonWiFi-5G', enabled: true, hidden: false, security: 'WPA2' },
        ]),
      };
    }
    if (method === 'GET' && path === '/api/autoreject') return { status: 200, body: '{"enabled":false,"devices":[]}' };
    if (method === 'POST' && path === '/api/usage/sample') return { status: 200, body: '{"devices":1,"bytes":0,"baseline":false}' };
    if (method === 'GET' && path.startsWith('/api/usage')) {
      return { status: 200, body: JSON.stringify([{ mac: PC.toLowerCase(), period: '2026-11', bytes: 12_000_000, sent: 0, received: 0 }]) };
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

async function render(ui: React.ReactElement, ready: (r: Root) => boolean) {
  qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false, gcTime: 0 } } });
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc!}>
        <PaperProvider settings={{ icon: iconAsText }}>{ui}</PaperProvider>
      </QueryClientProvider>,
    );
  });
  await settle(() => ready(root!));
  return root!;
}

const all = (r: Root, id: string) => r.container.queryAll((n) => n.props.testID === id);
const byId = (r: Root, id: string) => all(r, id)[0];
const text = (r: Root) => texts(r.container).join('\n');
/** The node under testID the screen reader reads (the touchable). */
const touchable = (r: Root, id: string) => all(r, id).find((n) => typeof n.props.onClick === 'function');
const press = async (r: Root, id: string) => {
  const node = touchable(r, id);
  if (!node) throw new Error(`no ${id}`);
  await act(async () => void node.props.onClick({ nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined }));
};
const writes = () =>
  request.mock.calls.filter(([m, p]) => m !== 'GET' && p !== '/api/usage/sample').map(([m, p]) => `${m} ${p}`);

/** A locked button: outlined, the lock, its own label, the line under it, the hint. */
function expectLocked(r: Root, id: string, label: string) {
  const node = touchable(r, id);
  expect(node).toBeDefined();
  const inside = texts(node!);
  expect(inside).toContain('icon:lock');
  expect(inside.join('\n')).toContain(label);
  expect(node!.props.accessibilityHint).toBe(SHORT);
  expect(texts(byId(r, `${id}-locked`)).join('')).toBe(SHORT);
}

function expectOpen(r: Root, id: string) {
  const node = touchable(r, id);
  expect(node).toBeDefined();
  expect(texts(node!)).not.toContain('icon:lock');
  expect(node!.props.accessibilityHint).toBeUndefined();
  expect(byId(r, `${id}-locked`)).toBeUndefined();
}

function as(who: 'demo' | 'free' | 'basic' | 'max') {
  const now = Math.floor(Date.now() / 1000);
  useSession.setState({ session: who === 'demo' ? { mode: 'demo' } : { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
  useEntitlement.setState(
    who === 'basic' || who === 'max'
      ? {
          boxId: 'ZTEG00000001',
          token: 'x.y',
          claims: { account: 'acc_1', boxId: 'ZTEG00000001', tier: who, expiresAt: now + 86400, issuedAt: now, keyId: 'k' },
        }
      : { boxId: 'ZTEG00000001', token: null, claims: null },
  );
}

let alert: jest.SpyInstance;
beforeEach(() => {
  request.mockReset();
  mockPush.mockReset();
  mockHouseRun.mockReset();
  mockMac = PC;
  alert = jest.spyOn(Alert, 'alert').mockImplementation(() => undefined);
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = qc = null;
  alert.mockRestore();
});

test('the words: a short neutral line, the Conso arrow and the shorter caveat (audit H4, M10)', () => {
  expect(fr['pro.lockedShort']).toBe(SHORT);
  expect(fr['usage.prevMonthLocked']).toBe('Mois précédent, pas disponible pour votre compte');
  expect(fr['usage.estimate']).toBe('Compté quand Kergui tourne : le vrai total est plus haut.');
  for (const k of ['pro.lockedShort', 'usage.prevMonthLocked', 'pro.locked'] as const) expect(fr[k]).not.toMatch(PAYMENT);
});

describe('device sheet', () => {
  // Since #108 the sheet's choices (Autorisé | À vérifier | Bloqué) and, under
  // « Bloqué », the switch that replaced « Couper l’accès ».
  const ready = (r: Root) => !!touchable(r, 'device-trust-blocked') && !!touchable(r, 'device-trust-more');
  const openMore = async (r: Root) => {
    await press(r, 'device-trust-more');
    await settle(() => !!touchable(r, 'device-trust-cut-row'));
  };

  test('with no pass, « Bloqué » and the Wi-Fi switch show the lock before the tap', async () => {
    as('free');
    box();
    const r = await render(<DeviceScreen />, ready);
    expectLocked(r, 'device-trust-blocked', 'Bloqué');
    await openMore(r);
    const cutRow = texts(touchable(r, 'device-trust-cut-row')!);
    expect(cutRow).toContain('icon:lock');
    expect(cutRow).toContain(SHORT);
    expect(text(r)).not.toMatch(PAYMENT);
  });

  test('a tap explains, in an alert titled with the action, and sends nothing', async () => {
    as('free');
    box();
    const r = await render(<DeviceScreen />, ready);
    await press(r, 'device-trust-blocked');
    expect(alert).toHaveBeenCalledTimes(1);
    const [title, message, buttons] = alert.mock.calls[0];
    expect(title).toBe('Bloqué');
    expect(message).toBe(NEUTRAL);
    expect(buttons).toHaveLength(1);
    await openMore(r);
    await press(r, 'device-trust-cut-row');
    expect(alert.mock.calls[1][0]).toBe('Ne plus le laisser se connecter du tout (même au Wi-Fi)');
    expect(writes()).toEqual([]);
    expect(mockPush).not.toHaveBeenCalled();
  });

  test('with Basic, « Bloqué » and the switch are open', async () => {
    as('basic');
    box();
    const r = await render(<DeviceScreen />, ready);
    expectOpen(r, 'device-trust-blocked');
    await openMore(r);
    expect(texts(touchable(r, 'device-trust-cut-row')!)).not.toContain('icon:lock');
    expect(text(r)).not.toContain(SHORT);
  });

  test('« Débloquer » (back to « À vérifier ») is free: never locked', async () => {
    as('free');
    box({ blocked: true });
    const r = await render(<DeviceScreen />, (x) => !!byId(x, 'device-blocked-badge'));
    expectOpen(r, 'device-trust-check');
    expectOpen(r, 'device-trust-authorised');
  });
});

describe('Wi-Fi tab', () => {
  const ready = (r: Root) => !!touchable(r, `wifi-ssid-rename-${AP1}`);

  test('with no pass, « Renommer » and « Changer le mot de passe » show the lock before the tap', async () => {
    as('free');
    box();
    const r = await render(<WiFi />, ready);
    expectLocked(r, `wifi-ssid-rename-${AP1}`, 'Renommer');
    expectLocked(r, `wifi-ssid-password-open-${AP1}`, 'Changer le mot de passe');
    // The switch that turns a network off says it too, with its lock.
    const sw = all(r, `wifi-ssid-switch-${AP5}`).find((n) => n.props.accessibilityHint);
    expect(sw?.props.accessibilityHint).toBe(SHORT);
    expect(byId(r, `wifi-ssid-switch-${AP5}-locked`)).toBeDefined();
    expect(text(r)).not.toMatch(PAYMENT);
  });

  test('a locked tap is titled with the action', async () => {
    as('free');
    box();
    const r = await render(<WiFi />, ready);
    await press(r, `wifi-ssid-password-open-${AP1}`);
    expect(alert.mock.calls[0][0]).toBe('Changer le mot de passe');
    expect(alert.mock.calls[0][1]).toBe(NEUTRAL);
    await press(r, `wifi-ssid-rename-${AP1}`);
    expect(alert.mock.calls[1][0]).toBe('Renommer');
    expect(writes()).toEqual([]);
  });

  test('with Max, nothing is locked', async () => {
    as('max');
    box();
    const r = await render(<WiFi />, ready);
    expectOpen(r, `wifi-ssid-password-open-${AP1}`);
    expect(byId(r, `wifi-ssid-rename-${AP1}-locked`)).toBeUndefined();
    expect(byId(r, `wifi-ssid-switch-${AP5}-locked`)).toBeUndefined();
    expect(text(r)).not.toContain(SHORT);
  });
});

describe('Conso tab', () => {
  const ready = (r: Root) => text(r).includes('12 Mo');

  test('the locked arrow says so to the screen reader, and its alert has a title', async () => {
    as('free');
    box();
    const r = await render(<UsageTab />, ready);
    const arrow = all(r, 'usage-month-prev').find((n) => n.props.accessibilityLabel);
    expect(arrow?.props.accessibilityLabel).toBe('Mois précédent, pas disponible pour votre compte');
    expect(texts(r.container)).toContain('icon:lock');
    await press(r, 'usage-month-prev');
    expect(alert).toHaveBeenCalledWith('Mois précédent', NEUTRAL, expect.anything());
    expect(text(r)).toContain('Compté quand Kergui tourne : le vrai total est plus haut.');
    expect(text(r)).not.toMatch(PAYMENT);
  });

  test('with Max, the arrow is « Mois précédent »', async () => {
    as('max');
    box();
    const r = await render(<UsageTab />, ready);
    const arrow = all(r, 'usage-month-prev').find((n) => n.props.accessibilityLabel);
    expect(arrow?.props.accessibilityLabel).toBe('Mois précédent');
  });
});

describe('auto-reject', () => {
  test('with no pass, the switch says it is not available, to the screen reader too', async () => {
    as('free');
    box();
    const r = await render(<AutoRejectSetting />, (x) => !!byId(x, 'settings-auto-reject-locked'));
    const sw = all(r, 'settings-auto-reject').find((n) => n.props.accessibilityHint);
    expect(sw?.props.accessibilityHint).toBe(SHORT);
    expect(text(r)).toContain(NEUTRAL);
    expect(text(r)).not.toMatch(PAYMENT);
  });

  test('with Max, no hint', async () => {
    as('max');
    box();
    const r = await render(<AutoRejectSetting />, (x) => request.mock.calls.some(([, p]) => p === '/api/autoreject') && !!x);
    await settle();
    expect(all(r, 'settings-auto-reject').some((n) => n.props.accessibilityHint)).toBe(false);
    expect(byId(r, 'settings-auto-reject-locked')).toBeUndefined();
  });
});

describe('« Internet de la maison »', () => {
  test('with no pass, « Couper Internet » shows the lock; « Rétablir Internet » stays open', async () => {
    as('free');
    box();
    const r = await render(<HouseInternet />, (x) => !!touchable(x, 'house-cut'));
    expectLocked(r, 'house-cut', 'Couper Internet');
    expectOpen(r, 'house-restore');
    await press(r, 'house-cut');
    expect(alert.mock.calls[0][0]).toBe('Couper Internet');
    expect(mockHouseRun).not.toHaveBeenCalled();
  });
});

test('the demo opens everything: no lock anywhere', async () => {
  as('demo');
  box();
  let r = await render(<DeviceScreen />, (x) => !!touchable(x, 'device-trust-blocked'));
  expectOpen(r, 'device-trust-blocked');
  await act(async () => r.unmount());
  r = await render(<WiFi />, (x) => !!touchable(x, `wifi-ssid-rename-${AP1}`));
  expect(text(r)).not.toContain(SHORT);
  expect(byId(r, `wifi-ssid-switch-${AP5}-locked`)).toBeUndefined();
  await act(async () => r.unmount());
  root = r = await render(<UsageTab />, (x) => text(x).includes('12 Mo'));
  expect(all(r, 'usage-month-prev').find((n) => n.props.accessibilityLabel)?.props.accessibilityLabel).toBe('Mois précédent');
});
