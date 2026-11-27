/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Changing a Wi-Fi password (#123, audit H8): the new password is shown
// while it is typed, a character the box refuses is said before, the
// warning says every device of the house must type it again, and after it a
// screen shows it big, its Wi-Fi QR code and « Envoyer sur WhatsApp ». That
// screen only shows what was just typed: leaving it forgets it, and the
// journal never sees it.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Alert, Share, StyleSheet, Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() },
  Stack: { Screen: () => null },
  useLocalSearchParams: () => ({}),
}));
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('expo-linking', () => ({ openURL: jest.fn(() => Promise.resolve()) }));
jest.mock('../alerts/alerts', () => ({ checkNewDevices: async () => 0, enforceAutoReject: async () => 0 }));
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));

import * as Linking from 'expo-linking';

import KerguiCore from '../../modules/kergui-core';
import WiFi from '../app/(tabs)/wifi';
import { useEntitlement } from '../licensing/entitlement';
import { useSession } from '../session/store';
import { qrRows } from '../ui/QrCode';
import { fontStyles, qr } from '../ui/theme';

const AP1 = 'DEV.WIFI.AP1';
const AP5 = 'DEV.WIFI.AP5';
const NEW = 'Dakar;2026:ok';
const request = KerguiCore.request as jest.Mock;
const note = KerguiCore.note as jest.Mock;
const openURL = Linking.openURL as jest.Mock;

const WARNING = 'Tous les appareils de la maison vont se déconnecter. Il faudra taper le nouveau mot de passe sur chacun.';

type SSID = { id: string; alias: string; band: string; name: string; enabled: boolean; hidden: boolean; security: string };

/** The core: two networks, and PATCH answers ok unless refused. */
function box(refuse = false) {
  const ssids: SSID[] = [
    { id: AP1, alias: 'SSID1', band: '2.4GHz', name: 'MonWiFi-2G', enabled: true, hidden: false, security: 'WPA2-PSK-AES' },
    { id: AP5, alias: 'SSID5', band: '5GHz', name: 'MonWiFi-5G', enabled: true, hidden: true, security: 'WPA2-PSK-AES' },
  ];
  request.mockImplementation(async (method: string, path: string) => {
    if (method === 'GET' && path === '/api/wifi/ssids') return { status: 200, body: JSON.stringify(ssids) };
    const m = /^\/api\/wifi\/ssids\/([^/]+)$/.exec(path);
    if (method === 'PATCH' && m) {
      if (refuse) return { status: 502, body: '{"error":"the router refused the new password","code":"router"}' };
      return { status: 200, body: JSON.stringify(ssids.find((x) => x.id === decodeURIComponent(m[1]))) };
    }
    return { status: 404, body: '{"error":"not found"}' };
  });
}

function texts(node: TestInstance): string[] {
  const out: string[] = [];
  for (const c of node.children) {
    if (typeof c === 'string') out.push(c);
    else out.push(...texts(c));
  }
  return out;
}

const iconAsText = ({ name }: { name: string }) => <Text>{`icon:${name}`}</Text>;

let root: Root | null = null;
let qc: QueryClient | null = null;

async function settle(until: () => boolean = () => false) {
  for (let i = 0; i < 50 && !until(); i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 10))));
}

async function render() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false, gcTime: 0 } } });
  qc = client;
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={client}>
        <PaperProvider settings={{ icon: iconAsText }}>
          <WiFi />
        </PaperProvider>
      </QueryClientProvider>,
    );
  });
  await settle(() => !!byId(root!, `wifi-ssid-${AP5}`));
  return root;
}

const all = (r: Root, id: string) => r.container.queryAll((n) => n.props.testID === id);
const byId = (r: Root, id: string) => all(r, id)[0];
const withProp = (r: Root, id: string, prop: string) => all(r, id).find((n) => n.props[prop] !== undefined);
const text = (r: Root) => texts(r.container).join('\n');
const press = async (r: Root, id: string) => {
  const node = byId(r, id);
  if (!node) throw new Error(`no ${id}`);
  await act(async () => void node.props.onClick({ nativeEvent: {}, persist: () => undefined, stopPropagation: () => undefined }));
};
const type = async (r: Root, id: string, value: string) => {
  const node = all(r, id).find((n) => typeof n.props.onChangeText === 'function');
  if (!node) throw new Error(`no input ${id}`);
  await act(async () => void node.props.onChangeText(value));
};
const disabled = (r: Root, id: string) => all(r, id).some((n) => n.props.accessibilityState?.disabled === true);
const patches = () =>
  request.mock.calls.filter(([m]) => m === 'PATCH').map(([, p, b]) => [decodeURIComponent(p as string), JSON.parse(b as string)]);

let alert: jest.SpyInstance;
let share: jest.SpyInstance;
const answer = (yes: boolean) =>
  alert.mockImplementation((_t, _m, buttons?: { onPress?: () => void }[]) => buttons?.[yes ? 1 : 0]?.onPress?.());

beforeEach(() => {
  request.mockReset();
  note.mockReset();
  openURL.mockReset().mockResolvedValue(undefined);
  alert = jest.spyOn(Alert, 'alert').mockImplementation(() => undefined);
  share = jest.spyOn(Share, 'share').mockResolvedValue({ action: 'sharedAction', activityType: undefined });
  useSession.setState({ session: { mode: 'router', routerUrl: 'http://192.168.1.1', pin: '' } });
  const now = Math.floor(Date.now() / 1000);
  useEntitlement.setState({
    boxId: 'ZTEG00000001',
    token: 'x.y',
    claims: { account: 'acc_1', boxId: 'ZTEG00000001', tier: 'max', expiresAt: now + 86400, issuedAt: now - 86400, keyId: 'k' },
  });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  qc?.clear();
  root = qc = null;
  alert.mockRestore();
  share.mockRestore();
});

/** Opens the editor of AP5, types the password, saves, and says yes or no. */
async function change(r: Root, password = NEW, yes = true) {
  await press(r, `wifi-ssid-password-open-${AP5}`);
  await type(r, `wifi-ssid-password-${AP5}`, password);
  answer(yes);
  await press(r, `wifi-ssid-password-save-${AP5}`);
}

describe('typing it', () => {
  test('the new password is shown while it is typed; the eye hides it', async () => {
    box();
    const r = await render();
    await press(r, `wifi-ssid-password-open-${AP5}`);
    await type(r, `wifi-ssid-password-${AP5}`, NEW);
    const secure = () => withProp(r, `wifi-ssid-password-${AP5}`, 'secureTextEntry')?.props.secureTextEntry;
    expect(secure()).toBe(false);
    await press(r, `wifi-ssid-password-${AP5}-eye`);
    expect(secure()).toBe(true);
    await press(r, `wifi-ssid-password-${AP5}-eye`);
    expect(secure()).toBe(false);
  });

  test('an accent or an emoji is said before saving, and nothing can be sent', async () => {
    box();
    const r = await render();
    await press(r, `wifi-ssid-password-open-${AP5}`);
    await type(r, `wifi-ssid-password-${AP5}`, 'Sénégal-2026');
    expect(byId(r, `wifi-ssid-password-rule-${AP5}`)).toBeDefined();
    expect(text(r)).toContain('Pas de lettre accentuée (é, è, ç…) ni d’emoji : la box les refuse.');
    expect(disabled(r, `wifi-ssid-password-save-${AP5}`)).toBe(true);
    await press(r, `wifi-ssid-password-save-${AP5}`);
    expect(alert).not.toHaveBeenCalled();
    expect(patches()).toEqual([]);
  });

  test('more than 63 characters is said too; 8 to 63 ASCII can be saved', async () => {
    box();
    const r = await render();
    await press(r, `wifi-ssid-password-open-${AP5}`);
    await type(r, `wifi-ssid-password-${AP5}`, 'x'.repeat(64));
    expect(text(r)).toContain('63 caractères au plus.');
    expect(disabled(r, `wifi-ssid-password-save-${AP5}`)).toBe(true);
    await type(r, `wifi-ssid-password-${AP5}`, 'court');
    // Too short: the label says 8 at least; no extra line while typing.
    expect(byId(r, `wifi-ssid-password-rule-${AP5}`)).toBeUndefined();
    expect(disabled(r, `wifi-ssid-password-save-${AP5}`)).toBe(true);
    await type(r, `wifi-ssid-password-${AP5}`, NEW);
    expect(byId(r, `wifi-ssid-password-rule-${AP5}`)).toBeUndefined();
    expect(disabled(r, `wifi-ssid-password-save-${AP5}`)).toBe(false);
  });
});

describe('the warning', () => {
  test('the title names the action, the line says every device of the house, the button is red', async () => {
    box();
    const r = await render();
    await change(r, NEW, false);
    expect(alert).toHaveBeenCalledTimes(1);
    const [title, message, buttons] = alert.mock.calls[0];
    expect(title).toBe('Changer le mot de passe du réseau « MonWiFi-5G » ?');
    expect(message).toBe(WARNING);
    expect(buttons[1]).toMatchObject({ text: 'Oui, changer', style: 'destructive' });
  });

  test('« Annuler » sends nothing and shows no new password', async () => {
    box();
    const r = await render();
    await change(r, NEW, false);
    await settle();
    expect(patches()).toEqual([]);
    expect(byId(r, 'wifi-password-done')).toBeUndefined();
    // The editor stays open with what was typed.
    expect(byId(r, `wifi-ssid-password-${AP5}`)).toBeDefined();
  });
});

describe('after it', () => {
  test('a screen shows the new password big, in monospace, with its Wi-Fi QR code', async () => {
    box();
    const r = await render();
    await change(r);
    await settle(() => !!byId(r, 'wifi-password-done'));
    expect(patches()).toEqual([[`/api/wifi/ssids/${AP5}`, { password: NEW }]]);
    const big = byId(r, 'wifi-password-done-password');
    expect(texts(big!).join('')).toBe(NEW);
    // Large type (Paper's displaySmall), the password itself in monospace.
    expect(StyleSheet.flatten(big!.props.style).fontSize).toBeGreaterThanOrEqual(32);
    const mono = big!.queryAll((n) => StyleSheet.flatten(n.props.style)?.fontFamily === fontStyles.mono.fontFamily);
    expect(mono.length).toBeGreaterThan(0);
    expect(text(r)).toContain('MonWiFi-5G');
    // A hidden network: the phone that reads the code looks for it.
    // The code drawn is the one of the standard Wi-Fi text, module by module.
    const qrNode = byId(r, 'wifi-password-done-qr');
    const drawn = (qrNode!.children as TestInstance[]).map((row) =>
      (row.children as TestInstance[]).map((cell) => StyleSheet.flatten(cell.props.style).backgroundColor === qr.dark),
    );
    expect(drawn).toEqual(qrRows('WIFI:T:WPA;S:MonWiFi-5G;P:Dakar\\;2026\\:ok;H:true;;'));
    expect(qrNode!.props.accessibilityLabel).toBe('Code à lire pour se connecter au réseau « MonWiFi-5G »');
    expect(byId(r, 'wifi-password-done-whatsapp')).toBeDefined();
    // The editor is closed behind it.
    expect(byId(r, `wifi-ssid-password-${AP5}`)).toBeUndefined();
  });

  test('« Envoyer sur WhatsApp » opens WhatsApp with the network and the password, in plain French', async () => {
    box();
    const r = await render();
    await change(r);
    await settle(() => !!byId(r, 'wifi-password-done'));
    await press(r, 'wifi-password-done-whatsapp');
    const message = `Nouveau mot de passe du Wi-Fi de la maison\nRéseau : MonWiFi-5G\nMot de passe : ${NEW}`;
    // WhatsApp itself, never a web page that would see the password.
    expect(openURL).toHaveBeenCalledWith(`whatsapp://send?text=${encodeURIComponent(message)}`);
    expect(share).not.toHaveBeenCalled();
  });

  test('without WhatsApp, the phone’s share sheet sends the same message', async () => {
    box();
    const r = await render();
    await change(r);
    await settle(() => !!byId(r, 'wifi-password-done'));
    openURL.mockRejectedValueOnce(new Error('no app'));
    await press(r, 'wifi-password-done-whatsapp');
    await settle(() => share.mock.calls.length > 0);
    expect(share).toHaveBeenCalledWith({ message: `Nouveau mot de passe du Wi-Fi de la maison\nRéseau : MonWiFi-5G\nMot de passe : ${NEW}` });
  });

  test.each([
    ['« Terminé »', 'close'],
    ['the back button', 'back'],
  ])('leaving it by %s forgets the password; the card says it changed', async (_label, how) => {
    box();
    const r = await render();
    await change(r);
    await settle(() => !!byId(r, 'wifi-password-done'));
    if (how === 'close') await press(r, 'wifi-password-done-close');
    else {
      const modal = withProp(r, 'wifi-password-done-modal', 'onRequestClose');
      await act(async () => void modal!.props.onRequestClose());
    }
    expect(byId(r, 'wifi-password-done')).toBeUndefined();
    expect(text(r)).not.toContain(NEW);
    expect(byId(r, `wifi-ssid-password-done-${AP5}`)).toBeDefined();
    expect(text(r)).toContain('Mot de passe changé.');
  });

  test('the password never reaches the journal', async () => {
    box();
    const r = await render();
    await change(r);
    await settle(() => !!byId(r, 'wifi-password-done'));
    await press(r, 'wifi-password-done-whatsapp');
    await press(r, 'wifi-password-done-close');
    for (const call of note.mock.calls) expect(JSON.stringify(call)).not.toContain('Dakar');
  });

  test('a refusal from the box shows no new password', async () => {
    box(true);
    const r = await render();
    await change(r);
    await settle(() => patches().length > 0);
    await settle();
    expect(byId(r, 'wifi-password-done')).toBeUndefined();
    expect(text(r)).not.toContain('Mot de passe changé.');
  });
});
