/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The first connection without a certificate question (#122, audit H6).
// On the box's default address (https://192.168.1.1), the very first time,
// Kergui keeps the box's certificate without a dialog and writes its
// fingerprint to the journal. A dialog stays only where a household can
// answer it: « C'est bien votre box ? », « Oui, c'est ma box » / « Non »,
// the fingerprint under « Détails »; a different certificate than the one
// Kergui remembers always asks, never goes on in silence. « Non » says why
// nothing happened. « Adresse de la box » and « Identifiant » wait under
// « Paramètres avancés », and the password has an eye.
import { act } from 'react';
import { Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn(), canGoBack: () => true },
  Stack: { Screen: () => null },
}));
// What the phone remembers, in memory.
const mockStore = new Map<string, string>();
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async (k: string) => mockStore.get(k) ?? null,
  setItemAsync: async (k: string, v: string) => void mockStore.set(k, v),
  deleteItemAsync: async (k: string) => void mockStore.delete(k),
}));
const mockApi = { discover: jest.fn(), login: jest.fn(), firstPassword: jest.fn() };
const mockCore = { note: jest.fn(), fingerprint: jest.fn(), configure: jest.fn() };
jest.mock('../core/client', () => ({
  ApiError: jest.requireActual('../core/client').ApiError,
  get api() {
    return mockApi;
  },
  get core() {
    return mockCore;
  },
}));
const mockConnected = jest.fn();
jest.mock('../session/store', () => ({
  useSession: (select: (s: object) => unknown) => select({ connected: mockConnected }),
}));
jest.mock('../session/filterSetup', () => ({ setupMACFilter: jest.fn(() => Promise.resolve()) }));
jest.mock('../licensing/relays', () => ({ relays: { afterBoxLogin: jest.fn(() => Promise.resolve()) } }));
jest.mock('../session/boxOwner', () => ({ boxRole: jest.fn(async () => ({ owner: true })) }));

import { router } from 'expo-router';

import Connect from '../app/connect';
import { formatFingerprint } from '../lib/format';
import { knownBoxPin, rememberBoxPin } from '../session/boxPins';
import { ConfirmHost } from '../ui/ConfirmHost';

const PIN = 'ab'.repeat(32);
const OTHER = 'cd'.repeat(32);

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
const pressable = (id: string) =>
  all(id).find((n) => typeof n.props.onClick === 'function' || typeof n.props.onPress === 'function');
const press = async (id: string) => {
  const node = pressable(id);
  if (!node) throw new Error(`no ${id}`);
  const ev = { nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined };
  await act(async () => void (node.props.onClick ?? node.props.onPress)(ev));
  await settle();
};
const input = (id: string) => all(id).find((n) => typeof n.props.onChangeText === 'function');
const type = async (id: string, value: string) => {
  const node = input(id);
  if (!node) throw new Error(`no input ${id}`);
  await act(async () => void node.props.onChangeText(value));
  await settle();
};
const dialogShown = () => all('confirm-title').length > 0;
const noted = () => mockCore.note.mock.calls.map(([, line]) => String(line)).join('\n');

async function render() {
  root = createRoot();
  await act(async () => {
    root!.render(
      <PaperProvider settings={{ icon: ({ name }: { name: string }) => <Text>{`icon:${name}`}</Text> }}>
        <Connect />
        <ConfirmHost />
      </PaperProvider>,
    );
  });
  await settle();
}

async function connect() {
  await type('connect-password', 'label-secret');
  await press('connect-submit');
}

beforeEach(() => {
  mockStore.clear();
  for (const m of [...Object.values(mockApi), ...Object.values(mockCore), mockConnected]) m.mockReset();
  (router.replace as jest.Mock).mockClear();
  mockCore.fingerprint.mockResolvedValue(PIN);
  mockCore.configure.mockResolvedValue(undefined);
  mockApi.discover.mockResolvedValue({ Reachable: true, Vendor: 'ZTE', Model: 'F6600P', AdapterID: 'zte_f6600p' });
  mockApi.login.mockResolvedValue({ status: 'ok' });
  mockConnected.mockResolvedValue(undefined);
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
});

test('the first connection to the default address asks nothing, and the journal keeps the fingerprint', async () => {
  await render();
  await connect();
  expect(dialogShown()).toBe(false);
  expect(text()).not.toMatch(/certificat|empreinte/i);
  expect(mockCore.configure).toHaveBeenCalledWith('https://192.168.1.1', PIN);
  expect(mockConnected).toHaveBeenCalledWith({ mode: 'router', routerUrl: 'https://192.168.1.1', pin: PIN });
  expect(router.replace).toHaveBeenCalledWith('/devices');
  // The fingerprint kept, said in the journal to check it later.
  expect(noted()).toContain(formatFingerprint(PIN));
  expect(await knownBoxPin('https://192.168.1.1')).toBe(PIN);
});

test('the same box, the same certificate: no question the next time either', async () => {
  await rememberBoxPin('https://192.168.1.1', PIN);
  await render();
  await connect();
  expect(dialogShown()).toBe(false);
  expect(mockConnected).toHaveBeenCalledWith({ mode: 'router', routerUrl: 'https://192.168.1.1', pin: PIN });
});

test('a certificate that changed asks « C’est bien votre box ? », with the fingerprint under « Détails »', async () => {
  await rememberBoxPin('https://192.168.1.1', PIN);
  mockCore.fingerprint.mockResolvedValue(OTHER);
  await render();
  await connect();
  expect(dialogShown()).toBe(true);
  expect(texts(all('confirm-title')[0]).join('')).toBe('C’est bien votre box ?');
  expect(texts(all('confirm-ok')[0]).join('')).toContain('Oui, c’est ma box');
  expect(texts(all('confirm-cancel')[0]).join('')).toBe('Non');
  // Nothing goes on while the household has not answered.
  expect(mockCore.configure).not.toHaveBeenCalled();
  expect(mockConnected).not.toHaveBeenCalled();
  // The hex only under « Détails ».
  expect(text()).not.toContain(formatFingerprint(OTHER));
  expect(text()).toContain('Détails');
  await press('confirm-details');
  expect(text()).toContain(formatFingerprint(OTHER));
});

test('« Oui, c’est ma box » on a changed certificate keeps the new one', async () => {
  await rememberBoxPin('https://192.168.1.1', PIN);
  mockCore.fingerprint.mockResolvedValue(OTHER);
  await render();
  await connect();
  await press('confirm-ok');
  expect(mockCore.configure).toHaveBeenCalledWith('https://192.168.1.1', OTHER);
  expect(mockConnected).toHaveBeenCalledWith({ mode: 'router', routerUrl: 'https://192.168.1.1', pin: OTHER });
  expect(await knownBoxPin('https://192.168.1.1')).toBe(OTHER);
});

test('« Non » stops, says why, and keeps the certificate Kergui knew', async () => {
  await rememberBoxPin('https://192.168.1.1', PIN);
  mockCore.fingerprint.mockResolvedValue(OTHER);
  await render();
  await connect();
  await press('confirm-cancel');
  expect(dialogShown()).toBe(false);
  expect(text()).toContain('Connexion annulée. Vérifiez que vous êtes sur le Wi-Fi de votre box.');
  // Not tried on http behind the household's back either.
  expect(mockCore.configure).not.toHaveBeenCalled();
  expect(mockConnected).not.toHaveBeenCalled();
  expect(await knownBoxPin('https://192.168.1.1')).toBe(PIN);
  expect(noted()).toMatch(/refusé/);
});

test('« Adresse de la box » and « Identifiant » wait under « Paramètres avancés »', async () => {
  await render();
  expect(text()).toContain('Paramètres avancés');
  expect(input('connect-address')).toBeUndefined();
  expect(input('connect-username')).toBeUndefined();
  await press('connect-advanced');
  expect(input('connect-address')?.props.value).toBe('192.168.1.1');
  expect(input('connect-username')?.props.value).toBe('user');
});

test('an address typed by hand asks on the first connection', async () => {
  await render();
  await press('connect-advanced');
  await type('connect-address', '10.0.0.138');
  await connect();
  expect(dialogShown()).toBe(true);
  expect(texts(all('confirm-message')[0]).join('')).toContain('Kergui va la reconnaître la prochaine fois.');
  expect(mockCore.configure).not.toHaveBeenCalled();
  await press('confirm-ok');
  expect(mockCore.configure).toHaveBeenCalledWith('https://10.0.0.138', PIN);
  expect(await knownBoxPin('https://10.0.0.138')).toBe(PIN);
});

test('a refused « user » opens « Paramètres avancés » to try « admin »', async () => {
  const { ApiError } = jest.requireActual('../core/client');
  mockApi.login.mockRejectedValue(new ApiError(401, 'unauthorized'));
  await render();
  await connect();
  expect(text()).toContain('essayez l’identifiant « admin »');
  expect(input('connect-username')?.props.value).toBe('user');
});

test('the password has an eye: shown, then hidden again', async () => {
  await render();
  await type('connect-password', 'label-secret');
  expect(input('connect-password')?.props.secureTextEntry).toBe(true);
  await press('connect-password-eye');
  expect(input('connect-password')?.props.secureTextEntry).toBe(false);
  await press('connect-password-eye');
  expect(input('connect-password')?.props.secureTextEntry).toBe(true);
});
