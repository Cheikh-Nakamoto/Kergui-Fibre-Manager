/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Relays by name (issue #57): Foyer and « Relais à la maison » list the
// household's relays by the name the owner gave them, what they are as the
// second line; the owner renames one after a confirmation (signed in the
// core), a member only reads the names. The demo relay has a name too.
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
const mockView = jest.fn();
const mockRename = jest.fn();
jest.mock('../features/delegations', () => ({
  ...jest.requireActual('../features/delegations'),
  loadDelegations: () => mockView(),
  renameRelay: (id: string, name: string) => mockRename(id, name),
  delegations: { publishKey: async () => false, code: async () => '', grant: jest.fn(), revoke: jest.fn(), setOwnerOnly: jest.fn() },
}));
const mockConfirm = jest.fn();
jest.mock('../ui/confirm', () => ({ confirm: (m: string) => mockConfirm(m) }));

import HouseholdScreen from '../app/household';
import RelayScreen from '../app/relay';
import { useDemoRemote } from '../features/demoRemote';
import { useAccount } from '../licensing/account';
import { useSession } from '../session/store';

const OWNER = { membershipId: 'M0', userId: 'U0', role: 'owner' as const, phone: '+221771234567', joinedAt: '2026-11-01T10:00:00Z' };
const AWA = { membershipId: 'M1', userId: 'U1', role: 'viewer' as const, phone: '+221772222222', joinedAt: '2026-11-10T10:00:00Z' };
const RELAYS = [
  { agentId: 'A1', kind: 'pc', label: 'DESKTOP-42' },
  { agentId: 'A2', kind: 'dedicated', label: 'Téléphone de Awa' },
];
const view = (owner: boolean) => ({ owner, ownerOnly: false, hasRelay: true, relays: RELAYS, delegations: [], member: null });

function texts(node: TestInstance): string[] {
  const out: string[] = [];
  for (const c of node.children) out.push(...(typeof c === 'string' ? [c] : texts(c)));
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

async function render(Screen: () => React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <PaperProvider settings={{ icon: () => null }}>
          <Screen />
        </PaperProvider>
      </QueryClientProvider>,
    );
  });
  await settle();
}

beforeEach(() => {
  mockMembers.mockReset().mockResolvedValue({ household: { id: 'T1', name: 'Foyer' }, you: OWNER, members: [OWNER, AWA], invites: [] });
  mockView.mockReset().mockResolvedValue(view(true));
  mockRename.mockReset().mockResolvedValue(undefined);
  mockConfirm.mockReset().mockResolvedValue(true);
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
  useAccount.setState({ ready: true, account: { accountId: 'acc_1', phone: '+221771234567', sessionToken: 'kgs_1', expiresAt: 'x' } });
  useDemoRemote.setState({ relay: null, kind: 'pc' });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
});

test.each([
  ['Foyer', HouseholdScreen],
  ['Relais', RelayScreen],
])('%s: the owner sees the relays by name and renames one after confirming', async (_, Screen) => {
  await render(Screen);
  expect(text()).toContain('Relais (2)');
  expect(text()).toContain('DESKTOP-42');
  expect(text()).toContain('Ordinateur');
  expect(text()).toContain('Téléphone de Awa');
  expect(text()).toContain('Téléphone à la maison');

  await press('relay-rename-A1');
  await type('relay-rename-input', 'PC du salon');
  mockConfirm.mockResolvedValueOnce(false);
  await press('relay-rename-save');
  expect(mockConfirm).toHaveBeenLastCalledWith(expect.stringContaining('« DESKTOP-42 » en « PC du salon »'));
  expect(mockRename).not.toHaveBeenCalled();

  mockView.mockResolvedValue({ ...view(true), relays: [{ ...RELAYS[0], label: 'PC du salon' }, RELAYS[1]] });
  await press('relay-rename-save');
  expect(mockRename).toHaveBeenCalledWith('A1', 'PC du salon');
  expect(text()).toContain('PC du salon');
  expect(text()).not.toContain('DESKTOP-42');
});

test('a member sees the names but cannot rename', async () => {
  mockMembers.mockResolvedValue({ household: { id: 'T1', name: 'Foyer' }, you: AWA, members: [OWNER, AWA], invites: [] });
  mockView.mockResolvedValue(view(false));
  await render(HouseholdScreen);
  expect(text()).toContain('DESKTOP-42');
  expect(all('relay-rename-A1')).toHaveLength(0);
});

test('a relay without a name says so', async () => {
  mockView.mockResolvedValue({ ...view(true), relays: [{ agentId: 'A3', kind: 'pc' }] });
  await render(HouseholdScreen);
  expect(text()).toContain('Relais sans nom');
});

test('demo: the paired demo relay has a name, renamed in memory, in Foyer and Relais', async () => {
  useSession.setState({ session: { mode: 'demo' } });
  await render(HouseholdScreen);
  expect(all('relay-row-demo')).toHaveLength(0); // not paired yet
  await act(async () => root?.unmount());

  useDemoRemote.getState().paired('PC du salon (démo)', 'pc');
  await render(RelayScreen);
  expect(text()).toContain('PC du salon (démo)');
  await press('relay-rename-demo');
  await type('relay-rename-input', 'Ordi de Moussa');
  await press('relay-rename-save');
  expect(useDemoRemote.getState().relay).toBe('Ordi de Moussa');
  expect(text()).toContain('Ordi de Moussa');
  await act(async () => root?.unmount());

  await render(HouseholdScreen);
  expect(all('relay-row-demo').length).toBeGreaterThan(0);
  expect(text()).toContain('Ordi de Moussa');
});
