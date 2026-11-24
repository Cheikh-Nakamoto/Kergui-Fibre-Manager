/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The Household screen's remote access (issue #47). The owner sees, per
// member, what the bundle delegates them and the six digits to compare,
// grants "Voir" or "Contrôler" (an admin only), revokes after confirming,
// and keeps the box to themself; a member who leaves is revoked too. A
// member sees their own access and publishes their phone's key. The demo
// plays it all in memory.
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
const mockInvite = jest.fn();
const mockCancel = jest.fn();
const mockChangeRole = jest.fn();
const mockRemove = jest.fn();
jest.mock('../licensing/household', () => ({
  ...jest.requireActual('../licensing/household'),
  household: {
    members: () => mockMembers(),
    invite: (phone: string, role: string) => mockInvite(phone, role),
    cancelInvite: (id: string) => mockCancel(id),
    changeRole: (id: string, role: string) => mockChangeRole(id, role),
    remove: (id: string) => mockRemove(id),
  },
}));
const mockLoad = jest.fn();
const mockGrant = jest.fn();
const mockRevoke = jest.fn();
const mockOwnerOnly = jest.fn();
const mockPublish = jest.fn();
const mockCode = jest.fn();
jest.mock('../features/delegations', () => ({
  ...jest.requireActual('../features/delegations'),
  loadDelegations: () => mockLoad(),
  delegations: {
    grant: (m: unknown, scope: string) => mockGrant(m, scope),
    revoke: (keyId: string) => mockRevoke(keyId),
    setOwnerOnly: (on: boolean) => mockOwnerOnly(on),
    publishKey: (you: unknown) => mockPublish(you),
    code: (m: { membershipId: string }) => mockCode(m),
  },
}));
const mockConfirm = jest.fn();
jest.mock('../ui/confirm', () => ({ confirm: (m: string) => mockConfirm(m) }));

import HouseholdScreen from '../app/household';
import { useAccount } from '../licensing/account';
import type { HouseholdMembers } from '../licensing/api';
import { useSession } from '../session/store';

import { useDemoDelegations } from '../features/delegations';

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
  return {
    household: { id: 'T1', name: 'Foyer +221••••67' },
    you: OWNER,
    members: [OWNER, AWA, MOUSSA, BINTA],
    invites: [],
    ...over,
  };
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

async function render() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <PaperProvider settings={{ icon: () => null }}>
          <HouseholdScreen />
        </PaperProvider>
      </QueryClientProvider>,
    );
  });
  await settle();
}

const signedIn = () =>
  useAccount.setState({ ready: true, account: { accountId: 'acc_1', phone: '+221771234567', sessionToken: 'kgs_1', expiresAt: 'x' } });


const ownerView = (over: object = {}) => ({ owner: true, ownerOnly: false, hasRelay: true, delegations: [], member: null, ...over });

beforeEach(() => {
  mockMembers.mockReset().mockResolvedValue(listing());
  mockRemove.mockReset().mockResolvedValue(undefined);
  mockConfirm.mockReset().mockResolvedValue(true);
  mockLoad.mockReset().mockResolvedValue(ownerView());
  mockGrant.mockReset().mockResolvedValue(undefined);
  mockRevoke.mockReset().mockResolvedValue(undefined);
  mockOwnerOnly.mockReset().mockResolvedValue(undefined);
  mockPublish.mockReset().mockResolvedValue(false);
  mockCode.mockReset().mockImplementation(async (m: { membershipId: string }) => (m.membershipId === 'M1' ? '482913' : '730254'));
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
  useAccount.setState({ ready: true, account: null });
  useDemoDelegations.setState({ byMember: {}, ownerOnly: false });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
});

// A Paper Switch renders the native switch, whose onChange carries the value.
const toggle = async (id: string, value: boolean) => {
  const node = all(id).find((n) => typeof n.props.onChange === 'function');
  if (!node) throw new Error(`no switch ${id}`);
  await act(async () => void node.props.onChange({ nativeEvent: { value } }));
  await settle();
};

const rowText = (id: string) => texts(all(`delegation-row-${id}`)[0]).join('\n');

test('the owner sees each member’s access, the code to compare, and who may control', async () => {
  signedIn();
  mockLoad.mockResolvedValue(ownerView({ delegations: [{ keyId: 'k-awa', scope: 'control', expiresAt: '2026-12-22T10:00:00Z' }] }));
  await render();
  expect(text()).toContain('Accès à distance');
  expect(rowText('M1')).toContain('Contrôler · jusqu’au 22/12/2026');
  expect(rowText('M1')).toContain('Code à comparer avec son écran : 482 913');
  expect(all('delegation-revoke-M1').length).toBeGreaterThan(0);
  // A viewer may see, not control: the hub refuses a viewer's writes.
  expect(rowText('M2')).toContain('Aucun accès à distance');
  expect(all('delegation-view-M2').length).toBeGreaterThan(0);
  expect(all('delegation-control-M2')).toHaveLength(0);
  expect(rowText('M2')).toContain('rendez-le administrateur');
  // A member whose phone has not published its key yet: nothing to grant.
  expect(rowText('M3')).toContain('Pas encore de clé : +221774444444 doit ouvrir Réglages > Plus de fonctionnalités > Foyer');
  expect(all('delegation-view-M3')).toHaveLength(0);
  expect(mockPublish).not.toHaveBeenCalled(); // the owner's phone publishes no member key
});

test('the owner grants, revokes after confirming, and keeps the box to themself', async () => {
  signedIn();
  await render();
  await press('delegation-view-M2');
  expect(mockGrant).toHaveBeenCalledWith(expect.objectContaining({ membershipId: 'M2' }), 'view');
  await press('delegation-control-M1');
  expect(mockGrant).toHaveBeenLastCalledWith(expect.objectContaining({ membershipId: 'M1' }), 'control');
  expect(mockLoad.mock.calls.length).toBeGreaterThanOrEqual(3); // read again after each change

  mockLoad.mockResolvedValue(ownerView({ delegations: [{ keyId: 'k-awa', scope: 'control', expiresAt: '2026-12-22T10:00:00Z' }] }));
  await render();
  mockConfirm.mockResolvedValueOnce(false);
  await press('delegation-revoke-M1');
  expect(mockRevoke).not.toHaveBeenCalled();
  await press('delegation-revoke-M1');
  expect(mockConfirm).toHaveBeenLastCalledWith(expect.stringContaining('+221772222222'));
  expect(mockRevoke).toHaveBeenCalledWith('k-awa');

  await toggle('delegation-owner-only', true);
  expect(mockOwnerOnly).toHaveBeenCalledWith(true);
});

test('removing a delegated member revokes their access too', async () => {
  signedIn();
  mockLoad.mockResolvedValue(ownerView({ delegations: [{ keyId: 'k-awa', scope: 'view', expiresAt: '2026-12-22T10:00:00Z' }] }));
  await render();
  await press('household-remove-M1');
  expect(mockRemove).toHaveBeenCalledWith('M1');
  expect(mockRevoke).toHaveBeenCalledWith('k-awa');
  await press('household-remove-M2');
  expect(mockRevoke).toHaveBeenCalledTimes(1); // nothing to revoke for one with no delegation
});

test('with no relay the owner is told to install one first', async () => {
  signedIn();
  mockLoad.mockResolvedValue(ownerView({ hasRelay: false }));
  await render();
  expect(all('delegation-no-relay').length).toBeGreaterThan(0);
  expect(all('delegation-view-M1')).toHaveLength(0);
});

test('a member publishes their phone’s key and sees their own access', async () => {
  signedIn();
  const me = { ...AWA, keys: undefined };
  mockMembers.mockResolvedValue(listing({ you: me, members: [OWNER, me], invites: undefined }));
  mockLoad.mockResolvedValue({
    owner: false,
    ownerOnly: false,
    hasRelay: true,
    delegations: [],
    member: { keyId: 'k-awa', state: 'none', control: false, code: '482913' },
  });
  await render();
  expect(mockPublish).toHaveBeenCalledWith(expect.objectContaining({ userId: 'U1' }));
  const you = texts(all('delegation-you')[0]).join('\n');
  expect(you).toContain('Le propriétaire ne vous a pas encore donné d’accès à distance.');
  expect(you).toContain('Vérifiez que son écran affiche le même code : 482 913');
  expect(all('delegation-owner-only')).toHaveLength(0);
});

test.each([
  [{ state: 'active', scope: 'control', control: true, expiresAt: '2026-12-22T10:00:00Z' }, false, 'voir et contrôler les appareils à distance, jusqu’au 22/12/2026'],
  [{ state: 'active', scope: 'view', control: false, expiresAt: '2026-12-22T10:00:00Z' }, false, 'voir les appareils à distance, jusqu’au 22/12/2026'],
  [{ state: 'active', scope: 'control', control: false, expiresAt: '2026-12-22T10:00:00Z' }, true, 'réservé au propriétaire pour l’instant'],
  [{ state: 'expired', control: false }, false, 'a expiré. Il revient quand le propriétaire ouvre Kergui'],
  [{ state: 'revoked', control: false }, false, 'a révoqué votre accès'],
])('a member’s access %#', async (member, ownerOnly, want) => {
  signedIn();
  mockMembers.mockResolvedValue(listing({ you: AWA, members: [OWNER, AWA], invites: undefined }));
  mockLoad.mockResolvedValue({ owner: false, ownerOnly, hasRelay: true, delegations: [], member: { keyId: 'k-awa', ...member } });
  await render();
  expect(texts(all('delegation-you')[0]).join('\n')).toContain(want);
});

test('demo: grant « Voir » and « Contrôler » for 30 days, revoke, owner-only, nothing sent', async () => {
  useSession.setState({ session: { mode: 'demo' } });
  await render();
  expect(all('delegation-demo').length).toBeGreaterThan(0);
  expect(rowText('demo-2')).toContain('Aucun accès à distance');
  expect(rowText('demo-2')).toContain('482 913');
  await press('delegation-control-demo-2');
  expect(rowText('demo-2')).toMatch(/Contrôler · jusqu’au \d\d\/\d\d\/\d{4}/);
  await press('delegation-view-demo-3');
  expect(rowText('demo-3')).toMatch(/Voir · jusqu’au/);
  expect(all('delegation-control-demo-3')).toHaveLength(0); // a viewer
  await press('delegation-revoke-demo-2');
  expect(rowText('demo-2')).toContain('Aucun accès à distance');
  await toggle('delegation-owner-only', true);
  expect(useDemoDelegations.getState().ownerOnly).toBe(true);
  expect(mockGrant).not.toHaveBeenCalled();
  expect(mockLoad).not.toHaveBeenCalled();
});
