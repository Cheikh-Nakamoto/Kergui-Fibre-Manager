/* eslint-disable import/first -- the mocks must come before the modules they replace */
// "Être un relais" (issue #45): this phone scans the owner's QR (or the code
// typed under it when the camera is refused), shows its own six digits,
// waits for the owner to confirm them, asks for the notifications and the
// location, and starts the relay service; then it shows what the relay is
// doing. In French, without jargon. In the demo the owner is simulated and
// no service starts.
import { act } from 'react';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn(), canGoBack: () => true },
  Stack: { Screen: () => null },
  Redirect: () => null,
  useFocusEffect: (cb: () => void) => jest.requireActual('react').useEffect(cb, [cb]),
}));
jest.mock('expo-constants', () => ({ __esModule: true, default: { deviceName: 'Téléphone du salon' } }));
const mockRequestCamera = jest.fn();
jest.mock('expo-camera', () => ({
  // A host element, so the test can find it and fire what it scans.
  CameraView: (props: object) => jest.requireActual('react').createElement('View', props),
  useCameraPermissions: () => [null, mockRequestCamera],
}));
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('../core/client', () => ({ api: {}, core: {} }));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));
const mockRelay = {
  supported: jest.fn(),
  enroll: jest.fn(),
  enrolled: jest.fn(),
  trusted: jest.fn(),
  status: jest.fn(),
  enabled: jest.fn(),
  askPermissions: jest.fn(),
  locationGranted: jest.fn(),
  start: jest.fn(),
  stop: jest.fn(),
};
jest.mock('../core/relayPhone', () => {
  const actual = jest.requireActual('../core/relayPhone');
  return {
    parseInvite: actual.parseInvite,
    readStatus: actual.readStatus,
    get relayPhone() {
      return mockRelay;
    },
  };
});
const mockOwner = { invite: jest.fn(), demoRelay: jest.fn() };
jest.mock('../core/owner', () => ({
  get owner() {
    return mockOwner;
  },
}));

import { router } from 'expo-router';

import BecomeRelayScreen, { STATUS_POLL_MS, TRUST_POLL_MS } from '../app/become-relay';
import Welcome from '../app/index';
import { RelayPhoneSetting } from '../ui/RelayPhoneSetting';
import { parseInvite, readStatus } from '../core/relayPhone';
import { useSession } from '../session/store';

const INVITE = 'kgp1.KNXW4ZI.0123456789abcdef0123456789abcdef';

function texts(node: TestInstance): string[] {
  const out: string[] = [];
  for (const c of node.children) {
    if (typeof c === 'string') out.push(c);
    else out.push(...texts(c));
  }
  return out;
}

let root: Root | null = null;

async function settle() {
  for (let i = 0; i < 10; i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 5))));
}

const all = (id: string) => root!.container.queryAll((n) => n.props.testID === id);
const text = () => texts(root!.container).join('\n');
const handler = (id: string, prop: string) => {
  const node = all(id).find((n) => typeof n.props[prop] === 'function');
  if (!node) throw new Error(`no ${id} with ${prop}`);
  return node.props[prop] as (...a: unknown[]) => unknown;
};
const press = async (id: string) => {
  const node = all(id).find((n) => typeof n.props.onClick === 'function' || typeof n.props.onPress === 'function');
  if (!node) throw new Error(`no ${id}`);
  const ev = { nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined };
  await act(async () => void (node.props.onClick ?? node.props.onPress)(ev));
  await settle();
};
const type = async (id: string, value: string) => {
  await act(async () => void handler(id, 'onChangeText')(value));
  await settle();
};

async function render(screen: React.ReactElement = <BecomeRelayScreen />) {
  root = createRoot();
  await act(async () => {
    root!.render(<PaperProvider settings={{ icon: () => null }}>{screen}</PaperProvider>);
  });
  await settle();
}

const NO_JARGON = /Ed25519|X25519|empreinte|clé publique|bundle|fingerprint|SSID|BSSID|lease|hub/i;

beforeEach(() => {
  for (const m of [...Object.values(mockRelay), ...Object.values(mockOwner), mockRequestCamera]) m.mockReset();
  mockRelay.supported.mockReturnValue(true);
  mockRelay.enrolled.mockResolvedValue(false);
  mockRelay.trusted.mockResolvedValue(false);
  mockRelay.enabled.mockReturnValue(false);
  mockRelay.enroll.mockResolvedValue({ agentId: 'A9', tenantId: 'T1', code: '042917' });
  mockRelay.askPermissions.mockResolvedValue({ location: true, notifications: true });
  mockRelay.locationGranted.mockResolvedValue(true);
  mockRelay.start.mockResolvedValue(undefined);
  mockRelay.stop.mockResolvedValue(undefined);
  mockRelay.status.mockResolvedValue({ running: true, state: 'active', check: 'signature', autoReject: false });
  useSession.setState({ session: null });
  (router.push as jest.Mock).mockReset();
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
  jest.useRealTimers();
});

test('the QR payload, typed with spaces or scanned, is recognised; anything else is not', () => {
  expect(parseInvite(` ${INVITE.slice(0, 10)} ${INVITE.slice(10)} `)).toBe(INVITE);
  expect(parseInvite('kgp1.knxw4zi.0123456789abcdef')).toBe('kgp1.knxw4zi.0123456789abcdef');
  expect(parseInvite('https://example.org')).toBeNull();
  expect(parseInvite('042917')).toBeNull();
});

test('the status the core answers is read field by field', () => {
  expect(readStatus('{"running":true,"state":"wrong-network","check":"","autoReject":false,"token":"x"}')).toEqual({
    running: true,
    state: 'wrong-network',
    check: '',
    autoReject: false,
  });
  expect(readStatus('{"state":"bizarre"}').state).toBe('stopped');
});

test('a new phone: what a relay is, the scan button and the code to type', async () => {
  await render();
  expect(all('relay-scan').length).toBeGreaterThan(0);
  expect(all('relay-manual-code').length).toBeGreaterThan(0);
  const s = text();
  expect(s).toContain('Faire de ce téléphone un relais');
  expect(s).toContain('Scanner le QR code');
  expect(s).toMatch(/Relais à la maison/);
  expect(s).not.toMatch(NO_JARGON);
});

test('scanning: the camera opens, the QR is redeemed and this phone shows its six digits', async () => {
  mockRequestCamera.mockResolvedValue({ granted: true });
  await render();
  await press('relay-scan');
  expect(all('relay-camera').length).toBeGreaterThan(0);
  await act(async () => void handler('relay-camera', 'onBarcodeScanned')({ data: INVITE }));
  await settle();
  expect(mockRelay.enroll).toHaveBeenCalledWith('https://hub.test', INVITE, 'Téléphone du salon');
  expect(texts(all('relay-my-code')[0]).join('')).toBe('042 917');
  expect(all('relay-waiting').length).toBeGreaterThan(0);
  expect(text()).toContain('En attente de la confirmation du propriétaire…');
  expect(text()).not.toMatch(NO_JARGON);
});

test('camera refused: it says so, and the code typed under the QR pairs all the same', async () => {
  mockRequestCamera.mockResolvedValue({ granted: false });
  await render();
  await press('relay-scan');
  expect(all('relay-camera')).toHaveLength(0);
  expect(all('relay-camera-denied').length).toBeGreaterThan(0);
  await type('relay-manual-code', 'pas un code');
  await press('relay-manual-submit');
  expect(mockRelay.enroll).not.toHaveBeenCalled();
  expect(text()).toContain('Ce code n’est pas celui d’un QR code Kergui');
  await type('relay-manual-code', INVITE);
  await press('relay-manual-submit');
  expect(mockRelay.enroll).toHaveBeenCalledWith('https://hub.test', INVITE, 'Téléphone du salon');
  expect(all('relay-my-code').length).toBeGreaterThan(0);
});

test('the owner confirms: permissions, then the relay service starts and its state shows', async () => {
  jest.useFakeTimers({ advanceTimers: true });
  await render();
  await type('relay-manual-code', INVITE);
  await press('relay-manual-submit');
  expect(all('relay-waiting').length).toBeGreaterThan(0);

  mockRelay.trusted.mockResolvedValue(true);
  await act(async () => void jest.advanceTimersByTime(TRUST_POLL_MS));
  await settle();
  expect(all('relay-confirmed').length).toBeGreaterThan(0);
  expect(text()).toContain('Le mot de passe de la box arrive chiffré');
  expect(mockRelay.start).not.toHaveBeenCalled();

  await press('relay-start-service');
  expect(mockRelay.askPermissions).toHaveBeenCalled();
  expect(mockRelay.start).toHaveBeenCalled();
  expect(all('relay-active').length).toBeGreaterThan(0);
  expect(all('relay-state-active').length).toBeGreaterThan(0);
  expect(text()).toContain('En service : il transmet vos actions à la box.');
  expect(all('relay-location-off')).toHaveLength(0);

  mockRelay.status.mockResolvedValue({ running: true, state: 'wrong-network', check: '', autoReject: false });
  await act(async () => void jest.advanceTimersByTime(STATUS_POLL_MS));
  await settle();
  expect(text()).toContain('ce téléphone n’est pas sur le Wi-Fi de la maison');

  await press('relay-stop');
  expect(mockRelay.stop).toHaveBeenCalled();
  expect(all('relay-confirmed').length).toBeGreaterThan(0);
});

test('location refused: the relay still runs and the screen says only the box is checked', async () => {
  mockRelay.enrolled.mockResolvedValue(true);
  mockRelay.trusted.mockResolvedValue(true);
  mockRelay.askPermissions.mockResolvedValue({ location: false, notifications: true });
  mockRelay.locationGranted.mockResolvedValue(false);
  mockRelay.status.mockResolvedValue({ running: true, state: 'active', check: 'box-identity', autoReject: false });
  await render();
  await press('relay-start-service');
  expect(mockRelay.start).toHaveBeenCalled();
  expect(all('relay-location-off').length).toBeGreaterThan(0);
  expect(text()).toMatch(/vérifie seulement qu’il parle bien à votre box/);
});

test('a phone already relaying opens on its state', async () => {
  mockRelay.enrolled.mockResolvedValue(true);
  mockRelay.trusted.mockResolvedValue(true);
  mockRelay.enabled.mockReturnValue(true);
  mockRelay.status.mockResolvedValue({ running: true, state: 'standby', check: 'signature', autoReject: false });
  await render();
  expect(all('relay-active').length).toBeGreaterThan(0);
  expect(text()).toContain('un autre relais s’occupe de la box');
});

test('paired but not confirmed yet: it keeps waiting, and can start over', async () => {
  mockRelay.enrolled.mockResolvedValue(true);
  await render();
  expect(all('relay-waiting').length).toBeGreaterThan(0);
  expect(all('relay-my-code')).toHaveLength(0);
  await press('relay-restart');
  expect(all('relay-scan').length).toBeGreaterThan(0);
});

test('the demo simulates the owner and starts nothing', async () => {
  useSession.setState({ session: { mode: 'demo' } });
  mockOwner.invite.mockResolvedValue(INVITE);
  mockOwner.demoRelay.mockResolvedValue({ agentId: 'D1', name: 'x', kind: 'dedicated', ed25519: 'e', x25519: 'x', code: '123456' });
  await render();
  expect(all('relay-phone-demo').length).toBeGreaterThan(0);
  expect(all('relay-manual-code').length).toBeGreaterThan(0);
  await press('relay-demo-qr');
  expect(texts(all('relay-my-code')[0]).join('')).toBe('123 456');
  await press('relay-demo-confirm');
  await press('relay-start-service');
  expect(all('relay-active').length).toBeGreaterThan(0);
  expect(mockRelay.enroll).not.toHaveBeenCalled();
  expect(mockRelay.start).not.toHaveBeenCalled();
  expect(mockRelay.askPermissions).not.toHaveBeenCalled();
});

test('an iPhone is told it cannot relay', async () => {
  mockRelay.supported.mockReturnValue(false);
  await render();
  expect(all('relay-phone-unsupported').length).toBeGreaterThan(0);
});

test('the welcome screen offers « Être un relais »', async () => {
  useSession.setState({ session: null, bootError: null });
  await render(<Welcome />);
  expect(text()).toContain('Être un relais');
  await press('welcome-relay');
  expect(router.push).toHaveBeenCalledWith('/become-relay');
});

test('Réglages: the switch opens the flow until this phone is confirmed, then starts the service', async () => {
  await render(<RelayPhoneSetting demo={false} />);
  expect(text()).toContain('Ce téléphone sert de relais');
  await act(async () => void handler('settings-relay-phone-switch', 'onChange')({ nativeEvent: { value: true } }));
  await settle();
  expect(router.push).toHaveBeenCalledWith('/become-relay');
  expect(mockRelay.start).not.toHaveBeenCalled();

  mockRelay.enrolled.mockResolvedValue(true);
  mockRelay.trusted.mockResolvedValue(true);
  await act(async () => void handler('settings-relay-phone-switch', 'onChange')({ nativeEvent: { value: true } }));
  await settle();
  expect(mockRelay.start).toHaveBeenCalled();
  expect(text()).toContain('la notification « Relais Kergui » le montre');
  await act(async () => void handler('settings-relay-phone-switch', 'onChange')({ nativeEvent: { value: false } }));
  await settle();
  expect(mockRelay.stop).toHaveBeenCalled();
});

test('Réglages in the demo: the switch only opens the simulated flow', async () => {
  await render(<RelayPhoneSetting demo />);
  await act(async () => void handler('settings-relay-phone-switch', 'onChange')({ nativeEvent: { value: true } }));
  await settle();
  expect(router.push).toHaveBeenCalledWith('/become-relay');
  expect(mockRelay.start).not.toHaveBeenCalled();
});
