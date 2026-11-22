/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The relay pairing screens (issue #43), in French and without a word of
// cryptography: what a relay is, which device, the QR, the six digits to
// compare, and what "same" and "different" do. In the demo the core plays
// the relay; for a member who is not the owner, no pairing at all.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn(), canGoBack: () => true },
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
const mockRelays = { start: jest.fn(), arrived: jest.fn(), code: jest.fn(), confirm: jest.fn(), reject: jest.fn() };
jest.mock('../licensing/relays', () => ({
  get relays() {
    return mockRelays;
  },
}));
const mockOwner = { invite: jest.fn(), demoRelay: jest.fn(), pairingCode: jest.fn(), signBundle: jest.fn(), keys: jest.fn() };
jest.mock('../core/owner', () => ({
  get owner() {
    return mockOwner;
  },
}));

import { router } from 'expo-router';

import RelayScreen, { groupCode, POLL_MS } from '../app/relay';
import { useAccount } from '../licensing/account';
import { HubError } from '../licensing/api';
import { useSession } from '../session/store';

const OWNER = { membershipId: 'M0', userId: 'U0', role: 'owner' as const, phone: '+221771234567' };
const PC = { id: 'A2', name: 'PC du salon', kind: 'pc', keyId: 'k', ed25519: 'RUQ=', x25519: 'WA==', createdAt: 'x' };
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
const press = async (id: string) => {
  const node = all(id).find((n) => typeof n.props.onClick === 'function' || typeof n.props.onPress === 'function');
  if (!node) throw new Error(`no ${id}`);
  const ev = { nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined };
  await act(async () => void (node.props.onClick ?? node.props.onPress)(ev));
  await settle();
};

async function render() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <PaperProvider settings={{ icon: () => null }}>
          <RelayScreen />
        </PaperProvider>
      </QueryClientProvider>,
    );
  });
  await settle();
}

const signedIn = () =>
  useAccount.setState({ ready: true, account: { accountId: 'acc_1', phone: '+221771234567', sessionToken: 'kgs_1', expiresAt: 'x' } });

beforeEach(() => {
  mockMembers.mockReset().mockResolvedValue({ household: { id: 'T1', name: 'Foyer' }, you: OWNER, members: [OWNER], invites: [] });
  for (const m of [...Object.values(mockRelays), ...Object.values(mockOwner)]) m.mockReset();
  mockRelays.start.mockResolvedValue({ invite: INVITE, expiresAt: new Date(Date.now() + 300_000).toISOString(), known: ['A1'] });
  mockRelays.arrived.mockResolvedValue(PC);
  mockRelays.code.mockResolvedValue('042917');
  mockRelays.confirm.mockResolvedValue(2);
  mockRelays.reject.mockResolvedValue(undefined);
  (router.back as jest.Mock).mockReset();
  useSession.setState({ session: { mode: 'router', routerUrl: 'https://192.168.1.1', pin: 'ab12' } });
  useAccount.setState({ ready: true, account: null });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
});

test('the six digits read in two groups', () => {
  expect(groupCode('042917')).toBe('042 917');
});

test('onboarding in plain French: what a relay is and which device', async () => {
  signedIn();
  await render();
  expect(all('relay-intro').length).toBeGreaterThan(0);
  const s = text();
  expect(s).toContain('Un relais pour votre box');
  expect(s).toMatch(/reste chez vous, allumé et sur le Wi-Fi de la box/);
  expect(s).toMatch(/code à 6 chiffres/);
  expect(s).toContain('Un ordinateur');
  expect(s).toContain('Un téléphone qui reste à la maison');
  expect(s).toContain('Afficher le QR code');
  // No jargon for a household.
  expect(s).not.toMatch(/Ed25519|X25519|empreinte|clé publique|bundle|fingerprint/i);
});

test('the owner pairs a PC: QR, the relay arrives, same code, signed in', async () => {
  jest.useFakeTimers({ advanceTimers: true });
  signedIn();
  mockRelays.arrived.mockResolvedValueOnce(null);
  await render();
  await press('relay-start');
  expect(mockRelays.start).toHaveBeenCalledWith('pc', 'ab12');
  // The QR and, for a computer, the command to type.
  expect(all('relay-qr').length).toBeGreaterThan(0);
  expect(text()).toContain('Montrez ce QR code au relais');
  expect(text()).toContain(`kergui hub enroll ${INVITE} --hub https://hub.test`);
  expect(text()).toContain('En attente du relais…');
  // The relay redeemed it: on the next look, the screen compares the codes.
  await act(async () => void jest.advanceTimersByTime(POLL_MS));
  await settle();
  jest.useRealTimers();
  expect(mockRelays.arrived).toHaveBeenCalledWith(['A1']);
  expect(mockRelays.code).toHaveBeenCalledWith(PC);
  expect(text()).toContain('Comparez les deux codes');
  expect(text()).toMatch(/Le relais « PC du salon » affiche lui aussi un code à 6 chiffres/);
  expect(text()).toContain('042 917');

  await press('relay-same');
  expect(mockRelays.confirm).toHaveBeenCalledWith(PC);
  expect(mockRelays.reject).not.toHaveBeenCalled();
  expect(all('relay-done').length).toBeGreaterThan(0);
  expect(text()).toContain('Relais ajouté');
  expect(text()).toMatch(/« PC du salon » est maintenant le relais de votre box/);
  await press('relay-finish');
  expect(router.back).toHaveBeenCalled();
});

test('a different code removes the relay and signs nothing', async () => {
  signedIn();
  await render();
  await press('relay-kind-dedicated');
  await press('relay-start');
  expect(mockRelays.start).toHaveBeenCalledWith('dedicated', 'ab12');
  await press('relay-different');
  expect(mockRelays.reject).toHaveBeenCalledWith(PC);
  expect(mockRelays.confirm).not.toHaveBeenCalled();
  expect(text()).toContain('Ajout annulé');
  expect(text()).toMatch(/par sécurité, ce relais a été retiré/);
  await press('relay-again');
  expect(all('relay-intro').length).toBeGreaterThan(0);
});

test('waiting for the relay, then an expired QR', async () => {
  signedIn();
  mockRelays.arrived.mockResolvedValue(null);
  mockRelays.start.mockResolvedValue({ invite: INVITE, expiresAt: new Date(Date.now() - 1000).toISOString(), known: [] });
  await render();
  await press('relay-start');
  expect(text()).toContain('Le QR code a expiré. Recommencez pour en afficher un nouveau.');
});

test('the QR screen says it is waiting while the relay has not scanned', async () => {
  signedIn();
  mockRelays.arrived.mockResolvedValue(null);
  await render();
  await press('relay-kind-dedicated');
  await press('relay-start');
  expect(text()).toMatch(/Sur le téléphone qui servira de relais/);
  expect(all('relay-command')).toHaveLength(0);
  expect(all('relay-waiting').length).toBeGreaterThan(0);
  expect(text()).toContain('En attente du relais…');
  expect(text()).toMatch(/Ce QR code est valable jusqu’à \d\d:\d\d/);
});

test('a member who is not the owner cannot pair', async () => {
  signedIn();
  mockMembers.mockResolvedValue({
    household: { id: 'T1', name: 'Foyer' },
    you: { ...OWNER, role: 'admin' },
    members: [],
    invites: [],
  });
  await render();
  expect(text()).toContain('Seul le propriétaire du foyer peut ajouter un relais.');
  expect(all('relay-start')).toHaveLength(0);
});

test('signed out: how to sign in, and no call to the hub', async () => {
  await render();
  expect(text()).toContain('Connectez-vous avec votre numéro WhatsApp pour ajouter un relais.');
  expect(mockMembers).not.toHaveBeenCalled();
});

test('a hub refusal is worded, not thrown', async () => {
  signedIn();
  mockRelays.start.mockRejectedValue(new HubError(0, 'NETWORK', 'offline'));
  await render();
  await press('relay-start');
  expect(all('relay-error').length).toBeGreaterThan(0);
  expect(all('relay-intro').length).toBeGreaterThan(0);
});

test('demo: the core plays the relay, both codes shown, nothing sent', async () => {
  useSession.setState({ session: { mode: 'demo' } });
  mockOwner.invite.mockResolvedValue(INVITE);
  mockOwner.demoRelay.mockResolvedValue({ agentId: 'demo-relay-1', name: 'PC du salon (démo)', kind: 'pc', ed25519: 'RUQ=', x25519: 'WA==', code: '042917' });
  mockOwner.pairingCode.mockResolvedValue('042917');
  mockOwner.signBundle.mockResolvedValue({ payload: 'P', sig: 'S', version: 1 });
  await render();
  expect(text()).toContain('Démo : rien n’est envoyé, le relais est simulé dans l’app.');
  await press('relay-start');
  expect(all('relay-qr').length).toBeGreaterThan(0);
  await press('relay-demo-scan');
  expect(mockOwner.demoRelay).toHaveBeenCalledWith(INVITE);
  expect(text()).toContain('042 917');
  expect(text()).toContain('Écran du relais : 042 917');
  await press('relay-same');
  expect(mockOwner.signBundle).toHaveBeenCalledWith('demo', null, {
    relay: { agentId: 'demo-relay-1', kind: 'pc', label: 'PC du salon (démo)', ed25519: 'RUQ=', x25519: 'WA==' },
  });
  expect(text()).toContain('Relais ajouté');
  expect(mockRelays.start).not.toHaveBeenCalled();
  expect(mockRelays.confirm).not.toHaveBeenCalled();
  expect(mockMembers).not.toHaveBeenCalled();
});
