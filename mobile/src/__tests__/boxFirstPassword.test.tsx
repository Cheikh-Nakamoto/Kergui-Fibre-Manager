/* eslint-disable import/first -- the mocks must come before the modules they replace */
// A new box (#91, #93): the label's login answers 409 password_change_required.
// The owner gets « Nouveau mot de passe de la box »: two fields that must
// match, the box's rule, an advisory gauge, the box's refusal in French, and
// « Annuler » back to the connection screen with the label's password
// forgotten. A member is sent to the owner, on WhatsApp when the hub gives
// the number, and nothing is tried. Réglages shows the password the core
// keeps only after the phone's own unlock.
import { act } from 'react';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn(), canGoBack: () => true },
  Stack: { Screen: () => null },
  useFocusEffect: (cb: () => void) => jest.requireActual('react').useEffect(cb, [cb]),
}));
jest.mock('expo-linking', () => ({ openURL: jest.fn(() => Promise.resolve()) }));
jest.mock('expo-clipboard', () => ({ setStringAsync: jest.fn(() => Promise.resolve(true)) }));
jest.mock('expo-local-authentication', () => ({ authenticateAsync: jest.fn() }));
const mockApi = { discover: jest.fn(), login: jest.fn(), firstPassword: jest.fn() };
const mockCore = { note: jest.fn(), fingerprint: jest.fn(), configure: jest.fn(), boxPassword: jest.fn() };
// Getters: the imports below run before these consts are set.
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
const mockRole = jest.fn();
jest.mock('../session/boxOwner', () => ({ boxRole: () => mockRole() }));

import * as Clipboard from 'expo-clipboard';
import * as Linking from 'expo-linking';
import * as LocalAuthentication from 'expo-local-authentication';
import { router } from 'expo-router';

import Connect from '../app/connect';
import { ApiError } from '../core/client';
import { BoxPasswordSetting } from '../ui/BoxPasswordSetting';

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
const disabled = (id: string) => {
  const node = all(id).find((n) => n.props.accessibilityState !== undefined);
  return node?.props.accessibilityState?.disabled === true;
};
const press = async (id: string) => {
  const node = pressable(id);
  if (!node) throw new Error(`no ${id}`);
  const ev = { nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined };
  await act(async () => void (node.props.onClick ?? node.props.onPress)(ev));
  await settle();
};
const type = async (id: string, value: string) => {
  const node = all(id).find((n) => typeof n.props.onChangeText === 'function');
  if (!node) throw new Error(`no input ${id}`);
  await act(async () => void node.props.onChangeText(value));
  await settle();
};
const valueOf = (id: string) => all(id).find((n) => typeof n.props.onChangeText === 'function')?.props.value;

async function render(screen: React.ReactElement) {
  root = createRoot();
  await act(async () => {
    root!.render(<PaperProvider settings={{ icon: () => null }}>{screen}</PaperProvider>);
  });
  await settle();
}

const NEW_BOX = new ApiError(409, 'password change required', 'password_change_required');

/** The connection screen up to the box's demand for a new password. */
async function reachNewBox() {
  await render(<Connect />);
  expect(valueOf('connect-username')).toBe('user');
  await type('connect-password', 'label-secret');
  await press('connect-submit');
}

beforeEach(() => {
  for (const m of [...Object.values(mockApi), ...Object.values(mockCore), mockConnected, mockRole]) m.mockReset();
  for (const f of [router.replace, Linking.openURL, Clipboard.setStringAsync, LocalAuthentication.authenticateAsync] as jest.Mock[]) {
    f.mockClear();
  }
  // No certificate on 443: the screen goes on with http, no trust prompt.
  mockCore.fingerprint.mockRejectedValue(new Error('connection refused'));
  mockCore.configure.mockResolvedValue(undefined);
  mockApi.discover.mockResolvedValue({ Reachable: true, Vendor: 'v', Model: 'm', AdapterID: 'a' });
  mockApi.login.mockRejectedValue(NEW_BOX);
  mockRole.mockResolvedValue({ owner: true });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
});

test('the label login on a new box opens « Nouveau mot de passe de la box » for the owner', async () => {
  await reachNewBox();
  expect(mockApi.login).toHaveBeenCalledWith('user', 'label-secret');
  expect(all('box-first-password').length).toBeGreaterThan(0);
  expect(text()).toContain('Nouveau mot de passe de la box');
  expect(text()).toContain('Au moins 8 caractères, avec une minuscule, une majuscule et un chiffre.');
  expect(mockConnected).not.toHaveBeenCalled();
});

test('the two fields must match and meet the box rule before anything is sent', async () => {
  await reachNewBox();
  expect(disabled('box-first-submit')).toBe(true);
  await type('box-first-new', 'kergui');
  expect(text()).toContain('Il manque : 8 caractères au moins, une majuscule, un chiffre.');
  await type('box-first-new', 'Kergui2026');
  await type('box-first-confirm', 'Kergui2025');
  expect(text()).toContain('Les deux mots de passe ne sont pas identiques.');
  expect(disabled('box-first-submit')).toBe(true);
  await type('box-first-confirm', 'Kergui2026');
  expect(text()).not.toContain('pas identiques');
  expect(disabled('box-first-submit')).toBe(false);

  mockApi.firstPassword.mockResolvedValue({ status: 'ok', stored: true, adapter: 'zte_f6600p', router_id: 'r1' });
  mockConnected.mockResolvedValue(undefined);
  await press('box-first-submit');
  // The label's login goes with the new password, once; the core keeps the new one.
  expect(mockApi.firstPassword).toHaveBeenCalledWith('user', 'label-secret', 'Kergui2026');
  expect(mockConnected).toHaveBeenCalledWith({ mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' });
  expect(router.replace).toHaveBeenCalledWith('/devices');
});

test('the gauge says weak, medium or strong, and only the rule blocks', async () => {
  await reachNewBox();
  await type('box-first-new', 'abcdefgh');
  expect(text()).toContain('Solidité : faible');
  await type('box-first-new', 'Kergui26');
  expect(text()).toContain('Solidité : moyenne');
  await type('box-first-confirm', 'Kergui26');
  expect(disabled('box-first-submit')).toBe(false); // medium is allowed
  await type('box-first-new', 'Kergui2026Dakar');
  expect(text()).toContain('Solidité : forte');
  await type('box-first-new', 'Sénégal2026');
  expect(text()).toContain('Pas de lettre accentuée (é, è, ç…) ni d’emoji : la box les refuse.');
});

test('the box’s refusal is said in French, and the owner may try another', async () => {
  await reachNewBox();
  await type('box-first-new', 'Kergui2026');
  await type('box-first-confirm', 'Kergui2026');
  mockApi.firstPassword.mockRejectedValue(
    new ApiError(422, 'the router refused the new password: mot de passe déjà utilisé', 'password_rejected'),
  );
  await press('box-first-submit');
  expect(text()).toContain('La box a refusé ce mot de passe. Choisissez-en un autre, plus long ou plus varié.');
  expect(text()).toContain('Réponse de la box : mot de passe déjà utilisé');
  expect(mockConnected).not.toHaveBeenCalled();
  expect(all('box-first-password').length).toBeGreaterThan(0);

  mockApi.firstPassword.mockRejectedValue(new ApiError(400, 'weak router password', 'weak_password'));
  await press('box-first-submit');
  expect(text()).toContain('Ce mot de passe ne respecte pas les règles de la box.');
});

test('« Annuler » goes back to the connection screen with the label’s password forgotten', async () => {
  await reachNewBox();
  await type('box-first-new', 'Kergui2026');
  await press('box-first-cancel');
  expect(all('box-first-password')).toHaveLength(0);
  expect(all('connect-submit').length).toBeGreaterThan(0);
  expect(valueOf('connect-password')).toBe('');
  expect(mockApi.firstPassword).not.toHaveBeenCalled();
  expect(mockConnected).not.toHaveBeenCalled();
});

test('a member is sent to the owner, on WhatsApp, and nothing is tried', async () => {
  mockRole.mockResolvedValue({ owner: false, ownerPhone: '221770000001' });
  await reachNewBox();
  expect(text()).toContain('Cette box doit d’abord être installée par le propriétaire du foyer.');
  expect(all('box-first-password')).toHaveLength(0);
  await press('box-owner-whatsapp');
  expect(Linking.openURL).toHaveBeenCalledWith(expect.stringMatching(/^https:\/\/wa\.me\/221770000001\?text=/));
  await press('box-owner-back');
  expect(all('connect-submit').length).toBeGreaterThan(0);
  expect(mockApi.firstPassword).not.toHaveBeenCalled();
  expect(mockConnected).not.toHaveBeenCalled();
});

test('a member without the owner’s number gets the message, without a WhatsApp button', async () => {
  mockRole.mockResolvedValue({ owner: false, ownerPhone: null });
  await reachNewBox();
  expect(text()).toContain('Cette box doit d’abord être installée par le propriétaire du foyer.');
  expect(all('box-owner-whatsapp')).toHaveLength(0);
});

test('a refused `user` suggests trying `admin` (Q14)', async () => {
  mockApi.login.mockRejectedValue(new ApiError(401, 'auth failed', 'auth_failed'));
  await reachNewBox();
  expect(text()).toContain('essayez l’identifiant « admin »');
  await type('connect-username', 'admin');
  expect(text()).not.toContain('essayez l’identifiant « admin »');
});

// --- Réglages › « Mot de passe de la box » ----------------------------------

const unlock = LocalAuthentication.authenticateAsync as jest.Mock;

test('the box password is read only after the phone’s unlock, and can be copied', async () => {
  mockCore.boxPassword.mockResolvedValue({ stored: true, username: 'user', password: 'Kergui2026' });
  unlock.mockResolvedValueOnce({ success: false, error: 'user_cancel' });
  await render(<BoxPasswordSetting />);
  expect(text()).toContain('Mot de passe de la box');
  await press('settings-box-password');
  // Cancelled: nothing read, nothing shown, no error either.
  expect(mockCore.boxPassword).not.toHaveBeenCalled();
  expect(text()).not.toContain('Kergui2026');

  unlock.mockResolvedValueOnce({ success: true });
  await press('settings-box-password');
  expect(mockCore.boxPassword).toHaveBeenCalledTimes(1);
  expect(text()).toContain('Identifiant : user');
  expect(text()).toContain('Mot de passe : Kergui2026');
  await press('settings-box-password-copy');
  expect(Clipboard.setStringAsync).toHaveBeenCalledWith('Kergui2026');
  await press('settings-box-password-hide');
  expect(text()).not.toContain('Kergui2026');
});

test('a phone without any lock is told to set one; the password stays hidden', async () => {
  unlock.mockResolvedValueOnce({ success: false, error: 'passcode_not_set' });
  await render(<BoxPasswordSetting />);
  await press('settings-box-password');
  expect(mockCore.boxPassword).not.toHaveBeenCalled();
  expect(text()).toContain('Protégez d’abord votre téléphone par un code ou une empreinte');
});

test('a member does not see « Mot de passe de la box »', async () => {
  mockRole.mockResolvedValue({ owner: false, ownerPhone: null });
  await render(<BoxPasswordSetting />);
  expect(all('settings-box-password')).toHaveLength(0);
  expect(text()).not.toContain('Mot de passe de la box');
});
