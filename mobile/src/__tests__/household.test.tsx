/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The Household screen (issue #42). The owner lists the members, invites a
// number on WhatsApp with a role, changes a role, removes a member and
// cancels a pending invite. A member who is not the owner sees the list
// only. In the demo, a made-up household and how to get one's own; signed
// out, the way to sign in.
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
// Remote access (issue #47) has its own tests (delegations.test.tsx).
jest.mock('../features/delegations', () => ({
  ...jest.requireActual('../features/delegations'),
  loadDelegations: async () => ({ owner: true, ownerOnly: false, hasRelay: false, delegations: [], member: null }),
  delegations: { publishKey: async () => false, code: async () => '', grant: jest.fn(), revoke: jest.fn(), setOwnerOnly: jest.fn() },
}));
const mockConfirm = jest.fn();
jest.mock('../ui/confirm', () => ({ confirm: (m: string) => mockConfirm(m) }));

import { router } from 'expo-router';

import HouseholdScreen from '../app/household';
import { useAccount } from '../licensing/account';
import { HubError, type HouseholdMembers } from '../licensing/api';
import { useSession } from '../session/store';

const OWNER = { membershipId: 'M0', userId: 'U0', role: 'owner' as const, phone: '+221771234567', joinedAt: '2026-11-01T10:00:00Z' };
const AWA = { membershipId: 'M1', userId: 'U1', role: 'viewer' as const, phone: '+221772222222', joinedAt: '2026-11-10T10:00:00Z' };

function listing(over: Partial<HouseholdMembers> = {}): HouseholdMembers {
  return {
    household: { id: 'T1', name: 'Foyer +221••••67' },
    you: OWNER,
    members: [OWNER, AWA],
    invites: [{ id: 'I1', phone: '+221773333333', role: 'admin', createdAt: '2026-11-20T10:00:00Z', expiresAt: '2026-11-27T10:00:00Z' }],
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
const type = async (id: string, value: string) => {
  const node = all(id).find((n) => typeof n.props.onChangeText === 'function');
  if (!node) throw new Error(`no input ${id}`);
  await act(async () => void node.props.onChangeText(value));
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

beforeEach(() => {
  mockMembers.mockReset().mockResolvedValue(listing());
  mockInvite.mockReset().mockResolvedValue({ id: 'I2', phone: '+221774444444', role: 'viewer', createdAt: 'x', expiresAt: 'y' });
  mockCancel.mockReset().mockResolvedValue(undefined);
  mockChangeRole.mockReset().mockResolvedValue({ ...AWA, role: 'admin' });
  mockRemove.mockReset().mockResolvedValue(undefined);
  mockConfirm.mockReset().mockResolvedValue(true);
  (router.push as jest.Mock).mockReset();
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
  useAccount.setState({ ready: true, account: null });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
});

test('demo: an example household, marked as such, and how to get one of your own', async () => {
  useSession.setState({ session: { mode: 'demo' } });
  await render();
  expect(all('household-demo').length).toBeGreaterThan(0);
  expect(text()).toMatch(/Connectez-vous avec votre numéro WhatsApp pour inviter/);
  expect(text()).toContain('Propriétaire');
  expect(text()).toContain('Lecteur');
  expect(all('household-invite-send')).toHaveLength(0); // nothing is sent from the demo
  expect(mockMembers).not.toHaveBeenCalled();
});

test('signed out: the way to sign in, and no call to the hub', async () => {
  await render();
  expect(text()).toMatch(/Connectez-vous avec votre numéro WhatsApp pour voir votre foyer/);
  await press('household-sign-in');
  expect(router.push).toHaveBeenCalledWith('/sign-in');
  expect(mockMembers).not.toHaveBeenCalled();
});

test('the owner sees the members with their roles and the pending invites', async () => {
  signedIn();
  await render();
  const s = text();
  expect(s).toContain('Membres (2)');
  expect(s).toContain('+221771234567 (vous)');
  expect(s).toContain('+221772222222');
  expect(s).toContain('Propriétaire');
  expect(s).toContain('Lecteur');
  expect(s).toContain('+221773333333');
  expect(s).toContain('Invitations en attente');
  // The owner's own row has no action; the viewer's does.
  expect(all('household-remove-M0')).toHaveLength(0);
  expect(all('household-remove-M1').length).toBeGreaterThan(0);
});

test('the owner invites a number as an admin: it goes out on WhatsApp and the list is reloaded', async () => {
  signedIn();
  await render();
  await type('household-invite-phone', '77 444 44 44');
  await press('household-invite-role-admin');
  await press('household-invite-send');
  expect(mockInvite).toHaveBeenCalledWith('77 444 44 44', 'admin');
  expect(text()).toMatch(/Invitation envoyée sur WhatsApp à \+221774444444/);
  expect(mockMembers).toHaveBeenCalledTimes(2);
});

test('an invite the hub refuses is worded in French', async () => {
  signedIn();
  mockInvite.mockRejectedValue(new HubError(409, 'ALREADY_MEMBER', 'x'));
  await render();
  await type('household-invite-phone', '+221772222222');
  await press('household-invite-send');
  // A viewer by default: the narrowest role.
  expect(mockInvite).toHaveBeenCalledWith('+221772222222', 'viewer');
  expect(text()).toContain('Ce numéro fait déjà partie du foyer.');
});

test('the owner changes a role, removes a member after confirming, and cancels an invite', async () => {
  signedIn();
  await render();
  await press('household-role-M1');
  expect(mockChangeRole).toHaveBeenCalledWith('M1', 'admin');

  mockConfirm.mockResolvedValueOnce(false);
  await press('household-remove-M1');
  expect(mockRemove).not.toHaveBeenCalled();
  await press('household-remove-M1');
  expect(mockConfirm).toHaveBeenLastCalledWith(expect.stringContaining('+221772222222'));
  expect(mockRemove).toHaveBeenCalledWith('M1');

  await press('household-cancel-I1');
  expect(mockCancel).toHaveBeenCalledWith('I1');
});

test('a viewer sees the list only, with why', async () => {
  signedIn();
  const me = { ...AWA, phone: '+221772222222' };
  mockMembers.mockResolvedValue(listing({ you: me, members: [{ ...OWNER, phone: '+221••••••67' }, me], invites: undefined }));
  await render();
  expect(text()).toContain('+221772222222 (vous)');
  expect(text()).toContain('Seul le propriétaire du foyer peut inviter');
  expect(all('household-invite-send')).toHaveLength(0);
  expect(all('household-remove-M0')).toHaveLength(0);
  expect(all('household-role-M0')).toHaveLength(0);
});

test('the list failing shows the hub error, in French', async () => {
  signedIn();
  mockMembers.mockRejectedValue(new HubError(0, 'NETWORK', 'offline'));
  await render();
  expect(text()).toMatch(/Pas de connexion à Internet/);
});
