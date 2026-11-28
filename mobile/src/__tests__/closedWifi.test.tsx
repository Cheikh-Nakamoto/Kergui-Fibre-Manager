/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The closed Wi-Fi in the app (#108, RFC-007 D12), as the owner arbitrated
// it (#111): « Réservé à la maison ». The card in the Wi-Fi tab (#134: no
// more at the top of Appareils) and its panel, the cadenas that opens
// nothing (R1), the reopening always free, the device's pill and choices,
// the undo bar (R6), the « Autorisés » tab and its places, « Voici les
// appareils de la maison » (R5, R8), « Réseaux des visiteurs » from the
// Wi-Fi tab, and a box that cannot do it, where all of it is hidden (D11).
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Alert, Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

const mockPush = jest.fn();
const mockBack = jest.fn();
let mockParams: Record<string, string> = {};
jest.mock('expo-router', () => ({
  router: { push: (...a: unknown[]) => mockPush(...a), replace: jest.fn(), back: () => mockBack() },
  Stack: { Screen: () => null },
  useLocalSearchParams: () => mockParams,
}));
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('../alerts/alerts', () => ({ checkNewDevices: async () => 0, enforceAutoReject: async () => 0 }));
jest.mock('../licensing/config', () => ({ config: { hubUrl: '', publicKeys: [] } }));

import KerguiCore from '../../modules/kergui-core';
import Devices from '../app/(tabs)/devices';
import WiFi from '../app/(tabs)/wifi';
import DeviceScreen from '../app/device/[mac]';
import AutoRejectScreen from '../app/auto-reject';
import HouseDevices from '../app/house-devices';
import WiFiVisitors from '../app/wifi-visitors';
import type { Device, WiFiLockStatus } from '../core/types';
import { useLockPanel, useLockPrefs, useTrustState } from '../features/wifiLock';
import { useEntitlement } from '../licensing/entitlement';
import { useSession } from '../session/store';

const SELF = 'fa:4b:47:00:00:86';
const PC = 'ac:bb:cc:00:00:22';
const TV = 'ac:bb:cc:00:00:44';
const STRANGER = 'ac:bb:cc:00:00:55';
const ENC = encodeURIComponent;
const request = KerguiCore.request as jest.Mock;
const NEUTRAL = 'Cette fonctionnalité n’est pas disponible pour votre compte.';

const dev = (mac: string, over: Partial<Device> = {}): Device => ({
  ID: mac,
  MAC: mac,
  IP: '192.168.1.10',
  Hostname: '',
  CustomName: '',
  Vendor: '',
  SSID: 'MonWiFi-2G',
  FirstSeen: new Date(Date.now() - 3_600_000).toISOString(),
  LastSeen: new Date().toISOString(),
  Connected: true,
  Blocked: false,
  NewlySeen: false,
  ...over,
});

type Box = {
  lock: WiFiLockStatus | 'unsupported';
  self: string;
  approved: Set<string>;
  blocked: Set<string>;
  cut: Set<string>;
  places: number;
  aps?: WiFiLockStatus['aps'];
};

let box: Box;

const lockStatus = (over: Partial<WiFiLockStatus> = {}): WiFiLockStatus => ({
  state: 'open',
  aps: [{ id: 'DEV.WIFI.AP1', band: '2.4GHz', name: 'MonWiFi-2G', mode: 'Disabled', visitors: false }],
  household_aps: ['DEV.WIFI.AP1'],
  visitor_aps: [],
  visitors_closed: false,
  window: { until: '', minutes_left: 0, can_extend: false },
  allowed: 0,
  places: 32,
  places_known: true,
  found_open: false,
  intended: false,
  since: '',
  cut: [],
  ...over,
});

/** The phone's core, as the closed Wi-Fi's routes answer (mobilecore, httpapi/wifilock.go). */
function core(b: Partial<Box> = {}) {
  box = {
    lock: lockStatus(),
    self: SELF,
    approved: new Set([PC, TV]),
    blocked: new Set(),
    cut: new Set(),
    places: 32,
    ...b,
  };
  request.mockImplementation(async (method: string, path: string, raw: string) => {
    const ok = (v: unknown = {}) => ({ status: 200, body: JSON.stringify(v) });
    const body = raw ? JSON.parse(raw) : {};
    if (method === 'GET' && path === '/api/devices')
      return ok(
        [
          dev(SELF, { Mark: 'Self', Hostname: 'Mon-Telephone' }),
          dev(PC, { Hostname: 'Laptop-Bureau' }),
          dev(TV, { CustomName: 'TV salon' }),
          dev(STRANGER),
        ].map((d) => ({ ...d, Approved: box.approved.has(d.MAC) || d.Mark === 'Self', Blocked: box.blocked.has(d.MAC) })),
      );
    if (method === 'GET' && path === '/api/wifi/blocked') return ok([]);
    if (method === 'GET' && path === '/api/blocked') return ok([]);
    if (method === 'GET' && path === '/api/wifi/ssids')
      return ok([{ id: 'DEV.WIFI.AP1', alias: 'SSID1', band: '2.4GHz', name: 'MonWiFi-2G', enabled: true, hidden: false, security: 'WPA2-PSK-AES' }]);
    if (method === 'GET' && path === '/api/wifi/lock') {
      if (box.lock === 'unsupported') return { status: 501, body: '{"error":"not implemented: adapter cannot close the Wi-Fi"}' };
      return ok({ ...box.lock, cut: [...box.cut] });
    }
    if (method === 'POST' && path === '/api/wifi/lock/close') {
      const devices = [box.self, ...box.approved].filter(Boolean);
      const p = {
        household_aps: ['DEV.WIFI.AP1'],
        visitor_aps: body.visitor_aps ?? [],
        closed_aps: ['DEV.WIFI.AP1'],
        visitors_closed: !!body.visitors_closed,
        devices,
        always: [box.self].filter(Boolean),
        relays: [],
        self: box.self,
        self_private: true,
        places: box.places,
        places_known: true,
        full: null,
        closed: !body.preview,
      };
      if (!body.preview && box.lock !== 'unsupported') box.lock = { ...box.lock, state: 'closed', intended: true };
      return ok(p);
    }
    if (method === 'POST' && path === '/api/wifi/lock/open') {
      if (box.lock !== 'unsupported') box.lock = { ...box.lock, state: 'open', intended: false };
      return ok({ open: true, restoring: [] });
    }
    if (method === 'POST' && path === '/api/wifi/lock/window') return ok({ action: body.action, window: { until: '', minutes_left: 5, can_extend: true } });
    if (method === 'GET' && path === '/api/approvals') return ok({ macs: [...box.approved], unsynced: [] });
    if (method === 'GET' && path === '/api/relay-devices') return ok({ macs: [] });
    const ap = /^\/api\/approvals\/(.+)$/.exec(path);
    if (ap && method === 'PUT') return box.approved.add(decodeURIComponent(ap[1])), ok();
    if (ap && method === 'DELETE') return box.approved.delete(decodeURIComponent(ap[1])), ok();
    const m = /^\/api\/devices\/([^/]+)\/(block|unblock|accept)$/.exec(path);
    if (m && method === 'POST') {
      const mac = decodeURIComponent(m[1]);
      if (m[2] === 'block') {
        box.blocked.add(mac);
        box.approved.delete(mac); // the core withdraws the authorisation (D1)
        if (body.cut_wifi) box.cut.add(mac);
        else if (body.cut_wifi === false) box.cut.delete(mac);
      }
      if (m[2] === 'unblock') {
        box.blocked.delete(mac);
        box.cut.delete(mac);
      }
      return ok();
    }
    if (method === 'GET' && path === '/api/autoreject') return ok({ enabled: false, since: '', delay_minutes: 15, devices: [] });
    if (method === 'POST' && path === '/api/usage/sample') return ok({ devices: 0, bytes: 0, baseline: true });
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

async function settle(until: () => boolean = () => false) {
  for (let i = 0; i < 60 && !until(); i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 10))));
}

async function render(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false, gcTime: 0 } } });
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
const type = async (r: Root, id: string, value: string) => {
  const node = r.container.queryAll((n) => n.props.testID === id && typeof n.props.onChangeText === 'function')[0];
  await act(async () => void node.props.onChangeText(value));
};
const calls = (method: string, path: string) => request.mock.calls.filter(([m, p]) => m === method && p === path);

function tier(t: 'free' | 'basic' | 'max') {
  const now = Math.floor(Date.now() / 1000);
  useEntitlement.setState(
    t === 'free'
      ? { boxId: 'ZTEG00000001', token: null, claims: null }
      : {
          boxId: 'ZTEG00000001',
          token: 'x.y',
          claims: { account: 'acc_1', boxId: 'ZTEG00000001', tier: t, expiresAt: now + 86400, issuedAt: now, keyId: 'k' },
        },
  );
}

let alert: jest.SpyInstance;
/** « Oui » on the one confirmation (the system alert: no ConfirmHost here). */
const sayYes = () => alert.mockImplementation((_t, _m, buttons?: { onPress?: () => void }[]) => buttons?.[1]?.onPress?.());

beforeEach(() => {
  request.mockReset();
  mockPush.mockReset();
  mockBack.mockReset();
  mockParams = {};
  alert = jest.spyOn(Alert, 'alert').mockImplementation(() => undefined);
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
  // Reviewed once already: the card shows no « Voir les appareils » unless said.
  useLockPrefs.setState({ loaded: true, byBox: { 'http://192.168.1.1': { reviewed: true } } });
  useLockPanel.setState({ open: false });
  useTrustState.setState({ pending: {}, last: null });
  tier('max');
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
  alert.mockRestore();
});

describe('the card in the Wi-Fi tab: the box’s state in words (R2, R3, R12, #134)', () => {
  it('open: « Ouvert avec mot de passe », and « Gérer »', async () => {
    core();
    const r = await render(<WiFi />);
    await settle(() => !!byId(r, 'lock-card'));
    expect(texts(byId(r, 'lock-state'))).toEqual(['Ouvert avec mot de passe']);
    expect(byId(r, 'lock-manage')).toBeDefined();
    // Never in the header (R3), never an emoji (R13).
    expect(text(r)).not.toMatch(/🔒|🔓|✓|⛔/);
  });

  it('reserved: « Réservé à la maison », with what it means', async () => {
    core({ lock: lockStatus({ state: 'closed', intended: true }) });
    const r = await render(<WiFi />);
    await settle(() => !!byId(r, 'lock-card'));
    expect(texts(byId(r, 'lock-state'))).toEqual(['Réservé à la maison']);
    expect(text(r)).toContain('Le mot de passe ou un QR code partagé ne suffisent plus : seuls vos appareils entrent. Internet marche.');
  });

  it('while the box is written: « En cours… »', async () => {
    core({ lock: lockStatus({ state: 'busy' }) });
    const r = await render(<WiFi />);
    await settle(() => !!byId(r, 'lock-card'));
    expect(texts(byId(r, 'lock-state'))).toEqual(['En cours…']);
  });

  it('an add window counts down: « Ajout d’appareil : encore 3 min »', async () => {
    const until = new Date(Date.now() + 150_000).toISOString();
    core({ lock: lockStatus({ state: 'window', intended: true, window: { until, minutes_left: 3, can_extend: true } }) });
    const r = await render(<WiFi />);
    await settle(() => !!byId(r, 'lock-card'));
    expect(texts(byId(r, 'lock-state'))).toEqual(['Ajout d’appareil : encore 3 min']);
  });

  it('an add window nobody closed: « L’ajout est terminé »', async () => {
    core({ lock: lockStatus({ state: 'open', intended: true }) });
    const r = await render(<WiFi />);
    await settle(() => !!byId(r, 'lock-ended'));
    expect(texts(byId(r, 'lock-ended')).join('')).toBe('L’ajout est terminé : réservez à nouveau votre Wi-Fi à la maison.');
  });
});

describe('the panel: nothing changes without a second gesture', () => {
  it('« Réserver » is off while the core does not find this phone on the box', async () => {
    core({ self: '' });
    const r = await render(<WiFi />);
    await settle(() => !!byId(r, 'lock-manage'));
    await press(byId(r, 'lock-manage'));
    await settle(() => !!byId(r, 'lock-reserve-why'));
    expect(texts(byId(r, 'lock-reserve-why')).join('')).toBe('Autorisez d’abord ce téléphone : Kergui ne le trouve pas sur la box.');
    expect(byId(r, 'lock-reserve').props.accessibilityState?.disabled).toBe(true);
  });

  it('this phone found: one confirmation, then the box is reserved', async () => {
    core();
    sayYes();
    const r = await render(<WiFi />);
    await settle(() => !!byId(r, 'lock-manage'));
    await press(byId(r, 'lock-manage'));
    await settle(() => !!byId(r, 'lock-count'));
    // The private address advice, in plain words (R10).
    expect(text(r)).toContain('Si vous oubliez ce Wi-Fi dans les paramètres du téléphone');
    expect(calls('POST', '/api/wifi/lock/close').every(([, , b]) => JSON.parse(b).preview === true)).toBe(true);
    await press(byId(r, 'lock-reserve'));
    await settle(() => calls('POST', '/api/wifi/lock/close').some(([, , b]) => !JSON.parse(b).preview));
    expect(alert).toHaveBeenCalledTimes(1);
    expect(alert.mock.calls[0][0]).toBe('Réserver votre Wi-Fi à la maison ?');
    await settle(() => texts(byId(r, 'lock-state')).join('') === 'Réservé à la maison');
    expect(texts(byId(r, 'lock-state'))).toEqual(['Réservé à la maison']);
  });

  it('the cadenas « Max » shows the neutral sentence and opens nothing (R1)', async () => {
    core();
    tier('basic');
    const r = await render(<WiFi />);
    await settle(() => !!byId(r, 'lock-manage'));
    await press(byId(r, 'lock-manage'));
    await settle(() => !!byId(r, 'lock-reserve-locked'));
    expect(texts(byId(r, 'lock-reserve-locked'))).toEqual(['Pas disponible pour votre compte']);
    await press(byId(r, 'lock-reserve'));
    expect(alert).toHaveBeenCalledWith('Réserver mon Wi-Fi à la maison', NEUTRAL, expect.anything());
    expect(mockPush).not.toHaveBeenCalled();
    expect(calls('POST', '/api/wifi/lock/close').some(([, , b]) => !JSON.parse(b).preview)).toBe(false);
    expect(text(r)).not.toMatch(/prix|payer|abonne|offre/i);
  });

  it('reopening is free, even with no pass at all', async () => {
    core({ lock: lockStatus({ state: 'closed', intended: true }) });
    tier('free');
    const r = await render(<WiFi />);
    await settle(() => !!byId(r, 'lock-manage'));
    await press(byId(r, 'lock-manage'));
    await settle(() => !!byId(r, 'lock-reopen'));
    // « Ajouter un appareil » is Max: locked; reopening is not.
    expect(byId(r, 'lock-add-locked')).toBeDefined();
    expect(byId(r, 'lock-reopen-locked')).toBeUndefined();
    await press(byId(r, 'lock-reopen'));
    await settle(() => calls('POST', '/api/wifi/lock/open').length > 0);
    expect(alert).not.toHaveBeenCalled();
    await settle(() => texts(byId(r, 'lock-state')).join('') === 'Ouvert avec mot de passe');
  });

  it('the add window: « Encore 5 min », « Refermer maintenant » and the QR code (R12)', async () => {
    const until = new Date(Date.now() + 240_000).toISOString();
    core({ lock: lockStatus({ state: 'window', intended: true, window: { until, minutes_left: 4, can_extend: true } }) });
    const r = await render(<WiFi />);
    await settle(() => !!byId(r, 'lock-manage'));
    await press(byId(r, 'lock-manage'));
    await settle(() => !!byId(r, 'lock-countdown'));
    expect(texts(byId(r, 'lock-countdown'))).toEqual(['Ajout d’appareil : encore 4 min']);
    expect(byId(r, 'lock-qr')).toBeUndefined();
    await type(r, 'lock-qr-password', 'motdepasse-maison');
    expect(byId(r, 'lock-qr')).toBeDefined();
    await press(byId(r, 'lock-extend'));
    await settle(() => calls('POST', '/api/wifi/lock/window').length > 0);
    expect(JSON.parse(calls('POST', '/api/wifi/lock/window')[0][2])).toEqual({ action: 'extend' });
    await press(byId(r, 'lock-close-now'));
    await settle(() => calls('POST', '/api/wifi/lock/window').length > 1);
    expect(JSON.parse(calls('POST', '/api/wifi/lock/window')[1][2])).toEqual({ action: 'close' });
    // The password typed is never sent to the core.
    expect(request.mock.calls.some(([, , b]) => String(b).includes('motdepasse-maison'))).toBe(false);
  });

  it('asks once which network is for the visitors, with its band and devices (R9)', async () => {
    core({
      lock: lockStatus({
        aps: [
          { id: 'DEV.WIFI.AP1', band: '2.4GHz', name: 'MonWiFi-2G', mode: 'Disabled', visitors: false },
          { id: 'DEV.WIFI.AP5', band: '5GHz', name: 'MonWiFi-5G', mode: 'Disabled', visitors: false },
        ],
      }),
    });
    const r = await render(<WiFi />);
    await settle(() => !!byId(r, 'lock-manage'));
    await press(byId(r, 'lock-manage'));
    await settle(() => !!byId(r, 'lock-visitors-0'));
    expect(text(r)).toContain('Ce réseau est-il pour vos visiteurs ?');
    expect(text(r)).toContain('MonWiFi-5G · 5 GHz · 0 appareil connecté');
    // Not answered: « Réserver » waits.
    expect(byId(r, 'lock-reserve').props.accessibilityState?.disabled).toBe(true);
    await press(byId(r, 'lock-visitors-0-unsure'));
    await settle(() => byId(r, 'lock-reserve').props.accessibilityState?.disabled === false);
    // « Je ne sais pas » keeps it open: it goes as a visitors' network.
    const last = calls('POST', '/api/wifi/lock/close').at(-1)!;
    expect(JSON.parse(last[2]).visitor_aps).toEqual(['DEV.WIFI.AP5']);
    // …and is asked again next time.
    expect(byId(r, 'lock-visitors-0')).toBeDefined();
  });

  it('fewer than 2 authorised: « Voici les appareils de la maison » first', async () => {
    core({ approved: new Set([PC]) });
    useLockPrefs.setState({ loaded: true, byBox: {} });
    const r = await render(<WiFi />);
    await settle(() => !!byId(r, 'lock-manage'));
    // Offered when the feature arrives, before any reservation (R5).
    expect(byId(r, 'lock-review')).toBeDefined();
    await press(byId(r, 'lock-manage'));
    await settle(() => byId(r, 'lock-reserve')?.props.accessibilityState?.disabled === false);
    await press(byId(r, 'lock-reserve'));
    expect(mockPush).toHaveBeenCalledWith({ pathname: '/house-devices', params: { then: 'reserve' } });
  });
});

describe('the device’s pill and its choices (D5, R4, R6, R7)', () => {
  it('each device says its state with colour, icon and word', async () => {
    core({ blocked: new Set([STRANGER]) });
    const r = await render(<Devices />);
    await settle(() => !!byId(r, `device-trust-${PC}`));
    expect(texts(byId(r, `device-trust-${PC}`))).toContain('Autorisé');
    expect(texts(byId(r, `device-trust-${PC}`))).toContain('icon:check-decagram');
    await press(byId(r, 'devices-filter-blocked'));
    await settle(() => !!byId(r, `device-trust-${STRANGER}`));
    expect(texts(byId(r, `device-trust-${STRANGER}`))).toContain('Bloqué');
    expect(texts(byId(r, `device-blocked-sub-${STRANGER}`))).toEqual(['Pas d’internet']);
  });

  it('the pill opens the three choices; « Bloqué » sends no question, then « Annuler » stays', async () => {
    core();
    const r = await render(<Devices />);
    await settle(() => !!byId(r, `device-trust-${PC}`));
    await press(byId(r, `device-trust-${PC}`));
    await settle(() => !!byId(r, 'trust-panel-blocked'));
    expect(text(r)).toContain('Peut toujours se connecter.');
    expect(text(r)).toContain('Il n’a plus internet.');
    // R7: the Wi-Fi switch is folded under « Plus d'options ».
    expect(text(r)).toContain('Plus d’options');
    await press(byId(r, 'trust-panel-blocked'));
    await settle(() => calls('POST', `/api/devices/${ENC(PC)}/block`).length > 0);
    expect(alert).not.toHaveBeenCalled();
    await settle(() => !!byId(r, 'trust-undo-bar') && text(r).includes('Laptop-Bureau : bloqué'));
    expect(text(r)).toContain('Laptop-Bureau : bloqué');
    // No time limit (R6): still there a while later.
    await act(async () => void (await new Promise((ok) => setTimeout(ok, 300))));
    expect(text(r)).toContain('Laptop-Bureau : bloqué');
    // « Annuler » puts the device back where it was: unblocked and authorised.
    await press(byId(r, 'trust-undo'));
    await settle(() => calls('POST', `/api/devices/${ENC(PC)}/unblock`).length > 0 && calls('PUT', `/api/approvals/${ENC(PC)}`).length > 0);
    expect(box.blocked.has(PC)).toBe(false);
    expect(box.approved.has(PC)).toBe(true);
  });

  it('reserved: the middle choice is « Retirer de la maison », Basic like a block', async () => {
    core({ lock: lockStatus({ state: 'closed', intended: true }) });
    tier('free');
    mockParams = { mac: PC };
    const r = await render(<DeviceScreen />);
    await settle(() => !!byId(r, 'device-trust-check'));
    expect(text(r)).toContain('Retirer de la maison');
    expect(text(r)).toContain('Il sera déconnecté du Wi-Fi.');
    expect(byId(r, 'device-trust-check-locked')).toBeDefined();
    await press(byId(r, 'device-trust-check'));
    expect(alert).toHaveBeenCalledWith('Retirer de la maison', NEUTRAL, expect.anything());
    expect(calls('DELETE', `/api/approvals/${ENC(PC)}`)).toHaveLength(0);
  });

  it('the sheet: no more « Couper l’accès », the switch under « Bloqué » instead', async () => {
    core();
    mockParams = { mac: PC };
    const r = await render(<DeviceScreen />);
    await settle(() => !!byId(r, 'device-trust-blocked'));
    expect(text(r)).not.toContain('Couper l’accès');
    expect(byId(r, 'device-toggle-wifi-cut')).toBeUndefined();
    expect(byId(r, 'device-approved-badge')).toBeDefined();
  });

  it('this phone: « Bloqué » off with « C’est ce téléphone », and « Retirer » too when reserved', async () => {
    core({ lock: lockStatus({ state: 'closed', intended: true }) });
    mockParams = { mac: SELF };
    const r = await render(<DeviceScreen />);
    await settle(() => !!byId(r, 'device-trust-blocked'));
    expect(byId(r, 'device-trust-blocked').props.accessibilityState).toMatchObject({ disabled: true });
    expect(byId(r, 'device-trust-check').props.accessibilityState).toMatchObject({ disabled: true });
    expect(text(r)).toContain('C’est ce téléphone');
  });

  it('open: « Autoriser » shows at once, before the box is read again', async () => {
    core({ approved: new Set([TV]) });
    const r = await render(<Devices />);
    await settle(() => !!byId(r, `device-trust-${STRANGER}`));
    expect(texts(byId(r, `device-trust-${STRANGER}`))).toContain('À vérifier');
    // The core answers slowly: the pill has already changed.
    const slow = request.getMockImplementation()!;
    request.mockImplementation(async (m: string, p: string, b: string) => {
      if (m === 'PUT' && p.startsWith('/api/approvals/')) await new Promise((ok) => setTimeout(ok, 200));
      return slow(m, p, b);
    });
    await press(byId(r, `device-trust-${STRANGER}`));
    await settle(() => !!byId(r, 'trust-panel-authorised'));
    await press(byId(r, 'trust-panel-authorised'));
    await settle(() => texts(byId(r, `device-trust-${STRANGER}`)).includes('Autorisé'));
    expect(texts(byId(r, `device-trust-${STRANGER}`))).toContain('Autorisé');
  });
});

describe('the « Autorisés » tab and its places (D3)', () => {
  it('between Connectés and Bloqués, absent devices included, counted against the box', async () => {
    core({ approved: new Set([PC, TV, '02:00:00:00:00:99']) });
    const r = await render(<Devices />);
    await settle(() => !!byId(r, 'devices-filter-allowed'));
    await press(byId(r, 'devices-filter-allowed'));
    await settle(() => !!byId(r, 'devices-places'));
    // This phone counts too: 4 of 32.
    expect(texts(byId(r, 'devices-places-count'))).toEqual(['4 / 32 places']);
    expect(byId(r, 'device-02:00:00:00:00:99')).toBeDefined();
  });

  it('orange from 80 %, and the full list says what to do', async () => {
    core({ lock: lockStatus({ places: 5 }), approved: new Set([PC, TV, STRANGER]) });
    const r = await render(<Devices />);
    await settle(() => !!byId(r, 'devices-filter-allowed'));
    await press(byId(r, 'devices-filter-allowed'));
    await settle(() => !!byId(r, 'devices-places'));
    expect(texts(byId(r, 'devices-places-warn'))).toEqual(['4 / 5 places']);
    expect(byId(r, 'devices-places-full')).toBeUndefined();
    await act(async () => root?.unmount());
    core({ lock: lockStatus({ places: 3 }) });
    const r2 = await render(<Devices />);
    await settle(() => !!byId(r2, 'devices-filter-allowed'));
    await press(byId(r2, 'devices-filter-allowed'));
    await settle(() => !!byId(r2, 'devices-places-full'));
    expect(texts(byId(r2, 'devices-places-full')).join('')).toBe(
      'La box accepte 3 appareils au plus. Retirez un appareil que vous n’utilisez plus.',
    );
  });
});

describe('« Voici les appareils de la maison » (R5, R8)', () => {
  it('two groups, this phone ticked for good, « Autoriser ces N appareils », then the panel again', async () => {
    core({ approved: new Set([PC]) });
    mockParams = { then: 'reserve' };
    const r = await render(<HouseDevices />);
    await settle(() => !!byId(r, `house-device-${STRANGER}`));
    expect(text(r)).toContain('Nommés ou autorisés');
    expect(text(r)).toContain('Sans nom, vus récemment');
    expect(byId(r, `house-device-check-${SELF}`).props.accessibilityState).toMatchObject({ checked: true, disabled: true });
    // The unnamed one is left to tick by hand.
    expect(byId(r, `house-device-check-${STRANGER}`).props.accessibilityState).toMatchObject({ checked: false });
    expect(texts(byId(r, 'house-devices-submit'))).toContain('Autoriser ces 2 appareils');
    await press(byId(r, `house-device-${STRANGER}`));
    expect(texts(byId(r, 'house-devices-submit'))).toContain('Autoriser ces 3 appareils');
    await press(byId(r, 'house-devices-submit'));
    await settle(() => mockBack.mock.calls.length > 0);
    expect(box.approved).toEqual(new Set([PC, TV, STRANGER]));
    expect(useLockPanel.getState().open).toBe(true);
  });
});

describe('where it lives (#134): the Wi-Fi tab reserves, Appareils keeps the devices', () => {
  it('Appareils shows no card, no « Gérer », no « Voir les appareils de la maison »', async () => {
    core();
    useLockPrefs.setState({ loaded: true, byBox: {} });
    const r = await render(<Devices />);
    await settle(() => !!byId(r, `device-${PC}`) && !!byId(r, 'devices-filter-allowed'));
    expect(byId(r, 'lock-card')).toBeUndefined();
    expect(byId(r, 'lock-manage')).toBeUndefined();
    expect(byId(r, 'lock-review')).toBeUndefined();
    expect(text(r)).not.toContain('Ouvert avec mot de passe');
    // The devices' own states stay: the pill and the « Autorisés » tab.
    expect(texts(byId(r, `device-trust-${PC}`))).toContain('Autorisé');
  });

  it('the Wi-Fi tab holds the card, « Gérer » and « Voir les appareils de la maison »', async () => {
    core();
    useLockPrefs.setState({ loaded: true, byBox: {} });
    const r = await render(<WiFi />);
    await settle(() => !!byId(r, 'lock-card') && !!byId(r, 'lock-review'));
    expect(texts(byId(r, 'lock-state'))).toEqual(['Ouvert avec mot de passe']);
    expect(byId(r, 'lock-manage')).toBeDefined();
    // Above the networks' cards.
    expect(byId(r, 'wifi-ssid-DEV.WIFI.AP1')).toBeDefined();
    await press(byId(r, 'lock-review'));
    expect(mockPush).toHaveBeenCalledWith('/house-devices');
  });

  it('the « Autorisés » tab sends to the Wi-Fi tab to reserve', async () => {
    core();
    const r = await render(<Devices />);
    await settle(() => !!byId(r, 'devices-filter-allowed'));
    await press(byId(r, 'devices-filter-allowed'));
    await settle(() => !!byId(r, 'devices-allowed-wifi'));
    expect(texts(byId(r, 'devices-allowed-wifi'))).toContain('Gérer le Wi-Fi de la maison');
    await press(byId(r, 'devices-allowed-wifi'));
    expect(mockPush).toHaveBeenCalledWith('/wifi');
  });

  it('« Réseaux des visiteurs » opens from the Wi-Fi tab', async () => {
    core();
    const r = await render(<WiFi />);
    await settle(() => !!byId(r, 'settings-visitors'));
    expect(text(r)).toContain('Réseaux des visiteurs');
    await press(byId(r, 'settings-visitors'));
    expect(mockPush).toHaveBeenCalledWith('/wifi-visitors');
  });
});

describe('a box that cannot reserve its Wi-Fi (D11)', () => {
  it('hides the card, the « Autorisés » tab and the « Autorisé » state', async () => {
    core({ lock: 'unsupported' });
    const r = await render(<Devices />);
    await settle(() => !!byId(r, `device-${PC}`));
    expect(byId(r, 'lock-card')).toBeUndefined();
    expect(byId(r, 'devices-filter-allowed')).toBeUndefined();
    expect(text(r)).not.toContain('Autorisé');
    expect(text(r)).not.toMatch(/Réserv|Ouvert avec mot de passe/);
  });

  it('the Wi-Fi tab shows its networks, and no card nor « Réseaux des visiteurs »', async () => {
    core({ lock: 'unsupported' });
    const r = await render(<WiFi />);
    await settle(() => !!byId(r, 'wifi-ssid-DEV.WIFI.AP1') && calls('GET', '/api/wifi/lock').length > 0);
    await settle();
    expect(byId(r, 'lock-card')).toBeUndefined();
    expect(byId(r, 'settings-visitors')).toBeUndefined();
    expect(text(r)).not.toMatch(/Réserv|Ouvert avec mot de passe|visiteurs/);
  });
});

describe('auto-reject and the visitors’ networks', () => {
  it('auto-reject is paused while the Wi-Fi is reserved, and the middle way while open (D5, R2)', async () => {
    core({ lock: lockStatus({ state: 'closed', intended: true }) });
    let r = await render(<AutoRejectScreen />);
    await settle(() => !!byId(r, 'auto-reject-paused'));
    expect(texts(byId(r, 'auto-reject-paused')).join('')).toBe(
      'En pause : votre Wi-Fi est réservé à la maison, seuls vos appareils entrent déjà.',
    );
    await act(async () => root?.unmount());
    core();
    r = await render(<AutoRejectScreen />);
    await settle(() => !!byId(r, 'auto-reject-middle'));
    expect(texts(byId(r, 'auto-reject-middle')).join('')).toMatch(/^Le moyen terme : le Wi-Fi reste ouvert avec le mot de passe/);
  });

  it('nothing about it on a box that cannot reserve its Wi-Fi', async () => {
    core({ lock: 'unsupported' });
    const r = await render(<AutoRejectScreen />);
    await settle();
    expect(byId(r, 'auto-reject-paused')).toBeUndefined();
    expect(byId(r, 'auto-reject-middle')).toBeUndefined();
  });

  it('« Réseaux des visiteurs » changes the answer to « Ce réseau est-il pour vos visiteurs ? »', async () => {
    core({
      lock: lockStatus({
        aps: [
          { id: 'DEV.WIFI.AP1', band: '2.4GHz', name: 'MonWiFi-2G', mode: 'Disabled', visitors: false },
          { id: 'DEV.WIFI.AP5', band: '5GHz', name: 'MonWiFi-5G', mode: 'Disabled', visitors: false },
        ],
      }),
    });
    const r = await render(<WiFiVisitors />);
    await settle(() => !!byId(r, 'wifi-visitors-0'));
    // This phone's network is not asked about.
    expect(text(r)).not.toContain('MonWiFi-2G ·');
    await press(byId(r, 'wifi-visitors-0-yes'));
    expect(useLockPrefs.getState().byBox['http://192.168.1.1'].visitors).toEqual({ 'MonWiFi-5G': 'yes' });
  });
});
