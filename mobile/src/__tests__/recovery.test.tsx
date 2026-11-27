/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The "Téléphone perdu" screen (issue #48). The owner designates an
// administrator as co-owner (a viewer cannot be one) and removes one after
// confirming; a co-owner's phone takes the household over; the owner's new
// phone recovers it at home: the relay is found on the Wi-Fi by itself
// (#117), the address typed only under « Je ne le trouve pas », and the box
// password; it is told why when that fails. The demo plays it all in memory.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn(), canGoBack: () => false },
  Stack: { Screen: () => null },
}));
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('../core/client', () => ({ api: { inspect: jest.fn() }, core: {} }));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));
const mockMembers = jest.fn();
jest.mock('../licensing/household', () => ({
  ...jest.requireActual('../licensing/household'),
  household: { members: () => mockMembers() },
}));
const mockLoad = jest.fn();
const mockDesignate = jest.fn();
const mockRemove = jest.fn();
const mockTakeOver = jest.fn();
const mockAtHome = jest.fn();
const mockFind = jest.fn();
jest.mock('../features/recovery', () => ({
  ...jest.requireActual('../features/recovery'),
  loadRecovery: () => mockLoad(),
  recovery: {
    designate: (m: unknown) => mockDesignate(m),
    remove: (keyId: string) => mockRemove(keyId),
    takeOver: () => mockTakeOver(),
    atHome: (a: string, p: string) => mockAtHome(a, p),
    findRelays: () => mockFind(),
  },
  demoFindRelays: () => jest.requireActual('../features/recovery').demoFindRelays(0),
}));
const mockConfirm = jest.fn();
jest.mock('../ui/confirm', () => ({ confirm: (m: string) => mockConfirm(m) }));

import RecoveryScreen from '../app/recovery';
import { RecoveryError } from '../core/recovery';
import { useDemoRecovery } from '../features/recovery';
import { useAccount } from '../licensing/account';
import type { HouseholdMembers } from '../licensing/api';
import { useSession } from '../session/store';

const OWNER = { membershipId: 'M0', userId: 'U0', role: 'owner' as const, phone: '+221771234567', joinedAt: '2026-11-01T10:00:00Z' };
const AWA = {
  membershipId: 'M1',
  userId: 'U1',
  role: 'admin' as const,
  phone: '+221772222222',
  joinedAt: '2026-11-10T10:00:00Z',
  keys: { keyId: 'k-awa', ed25519: 'RUQ=', x25519: 'WA==' },
};
const MOUSSA = { membershipId: 'M2', userId: 'U2', role: 'viewer' as const, phone: '+221773333333', joinedAt: '2026-11-11T10:00:00Z', keys: { keyId: 'k-moussa', ed25519: 'RQ==', x25519: 'WQ==' } };
const BINTA = { membershipId: 'M3', userId: 'U3', role: 'admin' as const, phone: '+221774444444', joinedAt: '2026-11-12T10:00:00Z' };

function listing(over: Partial<HouseholdMembers> = {}): HouseholdMembers {
  return { household: { id: 'T1', name: 'Foyer' }, you: OWNER, members: [OWNER, AWA, MOUSSA, BINTA], invites: [], ...over };
}

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
const type = async (id: string, value: string) => {
  const node = all(id).find((n) => typeof n.props.onChangeText === 'function');
  if (!node) throw new Error(`no input ${id}`);
  await act(async () => void node.props.onChangeText(value));
  await settle();
};

async function render() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
  if (root) await act(async () => root?.unmount());
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <PaperProvider settings={{ icon: () => null }}>
          <RecoveryScreen />
        </PaperProvider>
      </QueryClientProvider>,
    );
  });
  await settle();
}

const signedIn = () =>
  useAccount.setState({ ready: true, account: { accountId: 'acc_1', phone: '+221771234567', sessionToken: 'kgs_1', expiresAt: 'x' } });

const ownerView = (over: object = {}) => ({ owner: true, hasRelay: true, coOwners: [], member: null, ...over });
const memberView = (coOwner: boolean) => ({
  owner: false,
  hasRelay: true,
  coOwners: [],
  member: { keyId: 'k-awa', state: 'active', control: true, coOwner },
});
const recovered = { tenantId: 'T1', bundle: { payload: 'cA==', sig: 'cw==', version: 9 }, revoked: 'k-old' };

beforeEach(() => {
  mockMembers.mockReset().mockResolvedValue(listing());
  mockLoad.mockReset().mockResolvedValue(ownerView());
  mockDesignate.mockReset().mockResolvedValue(undefined);
  mockRemove.mockReset().mockResolvedValue(undefined);
  mockTakeOver.mockReset().mockResolvedValue(recovered);
  mockAtHome.mockReset().mockResolvedValue(recovered);
  mockFind.mockReset().mockResolvedValue({ relays: [{ address: '192.168.1.20', agentId: 'A1', version: 3 }], others: 0 });
  mockConfirm.mockReset().mockResolvedValue(true);
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
  useAccount.setState({ ready: true, account: null });
  useDemoRecovery.setState({ coOwners: {}, recovered: false });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
});

const rowText = (id: string) => texts(all(`coowner-row-${id}`)[0]).join('\n');

test('signed out: the screen asks to sign in with the household’s number', async () => {
  await render();
  expect(text()).toContain('Connectez-vous avec le numéro WhatsApp du foyer');
  expect(all('recovery-sign-in').length).toBeGreaterThan(0);
  expect(mockLoad).not.toHaveBeenCalled();
});

test('the owner designates an administrator as co-owner, never a viewer, and removes one after confirming', async () => {
  signedIn();
  await render();
  expect(all('recovery-owner').length).toBeGreaterThan(0);
  expect(all('coowner-add-M1').length).toBeGreaterThan(0);
  expect(all('coowner-add-M2')).toHaveLength(0);
  expect(rowText('M2')).toContain('Un lecteur ne peut pas être co-propriétaire');
  expect(rowText('M3')).toContain('Pas encore de clé');
  expect(all('coowner-add-M3')).toHaveLength(0);

  await press('coowner-add-M1');
  expect(mockConfirm).toHaveBeenLastCalledWith(expect.stringContaining('+221772222222'));
  expect(mockDesignate).toHaveBeenCalledWith(expect.objectContaining({ membershipId: 'M1' }));

  mockLoad.mockResolvedValue(ownerView({ coOwners: [{ keyId: 'k-awa', label: '+221772222222' }] }));
  await render();
  expect(rowText('M1')).toContain('Co-propriétaire');
  mockConfirm.mockResolvedValueOnce(false);
  await press('coowner-remove-M1');
  expect(mockRemove).not.toHaveBeenCalled();
  await press('coowner-remove-M1');
  expect(mockRemove).toHaveBeenCalledWith('k-awa');
});

test('no relay paired: co-owners are not offered', async () => {
  signedIn();
  mockLoad.mockResolvedValue(ownerView({ hasRelay: false }));
  await render();
  expect(all('coowner-no-relay').length).toBeGreaterThan(0);
  expect(all('coowner-add-M1')).toHaveLength(0);
});

test('a co-owner’s phone takes the household over after confirming', async () => {
  signedIn();
  mockMembers.mockResolvedValue(listing({ you: AWA }));
  mockLoad.mockResolvedValue(memberView(true));
  await render();
  expect(text()).toContain('Le propriétaire vous a désigné co-propriétaire');
  mockConfirm.mockResolvedValueOnce(false);
  await press('recovery-takeover');
  expect(mockTakeOver).not.toHaveBeenCalled();
  await press('recovery-takeover');
  // Titled, with the red button: the old phone cannot undo it (#119).
  expect(mockConfirm).toHaveBeenLastCalledWith(
    expect.objectContaining({ title: 'Reprendre le foyer ?', confirmLabel: 'Oui, reprendre', destructive: true }),
  );
  expect(mockConfirm.mock.calls.at(-1)[0].message).toMatch(/L’ancien téléphone ne pourra plus rien commander/);
  expect(mockTakeOver).toHaveBeenCalledTimes(1);
  expect(all('recovery-done').length).toBeGreaterThan(0);
  expect(text()).toContain('L’ancienne clé est révoquée');
});

test('a member who is not a co-owner sees no takeover, only the recovery at home', async () => {
  signedIn();
  mockMembers.mockResolvedValue(listing({ you: MOUSSA }));
  mockLoad.mockResolvedValue(memberView(false));
  await render();
  expect(all('recovery-takeover')).toHaveLength(0);
  expect(all('recovery-at-home').length).toBeGreaterThan(0);
});

const newPhone = async () => {
  signedIn();
  mockMembers.mockResolvedValue(listing({ you: OWNER }));
  mockLoad.mockResolvedValue({ owner: false, hasRelay: true, coOwners: [], member: null });
  await render();
};

test('the relay is found by itself: no address to type, only the box password', async () => {
  await newPhone();
  expect(mockFind).toHaveBeenCalledTimes(1);
  expect(all('recovery-relay-found').length).toBeGreaterThan(0);
  expect(text()).toContain('Relais trouvé');
  expect(text()).toContain('192.168.1.20');
  expect(all('recovery-relay-address')).toHaveLength(0);
  expect(all('recovery-not-found').length).toBeGreaterThan(0);

  await type('recovery-box-password', 'Sene-Gal-2026');
  mockAtHome.mockRejectedValueOnce(new RecoveryError('proof', 'recovery_proof: …'));
  await press('recovery-home-submit');
  expect(mockAtHome).toHaveBeenLastCalledWith('192.168.1.20', 'Sene-Gal-2026');
  expect(texts(all('recovery-error')[0]).join('')).toContain('Ce n’est pas le mot de passe de la box.');
  // A second try keeps the relay found: no new search.
  await press('recovery-home-submit');
  expect(mockFind).toHaveBeenCalledTimes(1);
  expect(all('recovery-done').length).toBeGreaterThan(0);
  expect(all('recovery-error')).toHaveLength(0);
  expect(all('recovery-home-submit')).toHaveLength(0);
});

test('the box password has an eye: shown, then hidden (#122)', async () => {
  await newPhone();
  await type('recovery-box-password', 'Sene-Gal-2026');
  const secure = () => all('recovery-box-password').find((n) => typeof n.props.onChangeText === 'function')?.props.secureTextEntry;
  expect(secure()).toBe(true);
  await press('recovery-box-password-eye');
  expect(secure()).toBe(false);
  await press('recovery-box-password-eye');
  expect(secure()).toBe(true);
  expect(mockAtHome).not.toHaveBeenCalled();
});

test('several relays answer: the household’s best one, as the core ranks them, is used', async () => {
  mockFind.mockResolvedValue({
    relays: [
      { address: '192.168.1.31', agentId: 'A2', version: 5 },
      { address: '192.168.1.20', agentId: 'A1', version: 4 },
    ],
    others: 2,
  });
  await newPhone();
  expect(text()).toContain('192.168.1.31');
  expect(text()).not.toContain('192.168.1.20');
  await type('recovery-box-password', 'Sene-Gal-2026');
  await press('recovery-home-submit');
  expect(mockAtHome).toHaveBeenLastCalledWith('192.168.1.31', 'Sene-Gal-2026');
});

test('no relay found: the way out is said, and the address is typed under « Je ne le trouve pas »', async () => {
  mockFind.mockResolvedValue({ relays: [], others: 1 });
  await newPhone();
  expect(all('recovery-relay-none').length).toBeGreaterThan(0);
  expect(text()).toContain('Kergui ne trouve pas le relais.');
  expect(text()).toContain('ce téléphone est sur le Wi-Fi de la maison');
  expect(text()).toContain('n’est pas à votre foyer');
  expect(text()).toContain('Je ne le trouve pas');
  expect(all('recovery-relay-address').length).toBeGreaterThan(0);
  const disabled = () => all('recovery-home-submit').some((n) => n.props.disabled === true || n.props.accessibilityState?.disabled === true);
  expect(disabled()).toBe(true);

  await type('recovery-relay-address', ' 192.168.1.44 ');
  await type('recovery-box-password', 'Sene-Gal-2026');
  mockAtHome.mockRejectedValueOnce(new RecoveryError('not_lan', 'recovery_not_lan: …'));
  await press('recovery-home-submit');
  expect(mockAtHome).toHaveBeenLastCalledWith('192.168.1.44', 'Sene-Gal-2026');
  expect(texts(all('recovery-error')[0]).join('')).toContain('ne passe jamais par Internet');

  // « Chercher encore » looks again, and finds it this time.
  mockFind.mockResolvedValue({ relays: [{ address: '192.168.1.20', agentId: 'A1', version: 3 }], others: 0 });
  await press('recovery-search-again');
  expect(mockFind).toHaveBeenCalledTimes(2);
  expect(all('recovery-relay-found').length).toBeGreaterThan(0);
});

test('the search failing is the same as no relay found', async () => {
  mockFind.mockRejectedValue(new Error('hub down'));
  await newPhone();
  expect(all('recovery-relay-none').length).toBeGreaterThan(0);
  expect(all('recovery-relay-address').length).toBeGreaterThan(0);
});

test('a relay found can still be replaced by a typed address, under « Je ne le trouve pas »', async () => {
  await newPhone();
  await press('recovery-not-found');
  expect(all('recovery-relay-address').length).toBeGreaterThan(0);
  await type('recovery-relay-address', '192.168.1.50');
  await type('recovery-box-password', 'p');
  await press('recovery-home-submit');
  expect(mockAtHome).toHaveBeenLastCalledWith('192.168.1.50', 'p');
});

test('the demo: Awa made co-owner, a wrong password refused, then the household recovered', async () => {
  useSession.setState({ session: { mode: 'demo' } } as never);
  await render();
  expect(all('recovery-demo').length).toBeGreaterThan(0);
  expect(text()).toContain('Mot de passe de la box de démo : admin');
  expect(all('coowner-add-demo-3')).toHaveLength(0); // Moussa is a viewer
  await press('coowner-add-demo-2');
  expect(rowText('demo-2')).toContain('Co-propriétaire');
  expect(all('coowner-remove-demo-2').length).toBeGreaterThan(0);

  // The search is simulated: the demo relay is found.
  expect(all('recovery-relay-found').length).toBeGreaterThan(0);
  expect(text()).toContain('192.168.1.20');
  expect(mockFind).not.toHaveBeenCalled();
  await type('recovery-box-password', 'guess');
  await press('recovery-home-submit');
  expect(texts(all('recovery-error')[0]).join('')).toContain('Ce n’est pas le mot de passe de la box.');
  await type('recovery-box-password', 'admin');
  await press('recovery-home-submit');
  expect(all('recovery-done').length).toBeGreaterThan(0);
  expect(all('recovery-error')).toHaveLength(0);
  expect(mockAtHome).not.toHaveBeenCalled();
  expect(text()).not.toMatch(/prix|payer|paiement|FCFA/i);
});
