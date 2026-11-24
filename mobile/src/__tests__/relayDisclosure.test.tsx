/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Play's prominent disclosure for the phone relay (#53): before this phone
// first becomes a relay, a screen says what it sends to the Kergui server and
// why, that a permanent notification shows while it runs, and how to stop
// it; « Accepter » goes on, « Annuler » leaves the relay off. The answer is
// remembered (not asked again) and the text stays one tap away. The demo
// shows it too, without remembering it for a real box.
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
jest.mock('expo-camera', () => ({
  CameraView: (props: object) => jest.requireActual('react').createElement('View', props),
  useCameraPermissions: () => [null, jest.fn()],
}));
const mockStore = new Map<string, string>();
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async (k: string) => mockStore.get(k) ?? null,
  setItemAsync: async (k: string, v: string) => void mockStore.set(k, v),
  deleteItemAsync: async (k: string) => void mockStore.delete(k),
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
jest.mock('../core/owner', () => ({ owner: { invite: jest.fn(), demoRelay: jest.fn() } }));

import { router } from 'expo-router';

import BecomeRelayScreen from '../app/become-relay';
import { RELAY_CONSENT_KEY, relayConsent } from '../features/relayConsent';
import { RelayPhoneSetting } from '../ui/RelayPhoneSetting';
import { useSession } from '../session/store';

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
const press = async (id: string) => {
  const node = all(id).find((n) => typeof n.props.onClick === 'function' || typeof n.props.onPress === 'function');
  if (!node) throw new Error(`no ${id}`);
  const ev = { nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined };
  await act(async () => void (node.props.onClick ?? node.props.onPress)(ev));
  await settle();
};
const flip = async (value: boolean) => {
  const node = all('settings-relay-phone-switch').find((n) => typeof n.props.onChange === 'function');
  if (!node) throw new Error('no switch');
  await act(async () => void node.props.onChange({ nativeEvent: { value } }));
  await settle();
};

async function render(screen: React.ReactElement = <BecomeRelayScreen />) {
  root = createRoot();
  await act(async () => {
    root!.render(<PaperProvider settings={{ icon: () => null }}>{screen}</PaperProvider>);
  });
  await settle();
}
const unmount = async () => {
  await act(async () => root?.unmount());
  root = null;
};

const NO_JARGON = /Ed25519|X25519|empreinte|clé publique|bundle|fingerprint|SSID|BSSID|lease|hub/i;

beforeEach(() => {
  mockStore.clear();
  relayConsent.forgetDemo();
  for (const m of Object.values(mockRelay)) m.mockReset();
  mockRelay.supported.mockReturnValue(true);
  mockRelay.enrolled.mockResolvedValue(false);
  mockRelay.trusted.mockResolvedValue(false);
  mockRelay.enabled.mockReturnValue(false);
  mockRelay.askPermissions.mockResolvedValue({ location: true, notifications: true });
  mockRelay.start.mockResolvedValue(undefined);
  mockRelay.stop.mockResolvedValue(undefined);
  useSession.setState({ session: null });
  for (const f of [router.push, router.back, router.replace] as jest.Mock[]) f.mockReset();
});

afterEach(unmount);

test('a new phone sees the disclosure first: what is sent, why, the notification, how to stop', async () => {
  await render();
  expect(all('relay-disclosure').length).toBeGreaterThan(0);
  expect(all('relay-scan')).toHaveLength(0);
  const s = text();
  expect(s).toContain('Avant de faire de ce téléphone un relais');
  // What leaves the phone, and where to.
  expect(s).toMatch(/serveur Kergui/);
  expect(s).toMatch(/liste des appareils de la box/);
  expect(s).toMatch(/adresse MAC/);
  expect(s).toMatch(/nom du Wi-Fi/);
  // Why.
  expect(s).toMatch(/quand vous êtes ailleurs/);
  // What never leaves.
  expect(s).toMatch(/mot de passe de la box ne quitte jamais/);
  // The foreground service and its notification.
  expect(s).toMatch(/notification permanente « Relais Kergui »/);
  expect(s).toMatch(/même quand l’application est fermée/);
  // How to stop.
  expect(s).toMatch(/Arrêter le relais/);
  expect(s).toMatch(/Plus de fonctionnalités/);
  expect(s).toContain('Accepter');
  expect(s).toContain('Annuler');
  expect(s).not.toMatch(NO_JARGON);
  expect(mockRelay.enroll).not.toHaveBeenCalled();
});

test('« Annuler »: back where the person came from, nothing remembered, nothing started', async () => {
  await render();
  await press('relay-disclosure-cancel');
  expect(router.back).toHaveBeenCalled();
  expect(mockStore.has(RELAY_CONSENT_KEY)).toBe(false);
  expect(mockRelay.start).not.toHaveBeenCalled();
  expect(mockRelay.enroll).not.toHaveBeenCalled();
  expect(await relayConsent.accepted(false)).toBe(false);
});

test('« Accepter »: the flow goes on, and the next visit does not ask again', async () => {
  await render();
  await press('relay-disclosure-accept');
  expect(all('relay-disclosure')).toHaveLength(0);
  expect(all('relay-scan').length).toBeGreaterThan(0);
  expect(await relayConsent.accepted(false)).toBe(true);

  await unmount();
  await render();
  expect(all('relay-disclosure')).toHaveLength(0);
  expect(all('relay-scan').length).toBeGreaterThan(0);
});

test('once accepted, the text stays one tap away and closes back to the flow', async () => {
  await relayConsent.accept(false);
  await render();
  expect(all('relay-disclosure')).toHaveLength(0);
  await press('relay-disclosure-open');
  expect(all('relay-disclosure').length).toBeGreaterThan(0);
  expect(all('relay-disclosure-accept')).toHaveLength(0);
  expect(text()).toMatch(/serveur Kergui/);
  await press('relay-disclosure-close');
  expect(all('relay-disclosure')).toHaveLength(0);
  expect(all('relay-scan').length).toBeGreaterThan(0);
});

test('a phone paired before the disclosure existed sees it before « Démarrer le relais »', async () => {
  mockRelay.enrolled.mockResolvedValue(true);
  mockRelay.trusted.mockResolvedValue(true);
  await render();
  expect(all('relay-disclosure').length).toBeGreaterThan(0);
  expect(all('relay-start-service')).toHaveLength(0);
  await press('relay-disclosure-accept');
  expect(all('relay-start-service').length).toBeGreaterThan(0);
});

test('Réglages: the switch on a confirmed phone does not start the service before the disclosure', async () => {
  mockRelay.enrolled.mockResolvedValue(true);
  mockRelay.trusted.mockResolvedValue(true);
  await render(<RelayPhoneSetting demo={false} />);
  await flip(true);
  expect(router.push).toHaveBeenCalledWith('/become-relay');
  expect(mockRelay.start).not.toHaveBeenCalled();
  expect(mockRelay.askPermissions).not.toHaveBeenCalled();
  expect(text()).toContain('Ce téléphone reste à la maison');

  await relayConsent.accept(false);
  await flip(true);
  expect(mockRelay.start).toHaveBeenCalled();
});

test('the demo shows the disclosure too, and its « Accepter » does not count for a real box', async () => {
  useSession.setState({ session: { mode: 'demo' } } as never);
  await render();
  expect(all('relay-phone-demo').length).toBeGreaterThan(0);
  expect(all('relay-disclosure').length).toBeGreaterThan(0);
  await press('relay-disclosure-accept');
  expect(all('relay-demo-qr').length).toBeGreaterThan(0);
  expect(await relayConsent.accepted(true)).toBe(true);
  expect(await relayConsent.accepted(false)).toBe(false);
  expect(mockStore.has(RELAY_CONSENT_KEY)).toBe(false);
});
