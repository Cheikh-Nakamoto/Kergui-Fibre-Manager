/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Réglages say what the box is (#124, audit L3): « Box : ZTE F6600P », the
// model discovery found. Its address, its certificate's fingerprint and the
// app's version wait, closed, under « Détails techniques ».
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
const mockDiscover = jest.fn();
jest.mock('../core/client', () => ({ api: { discover: () => mockDiscover() }, core: { version: () => 'v9' }, ApiError: class {} }));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));
jest.mock('../ui/ImportBackup', () => ({ ImportBackup: () => null }));
jest.mock('../ui/HubBackup', () => ({ HubBackup: () => null }));
jest.mock('../ui/BoxPasswordSetting', () => ({ BoxPasswordSetting: () => null }));
jest.mock('../licensing/useGate', () => ({ useGate: () => ({ tier: 'basic', demo: false, can: () => true, guard: jest.fn() }) }));

import Settings from '../app/(tabs)/settings';
import { formatFingerprint } from '../lib/format';
import { useSession } from '../session/store';
import { fontStyles } from '../ui/theme';

const PIN = 'ab:cd:ef:01:23:45:67:89';
const BOX = 'https://192.168.1.1';

function texts(node: TestInstance): string[] {
  const out: string[] = [];
  for (const c of node.children) out.push(...(typeof c === 'string' ? [c] : texts(c)));
  return out;
}

let root: Root | null = null;
const settle = async () => {
  for (let i = 0; i < 10; i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 5))));
};
const all = (id: string) => root!.container.queryAll((n) => n.props.testID === id);
const text = () => texts(root!.container).join('\n');
const press = async (id: string) => {
  let node: TestInstance | null | undefined = all(id).find((n) => typeof n.props.onClick === 'function' || typeof n.props.onPress === 'function') ?? all(id)[0];
  while (node && typeof node.props.onPress !== 'function' && typeof node.props.onClick !== 'function') node = node.parent;
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
          <Settings />
        </PaperProvider>
      </QueryClientProvider>,
    );
  });
  await settle();
}

beforeEach(() => {
  mockDiscover.mockReset().mockResolvedValue({ Vendor: 'ZTE', Model: 'F6600P', Reachable: true });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
});

test('the box by its model; the address, the fingerprint and the version folded away', async () => {
  useSession.setState({ session: { mode: 'router', routerUrl: BOX, pin: PIN, model: 'ZTE F6600P' } });
  await render();
  expect(text()).toContain('Box : ZTE F6600P');
  // Closed: none of the technical is on screen.
  expect(all('settings-technical').length).toBeGreaterThan(0);
  expect(text()).toContain('Détails techniques');
  expect(text()).not.toContain(BOX);
  expect(text()).not.toContain(formatFingerprint(PIN));
  expect(text()).not.toMatch(/moteur/);
  // The model came from the session: discovery is not asked again.
  expect(mockDiscover).not.toHaveBeenCalled();

  await press('settings-technical');
  expect(text()).toContain(BOX);
  expect(text()).toContain(formatFingerprint(PIN));
  expect(text()).toContain('Version 0.2.0 · moteur v9');
  // The fingerprint in the site's monospace (#128).
  const fp = root!.container.queryAll((n) => texts(n).join('') === formatFingerprint(PIN));
  expect(fp.some((n) => JSON.stringify(n.props.style ?? null).includes(String(fontStyles.mono.fontFamily)))).toBe(true);
});

test('a session from before the model was kept: discovery names the box', async () => {
  useSession.setState({ session: { mode: 'router', routerUrl: BOX, pin: PIN } });
  await render();
  expect(mockDiscover).toHaveBeenCalled();
  expect(text()).toContain('Box : ZTE F6600P');
});

test('the box does not answer and the model is unknown: « Box », nothing made up', async () => {
  mockDiscover.mockRejectedValue(new Error('router unreachable'));
  useSession.setState({ session: { mode: 'router', routerUrl: BOX, pin: PIN } });
  await render();
  expect(text()).not.toContain('Box :');
  expect(texts(all('settings-box-model')[0]).join('')).toBe('Box');
});

test('the demo folds its version away too', async () => {
  useSession.setState({ session: { mode: 'demo' } });
  await render();
  expect(text()).toContain('Mode démo');
  expect(text()).not.toMatch(/moteur/);
  await press('settings-technical');
  expect(text()).toContain('Version 0.2.0 · moteur v9');
  expect(mockDiscover).not.toHaveBeenCalled();
});
