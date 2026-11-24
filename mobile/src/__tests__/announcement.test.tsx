/* eslint-disable import/first -- the mocks must come before the modules they replace */
// An announcement of the Kergui team (#80): the hub pushes it with data
// {kind: "announcement", messageId, title, body}; the app files it in its
// notifications as a « message », once, whether it arrived with the app
// open or was read back from the shade — its title and text, a green badge,
// no device to open.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { Text } from 'react-native';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

const mockReceived: ((n: unknown) => void)[] = [];
const mockPresented = { current: [] as unknown[] };
jest.mock('expo-notifications', () => ({
  addNotificationReceivedListener: (fn: (n: unknown) => void) => {
    mockReceived.push(fn);
    return { remove: () => mockReceived.splice(mockReceived.indexOf(fn), 1) };
  },
  getPresentedNotificationsAsync: async () => mockPresented.current,
}));
const mockPush = jest.fn();
jest.mock('expo-router', () => ({
  router: { push: (...a: unknown[]) => mockPush(...a), replace: jest.fn(), back: jest.fn() },
  Stack: { Screen: () => null },
  useLocalSearchParams: () => ({}),
}));
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('../alerts/alerts', () => ({ checkNewDevices: async () => 0, enforceAutoReject: async () => 0 }));
jest.mock('../licensing/config', () => ({ config: { hubUrl: '', publicKeys: [] } }));

import KerguiCore from '../../modules/kergui-core';
import { useInbox, unreadCount } from '../alerts/inbox';
import { useInboxFeed } from '../alerts/inboxFeed';
import Notifications from '../app/notifications';

// What the hub pushes (internal/hub/messages, deliver).
const MESSAGE_ID = '6f1c2b3a-0000-4000-8000-000000000080';
const announcement = (date: number, identifier = 'expo-n-1') => ({
  date,
  request: {
    identifier,
    content: {
      title: 'Coupure prévue ce soir',
      body: 'Le réseau sera en maintenance de 22 h à minuit.',
      data: {
        kind: 'announcement',
        messageId: MESSAGE_ID,
        title: 'Coupure prévue ce soir',
        body: 'Le réseau sera en maintenance de 22 h à minuit.',
        tenantId: 'T1',
      },
    },
  },
});

function texts(node: TestInstance): string[] {
  const out: string[] = [];
  for (const c of node.children) {
    if (typeof c === 'string') out.push(c);
    else out.push(...texts(c));
  }
  return out;
}
const text = (r: Root) => texts(r.container).join('\n');
const byId = (r: Root, id: string) => r.container.queryAll((n) => n.props.testID === id)[0];
const iconAsText = ({ name }: { name: string }) => <Text>{`icon:${name}`}</Text>;

async function settle(until: () => boolean = () => false) {
  for (let i = 0; i < 50 && !until(); i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 10))));
}

/** The app's root: the feed that files notifications, and the bell's screen. */
function App() {
  useInboxFeed();
  return <Notifications />;
}

let root: Root | null = null;

beforeEach(() => {
  mockReceived.length = 0;
  mockPresented.current = [];
  mockPush.mockReset();
  (KerguiCore.request as jest.Mock).mockReset().mockResolvedValue({ status: 200, body: '[]' });
  useInbox.setState({ items: [], loaded: false });
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
});

async function launch() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  root = createRoot();
  await act(async () => {
    root!.render(
      <QueryClientProvider client={client}>
        <PaperProvider settings={{ icon: iconAsText }}>
          <App />
        </PaperProvider>
      </QueryClientProvider>,
    );
  });
  await settle(() => useInbox.getState().loaded);
  return root;
}

test('an announcement push lands in the notifications screen as a message', async () => {
  const r = await launch();
  expect(byId(r, `inbox-message:${MESSAGE_ID}`)).toBeUndefined();

  // Received while the app is open.
  await act(async () => mockReceived.forEach((fn) => fn(announcement(Date.now()))));
  await settle(() => !!byId(r, `inbox-message:${MESSAGE_ID}`));

  expect(byId(r, `inbox-message:${MESSAGE_ID}`)).toBeDefined();
  const shown = text(r);
  expect(shown).toContain('Coupure prévue ce soir');
  expect(shown).toContain('Le réseau sera en maintenance de 22 h à minuit.');
  expect(shown).toContain('Message de Kergui');
  // A green badge, and nothing about a device.
  expect(shown).toContain('icon:bullhorn-outline');
  expect(shown).not.toContain('Appareil');
  expect(byId(r, 'inbox-approve-')).toBeUndefined();
  const [entry] = useInbox.getState().items;
  expect(entry).toMatchObject({ kind: 'message', mac: '', name: 'Coupure prévue ce soir' });
  // Read once the screen is open.
  expect(unreadCount(useInbox.getState().items)).toBe(0);

  // Pressing it opens no device sheet.
  const row = byId(r, `inbox-message:${MESSAGE_ID}`);
  if (typeof row.props.onPress === 'function') await act(async () => row.props.onPress());
  expect(mockPush).not.toHaveBeenCalled();
});

test('an announcement read back from the shade is filed once', async () => {
  // Still in the shade at launch, and received again: one entry.
  mockPresented.current = [announcement(1_000, 'shade-id')];
  const r = await launch();
  await settle(() => !!byId(r, `inbox-message:${MESSAGE_ID}`));
  await act(async () => mockReceived.forEach((fn) => fn(announcement(2_000, 'other-id'))));
  await settle();
  expect(useInbox.getState().items.filter((e) => e.kind === 'message')).toHaveLength(1);
  // Two different announcements a minute apart are two entries.
  const second = announcement(60_000, 'n-2');
  second.request.content.data.messageId = 'second';
  await act(async () => mockReceived.forEach((fn) => fn(second)));
  await settle();
  expect(useInbox.getState().items.filter((e) => e.kind === 'message')).toHaveLength(2);
});
