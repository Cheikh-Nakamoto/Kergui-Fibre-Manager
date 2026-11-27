/* eslint-disable import/first -- the mocks must come before the modules they replace */
// « Plus de fonctionnalités » (#76): a coloured tile at the top of Réglages
// opens a screen with the box's level and the features grouped by purpose
// (contrôle à distance, alertes WhatsApp, automatisation). A feature the
// level does not include shows a lock and the level that does; the demo
// shows none. Réglages no longer lists the entries that moved.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn(), canGoBack: () => true },
  Stack: { Screen: () => null },
  useFocusEffect: (cb: () => void) => jest.requireActual('react').useEffect(cb, [cb]),
}));
jest.mock('expo-constants', () => ({ __esModule: true, default: { expoConfig: { version: '0.2.0' } } }));
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('../alerts/alerts', () => ({
  alertsEnabled: async () => false,
  askBatteryUnrestricted: async () => undefined,
  batteryRestricted: () => false,
  disableAlerts: async () => undefined,
  forgetKnown: async () => undefined,
}));
jest.mock('../alerts/push', () => ({ pushToken: { forget: async () => undefined, sync: async () => undefined } }));
jest.mock('../alerts/switchOn', () => ({ switchAlertsOn: async () => true }));
const mockAutoReject = jest.fn();
jest.mock('../core/client', () => ({ api: { autoReject: () => mockAutoReject() }, core: { version: () => 'test' }, ApiError: class {} }));
jest.mock('../core/relayPhone', () => ({
  relayPhone: { enabled: () => false, supported: () => true, enrolled: async () => false, trusted: async () => false },
}));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));
jest.mock('../ui/ImportBackup', () => ({ ImportBackup: () => null }));
let mockGate = { tier: 'basic', demo: false };
jest.mock('../licensing/useGate', () => {
  const { canUse } = jest.requireActual('../licensing/gate');
  return { useGate: () => ({ ...mockGate, can: (f: string) => canUse(f, mockGate), guard: jest.fn() }) };
});

import { router } from 'expo-router';

import Settings from '../app/(tabs)/settings';
import FeaturesScreen from '../app/features';
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
const pressable = (n: TestInstance) => typeof n.props.onClick === 'function' || typeof n.props.onPress === 'function';
const press = async (id: string) => {
  // A card's touchable sits around the element carrying its testID.
  let node: TestInstance | null | undefined = all(id).find(pressable) ?? all(id)[0];
  while (node && !pressable(node)) node = node.parent;
  if (!node) throw new Error(`no ${id}`);
  const ev = { nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined };
  await act(async () => void (node.props.onClick ?? node.props.onPress)(ev));
  await settle();
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

const MOVED = ['settings-household', 'settings-whatsapp', 'settings-relay', 'settings-relay-phone', 'settings-auto-reject', 'settings-plan'];

beforeEach(() => {
  mockGate = { tier: 'basic', demo: false };
  mockAutoReject.mockReset().mockResolvedValue({ enabled: false, devices: [] });
  (router.push as jest.Mock).mockReset();
  useSession.setState({ session: { mode: 'demo' } });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
});

test('Réglages: the tile opens « Plus de fonctionnalités », and the moved entries are gone', async () => {
  await render(Settings);
  expect(text()).toContain('Plus de fonctionnalités');
  for (const id of MOVED) expect(all(id)).toHaveLength(0);
  // The essentials stay.
  for (const id of ['settings-account', 'settings-recovery', 'settings-alerts', 'settings-journal', 'settings-sign-out']) {
    expect(all(id).length).toBeGreaterThan(0);
  }
  await press('settings-more-features');
  expect(router.push).toHaveBeenCalledWith('/features');
});

test('the screen: the level on top, then the three groups and their entries', async () => {
  await render(FeaturesScreen);
  expect(all('settings-plan').length).toBeGreaterThan(0);
  const s = text();
  expect(s).toContain('Fonctionnalités · niveau Basic');
  expect(s).toContain('Contrôle à distance');
  expect(s).toContain('Alertes WhatsApp');
  expect(s).toContain('Automatisation');
  for (const id of ['features-group-remote', 'features-group-whatsapp', 'features-group-automation']) {
    expect(all(id).length).toBeGreaterThan(0);
  }
  // Each group holds its entries, under the ids they had in Réglages.
  const inGroup = (group: string, id: string) => all(group)[0].queryAll((n) => n.props.testID === id).length > 0;
  expect(inGroup('features-group-remote', 'settings-relay')).toBe(true);
  expect(inGroup('features-group-remote', 'settings-relay-phone')).toBe(true);
  expect(inGroup('features-group-remote', 'settings-household')).toBe(true);
  expect(inGroup('features-group-whatsapp', 'settings-whatsapp')).toBe(true);
  expect(inGroup('features-group-whatsapp', 'features-whatsapp-hub-key')).toBe(true);
  expect(inGroup('features-group-automation', 'features-auto-reject')).toBe(true);
  // One sentence each.
  expect(s).toContain('Pour gérer la box quand vous n’êtes pas chez vous.');
  expect(s).toContain('Les appareils que vous n’avez pas autorisés sont refusés tout seuls.');
});

test('each tile opens its existing screen', async () => {
  await render(FeaturesScreen);
  const opens: [string, string][] = [
    ['settings-relay', '/relay'],
    ['settings-relay-phone', '/become-relay'],
    ['settings-household', '/household'],
    ['settings-whatsapp', '/whatsapp'],
    ['features-whatsapp-hub-key', '/whatsapp'],
    ['features-auto-reject', '/auto-reject'],
  ];
  for (const [id, href] of opens) {
    (router.push as jest.Mock).mockReset();
    await press(id);
    expect(router.push).toHaveBeenCalledWith(href);
  }
});

test('Basic: auto-reject shows the lock and « Niveau Max »; the rest is open', async () => {
  await render(FeaturesScreen);
  expect(all('features-auto-reject-locked').length).toBeGreaterThan(0);
  expect(text()).toContain('Niveau Max');
  for (const id of ['settings-relay', 'settings-household', 'settings-whatsapp', 'features-whatsapp-hub-key']) {
    expect(all(`${id}-locked`)).toHaveLength(0);
  }
});

test('Max: no lock', async () => {
  mockGate = { tier: 'max', demo: false };
  await render(FeaturesScreen);
  expect(all('features-auto-reject-locked')).toHaveLength(0);
  expect(text()).not.toContain('Niveau Max');
});

test('the demo shows no lock, even on the free level', async () => {
  mockGate = { tier: 'free', demo: true };
  await render(FeaturesScreen);
  expect(root!.container.queryAll((n) => typeof n.props.testID === 'string' && n.props.testID.endsWith('-locked'))).toHaveLength(0);
  expect(text()).not.toMatch(/Niveau (Basic|Max)/);
});

test('a box without Wi-Fi access control: no automation group', async () => {
  mockAutoReject.mockResolvedValue(null);
  await render(FeaturesScreen);
  expect(all('features-group-automation')).toHaveLength(0);
  expect(all('features-group-remote').length).toBeGreaterThan(0);
});

test('Réglages: « Politique de confidentialité » opens the hub’s policy; the demo the public one (#37)', async () => {
  const { Linking } = jest.requireActual('react-native');
  const open = jest.spyOn(Linking, 'openURL').mockResolvedValue(true);
  mockGate = { tier: 'free', demo: true };
  await render(Settings);
  expect(text()).toContain('Politique de confidentialité');
  await press('settings-privacy');
  expect(open).toHaveBeenLastCalledWith('https://hub.kergui.click/licensing/v1/privacy');

  await act(async () => root?.unmount());
  mockGate = { tier: 'basic', demo: false };
  await render(Settings);
  await press('settings-privacy');
  expect(open).toHaveBeenLastCalledWith('https://hub.test/licensing/v1/privacy');
  open.mockRestore();
});
