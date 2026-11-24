/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Self-update of the website APK (#74): the hub offers a published version;
// on Wi-Fi it is downloaded in the background and offered ready, on mobile
// data offered with its size; « Plus tard » until the next launch; a
// blocking screen below the minimum; a file whose SHA-256 is wrong is never
// installed; the « install unknown apps » permission explained, then the
// way out when refused; nothing at all for an app installed from Play.
// Only the hub's fetch, the network, the files and the native installer are mocked.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { AppState, Linking, Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() },
  Stack: { Screen: () => null },
  useLocalSearchParams: () => ({}),
}));
const mockStore = new Map<string, string>();
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async (k: string) => mockStore.get(k) ?? null,
  setItemAsync: async (k: string, v: string) => void mockStore.set(k, v),
  deleteItemAsync: async (k: string) => void mockStore.delete(k),
}));
jest.mock('expo-crypto', () => ({ randomUUID: () => '0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0' }));
/** The cache directory: uri → size. */
const mockFiles = new Map<string, number>();
const mockDownload = jest.fn();
jest.mock('expo-file-system', () => {
  class File {
    uri: string;
    constructor(_dir: unknown, name: string) {
      this.uri = `file:///cache/${name}`;
    }
    get exists() {
      return mockFiles.has(this.uri);
    }
    get size() {
      return mockFiles.get(this.uri) ?? 0;
    }
    delete() {
      mockFiles.delete(this.uri);
    }
    static downloadFileAsync = (...a: unknown[]) => mockDownload(...a);
  }
  return { File, Paths: { cache: 'cache' } };
});
jest.mock('../licensing/config', () => ({ config: { hubUrl: 'https://hub.test', publicKeys: [] } }));
jest.mock('../alerts/alerts', () => ({ checkNewDevices: async () => 0, enforceAutoReject: async () => 0 }));

import KerguiCore from '../../modules/kergui-core';
import { useInbox } from '../alerts/inbox';
import Notifications from '../app/notifications';
import { UpdateHost } from '../ui/UpdateHost';
import { useUpdate } from '../update/update';
import { useSelfUpdate } from '../update/useSelfUpdate';

const SIZE = 31_400_000;
const SHA = 'ab'.repeat(32);
const APK = 'file:///cache/kergui-update.apk';
const RELEASE = {
  versionName: '0.3.0',
  build: 42,
  url: 'https://github.com/kergui/releases/download/kergui-0.3.0-build.42.apk',
  sha256: SHA,
  size: SIZE,
  notes: 'Les appareils se chargent plus vite.',
  publishedAt: '2026-11-23T10:00:00Z',
};

// The native seam (modules/kergui-core, SelfUpdate.kt).
const native = KerguiCore as unknown as Record<string, jest.Mock>;
let installer = 'com.google.android.packageinstaller';
let unmetered = true;
let canInstall = true;
let fileSha = SHA;
let hubAnswer: unknown = { update: RELEASE, minimumBuild: 1, required: false };
const fetchMock = jest.fn();

let root: Root | null = null;
let appStateListener: ((s: string) => void) | null = null;

function texts(node: TestInstance): string[] {
  const out: string[] = [];
  for (const c of node.children) {
    if (typeof c === 'string') out.push(c);
    else out.push(...texts(c));
  }
  return out;
}
const text = (r: Root) => texts(r.container).join(' ');
const byId = (r: Root, id: string) => r.container.queryAll((n) => n.props.testID === id)[0];
async function settle(until: () => boolean = () => false) {
  for (let i = 0; i < 30 && !until(); i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 5))));
}
async function press(r: Root, id: string) {
  const node = r.container.queryAll((n) => n.props.testID === id && typeof (n.props.onPress ?? n.props.onClick) === 'function')[0];
  if (!node) throw new Error(`no pressable ${id} in: ${text(r)}`);
  await act(async () => (node.props.onPress ?? node.props.onClick)());
  await settle();
}

/** The app's root: the hook that checks, and the banner / blocking screen. */
function App({ children }: { children?: React.ReactNode }) {
  useSelfUpdate(true);
  return (
    <>
      {children}
      <UpdateHost />
    </>
  );
}

const iconAsText = ({ name }: { name: string }) => <Text>{`icon:${name}`}</Text>;

/** A launch of the app: the in-memory state starts over, the cache and SecureStore stay. */
async function launch(children?: React.ReactNode) {
  if (root) await act(async () => root!.unmount());
  useUpdate.setState({ phase: 'none', release: null, required: false, dismissed: false, byTap: false, failure: null, permission: null, nextCheckAt: 0 });
  root = createRoot();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={client}>
        <PaperProvider settings={{ icon: iconAsText }}>
          <App>{children}</App>
        </PaperProvider>
      </QueryClientProvider>,
    );
  });
  await settle();
  return root;
}

/** The app comes back to the front. */
async function foreground() {
  await act(async () => appStateListener?.('active'));
  await settle();
}

const checks = () => fetchMock.mock.calls.filter(([url]) => String(url).endsWith('/releases/v1/check'));

beforeEach(() => {
  mockStore.clear();
  mockFiles.clear();
  installer = 'com.google.android.packageinstaller';
  unmetered = true;
  canInstall = true;
  fileSha = SHA;
  hubAnswer = { update: RELEASE, minimumBuild: 1, required: false };
  useInbox.setState({ items: [], loaded: true });

  native.selfUpdateInfo = jest.fn(() => ({ allowed: true, installer, build: 41 }));
  native.onUnmeteredNetwork = jest.fn(() => unmetered);
  native.canInstallPackages = jest.fn(() => canInstall);
  native.openInstallSettings = jest.fn(async () => undefined);
  native.sha256File = jest.fn(async () => fileSha);
  native.installApk = jest.fn(async () => undefined);
  mockDownload.mockReset().mockImplementation(async (_url: string, file: { uri: string }) => {
    mockFiles.set(file.uri, SIZE);
    return file;
  });
  fetchMock.mockReset().mockImplementation(async () => ({
    status: 200,
    text: async () => JSON.stringify(hubAnswer),
    headers: { get: () => null },
  }));
  global.fetch = fetchMock as unknown as typeof fetch;
  appStateListener = null;
  jest.spyOn(AppState, 'addEventListener').mockImplementation((_t, fn) => {
    appStateListener = fn as (s: string) => void;
    return { remove: () => undefined } as ReturnType<typeof AppState.addEventListener>;
  });
  jest.spyOn(Linking, 'openURL').mockResolvedValue(undefined);
});

afterEach(async () => {
  if (root) await act(async () => root!.unmount());
  root = null;
});

test('the check sends the build and an anonymous installation id, kept across launches', async () => {
  await launch();
  await launch();
  const bodies = checks().map(([, init]) => JSON.parse((init as { body: string }).body));
  expect(bodies).toHaveLength(2);
  expect(bodies[0]).toEqual({ platform: 'android', channel: 'website', build: 41, installationId: expect.stringMatching(/^[A-Za-z0-9_-]{8,64}$/) });
  expect(bodies[1].installationId).toBe(bodies[0].installationId);
  expect(checks()[0][1]).toMatchObject({ method: 'POST' });
  expect((checks()[0][1] as { headers: Record<string, string> }).headers.Authorization).toBeUndefined();
});

test('on Wi-Fi the version is downloaded in the background, then offered ready and installed', async () => {
  const r = await launch();
  expect(mockDownload).toHaveBeenCalledWith(RELEASE.url, expect.objectContaining({ uri: APK }), { idempotent: true });
  expect(byId(r, 'update-title-ready')).toBeDefined();
  expect(text(r)).toContain('Mise à jour prête');
  expect(text(r)).toContain('Kergui 0.3.0');
  expect(text(r)).toContain(RELEASE.notes);
  // The same offer in the notifications screen (kind « update »).
  expect(useInbox.getState().items).toEqual([expect.objectContaining({ id: 'update:42', kind: 'update', name: '0.3.0', read: false })]);

  await press(r, 'update-install');
  expect(native.sha256File).toHaveBeenLastCalledWith(APK);
  expect(native.installApk).toHaveBeenCalledWith(APK);
});

test('on mobile data the size is offered first, and nothing is downloaded until the tap', async () => {
  unmetered = false;
  const r = await launch();
  expect(mockDownload).not.toHaveBeenCalled();
  expect(byId(r, 'update-title-available')).toBeDefined();
  expect(text(r)).toContain('Mettre à jour (31 Mo)');
  expect(useInbox.getState().items.map((e) => e.id)).toEqual(['update:42']);

  await press(r, 'update-install');
  expect(mockDownload).toHaveBeenCalledTimes(1);
  expect(native.installApk).toHaveBeenCalledWith(APK);
});

test('« Plus tard » hides the offer until the next launch', async () => {
  unmetered = false;
  const r = await launch();
  await press(r, 'update-later');
  expect(byId(r, 'update-banner')).toBeUndefined();
  // Back to the front: still put off (and not checked again within 6 h).
  await foreground();
  expect(byId(r, 'update-banner')).toBeUndefined();
  expect(checks()).toHaveLength(1);

  const again = await launch();
  expect(byId(again, 'update-banner')).toBeDefined();
  expect(text(again)).toContain('Mise à jour disponible');
});

test('below the minimum version a blocking screen says the size and updates on any network', async () => {
  unmetered = false;
  hubAnswer = { update: RELEASE, minimumBuild: 42, required: true };
  const r = await launch();
  expect(byId(r, 'update-required')).toBeDefined();
  expect(byId(r, 'update-later')).toBeUndefined();
  expect(text(r)).toContain('Mise à jour requise');
  expect(text(r)).toContain('Téléchargement de 31 Mo.');
  expect(mockDownload).not.toHaveBeenCalled();

  await press(r, 'update-install');
  expect(mockDownload).toHaveBeenCalledTimes(1);
  expect(native.installApk).toHaveBeenCalledWith(APK);
  expect(byId(r, 'update-required')).toBeDefined();
});

test('a downloaded file whose SHA-256 is wrong is deleted, never installed, and said so', async () => {
  fileSha = 'cd'.repeat(32);
  const r = await launch();
  expect(mockDownload).toHaveBeenCalled();
  expect(byId(r, 'update-failed-corrupt')).toBeDefined();
  expect(mockFiles.has(APK)).toBe(false);
  expect(native.installApk).not.toHaveBeenCalled();
});

test('a file changed after its download is refused at install time', async () => {
  const r = await launch();
  expect(byId(r, 'update-title-ready')).toBeDefined();
  fileSha = 'cd'.repeat(32);
  await press(r, 'update-install');
  expect(native.installApk).not.toHaveBeenCalled();
  expect(byId(r, 'update-failed-corrupt')).toBeDefined();
});

test('a file of the wrong size is refused too', async () => {
  mockDownload.mockImplementation(async (_u: string, file: { uri: string }) => void mockFiles.set(file.uri, SIZE - 1));
  const r = await launch();
  expect(byId(r, 'update-failed-corrupt')).toBeDefined();
  expect(native.sha256File).not.toHaveBeenCalled();
});

test('a failed download says it will be tried again later', async () => {
  mockDownload.mockRejectedValue(new Error('timeout'));
  const r = await launch();
  expect(byId(r, 'update-failed-network')).toBeDefined();
  expect(text(r)).toContain('relancera plus tard');
});

test('the install permission is explained first; refused, the app keeps working and points to the download page', async () => {
  canInstall = false;
  const r = await launch();
  await press(r, 'update-install');
  expect(native.installApk).not.toHaveBeenCalled();
  expect(byId(r, 'update-permission-explain')).toBeDefined();
  expect(text(r)).toContain('Installer des applications inconnues');

  await press(r, 'update-permission-settings');
  expect(native.openInstallSettings).toHaveBeenCalled();
  // Back from the settings page without granting it.
  await foreground();
  expect(byId(r, 'update-permission-refused')).toBeDefined();
  expect(native.installApk).not.toHaveBeenCalled();
  await press(r, 'update-download-page');
  expect(Linking.openURL).toHaveBeenCalledWith('https://kergui.click/telecharger');
  // The offer can still be put off: nothing blocks.
  await press(r, 'update-later');
  expect(byId(r, 'update-banner')).toBeUndefined();
});

test('granted in the settings page, the install carries on on return', async () => {
  canInstall = false;
  const r = await launch();
  await press(r, 'update-install');
  await press(r, 'update-permission-settings');
  canInstall = true;
  await foreground();
  expect(native.installApk).toHaveBeenCalledWith(APK);
});

test('below the minimum, a refused permission keeps the blocking screen', async () => {
  canInstall = false;
  hubAnswer = { update: RELEASE, minimumBuild: 42, required: true };
  const r = await launch();
  await press(r, 'update-install');
  await press(r, 'update-permission-settings');
  await foreground();
  expect(byId(r, 'update-permission-refused')).toBeDefined();
  expect(byId(r, 'update-required')).toBeDefined();
});

test('an app installed from the Play Store is never offered anything', async () => {
  installer = 'com.android.vending';
  const r = await launch();
  expect(checks()).toHaveLength(0);
  expect(mockDownload).not.toHaveBeenCalled();
  expect(byId(r, 'update-banner')).toBeUndefined();
  expect(useInbox.getState().items).toEqual([]);
});

test('the Play bundle (no self-update flag) never asks the hub', async () => {
  native.selfUpdateInfo = jest.fn(() => ({ allowed: false, installer: '', build: 41 }));
  await launch();
  expect(checks()).toHaveLength(0);
});

test('up to date: nothing is offered, and an old offer leaves the notifications', async () => {
  useInbox.getState().add([{ id: 'update:40', kind: 'update', mac: '', name: '0.2.0', at: Date.now() }]);
  hubAnswer = { update: null, minimumBuild: 1, required: false };
  const r = await launch();
  expect(byId(r, 'update-banner')).toBeUndefined();
  expect(useInbox.getState().items).toEqual([]);
});

test('the notifications screen offers « Mettre à jour »', async () => {
  (KerguiCore.request as jest.Mock).mockResolvedValue({ status: 200, body: '[]' });
  const r = await launch(<Notifications />);
  await settle(() => !!byId(r, 'inbox-update-install'));
  expect(byId(r, 'inbox-update:42')).toBeDefined();
  expect(text(r)).toContain('Mise à jour prête');
  await press(r, 'inbox-update-install');
  expect(native.installApk).toHaveBeenCalledWith(APK);
});
