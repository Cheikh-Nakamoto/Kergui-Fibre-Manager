/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Réglages > Alertes WhatsApp (issue #49): the opt-in opens WhatsApp on the
// hub's wa.me link, the provider's notice, if any, is visible, and the
// owner's « Répondre 1 pour bloquer » switch delegates or revokes the hub
// key after a confirmation. The demo plays it in memory.
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
const mockOpenURL = jest.fn();
jest.mock('expo-linking', () => ({ openURL: (u: string) => mockOpenURL(u) }));
jest.mock('../core/client', () => ({ api: {}, core: {} }));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));
const mockLoad = jest.fn();
const mockInvite = jest.fn();
const mockSetHubKey = jest.fn();
jest.mock('../features/whatsapp', () => ({
  ...jest.requireActual('../features/whatsapp'),
  loadAlertChannels: () => mockLoad(),
  whatsapp: { invite: () => mockInvite(), setHubKey: (on: boolean) => mockSetHubKey(on) },
}));
const mockConfirm = jest.fn();
jest.mock('../ui/confirm', () => ({ confirm: (m: string) => mockConfirm(m) }));

import { router } from 'expo-router';

import WhatsAppScreen from '../app/whatsapp';
import { useDemoWhatsApp } from '../features/whatsapp';
import { useAccount } from '../licensing/account';
import { useSession } from '../session/store';

const WHATSMEOW = { name: 'whatsmeow', notice: '' };
const view = (over: object = {}) => ({ provider: WHATSMEOW, linked: false, linkedCount: 0, canLink: true, hubKey: { state: 'off', keyId: 'k-hub' }, ...over });

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
  const node = all(id).find((n) => typeof n.props.onClick === 'function' || typeof n.props.onPress === 'function');
  if (!node) throw new Error(`no ${id}`);
  const ev = { nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined };
  await act(async () => void (node.props.onClick ?? node.props.onPress)(ev));
  await settle();
};
const toggle = async (id: string, value: boolean) => {
  const node = all(id).find((n) => typeof n.props.onChange === 'function');
  if (!node) throw new Error(`no switch ${id}`);
  await act(async () => void node.props.onChange({ nativeEvent: { value } }));
  await settle();
};
const switchValue = (id: string) => all(id).find((n) => typeof n.props.onChange === 'function')?.props.value;

async function render() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <PaperProvider settings={{ icon: () => null }}>
          <WhatsAppScreen />
        </PaperProvider>
      </QueryClientProvider>,
    );
  });
  await settle();
}

beforeEach(() => {
  mockLoad.mockReset().mockResolvedValue(view());
  mockInvite.mockReset().mockResolvedValue({
    waLink: 'https://wa.me/221700000000?text=KERGUI%20AB3DEFGH',
    qrPayload: 'x',
    number: '+221700000000',
    expiresAt: 'x',
    provider: WHATSMEOW,
  });
  mockSetHubKey.mockReset().mockResolvedValue(undefined);
  mockConfirm.mockReset().mockResolvedValue(true);
  mockOpenURL.mockReset().mockResolvedValue(true);
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
  useAccount.setState({ ready: true, account: { accountId: 'acc_1', phone: '+221771234567', sessionToken: 'kgs_1', expiresAt: 'x' } });
  useDemoWhatsApp.setState({ linked: false, hubKey: false, message: '' });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
});

test('the opt-in opens WhatsApp on the hub’s link; whatsmeow has no notice to show', async () => {
  await render();
  expect(all('whatsapp-attribution')).toHaveLength(0);
  expect(text()).toContain('Votre numéro ne reçoit pas encore les alertes');
  await press('whatsapp-optin');
  expect(mockOpenURL).toHaveBeenCalledWith('https://wa.me/221700000000?text=KERGUI%20AB3DEFGH');
  expect(text()).toContain('envoyez le message tel quel');
});

test('without WhatsApp on the phone, the message and the number are given to send by hand', async () => {
  mockOpenURL.mockRejectedValue(new Error('no app'));
  await render();
  await press('whatsapp-optin');
  expect(text()).toContain('Envoyez « KERGUI AB3DEFGH » au +221700000000');
});

test('a viewer cannot link a number; a console hub says it sends nothing', async () => {
  mockLoad.mockResolvedValue(view({ canLink: false, provider: { name: 'console' }, hubKey: { state: 'not_owner' } }));
  await render();
  expect(all('whatsapp-optin')).toHaveLength(0);
  expect(text()).toContain('Seuls le propriétaire et les administrateurs');
  expect(text()).toContain('n’envoie pas encore de messages WhatsApp');
  expect(switchValue('whatsapp-hub-key')).toBe(false);
  expect(text()).toContain('Seul le téléphone du propriétaire');
});

test('no relay yet: « Installer un relais » goes there, no path to walk (#124)', async () => {
  mockLoad.mockResolvedValue(view({ hubKey: { state: 'no_relay' } }));
  await render();
  expect(text()).toContain('Il faut d’abord un relais à la maison.');
  expect(text()).not.toMatch(/Réglages|>/);
  (router.push as jest.Mock).mockClear();
  await press('whatsapp-install-relay');
  expect(router.push).toHaveBeenCalledWith('/relay');
});

test('the owner switches « 1 pour bloquer » on after confirming, and off', async () => {
  await render();
  mockConfirm.mockResolvedValueOnce(false);
  await toggle('whatsapp-hub-key', true);
  expect(mockSetHubKey).not.toHaveBeenCalled();
  await toggle('whatsapp-hub-key', true);
  expect(mockConfirm).toHaveBeenLastCalledWith(expect.stringContaining('répondez « 1 »'));
  expect(mockSetHubKey).toHaveBeenCalledWith(true);

  mockLoad.mockResolvedValue(view({ hubKey: { state: 'on', keyId: 'k-hub' } }));
  await render();
  expect(switchValue('whatsapp-hub-key')).toBe(true);
  mockConfirm.mockClear();
  await toggle('whatsapp-hub-key', false);
  expect(mockConfirm).not.toHaveBeenCalled();
  expect(mockSetHubKey).toHaveBeenLastCalledWith(false);
});

test('the demo shows the message to send and switches in memory', async () => {
  useSession.setState({ session: { mode: 'demo' } as never });
  await render();
  expect(all('whatsapp-attribution')).toHaveLength(0);
  await press('whatsapp-optin');
  expect(mockInvite).not.toHaveBeenCalled();
  expect(mockOpenURL).not.toHaveBeenCalled();
  expect(texts(all('whatsapp-demo-message')[0]).join('\n')).toContain('KERGUI DEMO7K2P');
  expect(text()).toContain('Votre numéro reçoit les alertes');
  await toggle('whatsapp-hub-key', true);
  expect(useDemoWhatsApp.getState().hubKey).toBe(true);
  expect(switchValue('whatsapp-hub-key')).toBe(true);
});
