import { INBOX_MAX, unreadCount, useInbox } from '../alerts/inbox';
import { entryOf } from '../alerts/inboxFeed';
import { t } from '../i18n';

// The notifications' own space (owner request, 2026-11-23): alerts are kept
// on the phone, newest first, once each, and survive a restart.

const mockStore = new Map<string, string>();
jest.mock('expo-secure-store', () => ({
  getItemAsync: async (k: string) => mockStore.get(k) ?? null,
  setItemAsync: async (k: string, v: string) => void mockStore.set(k, v),
  deleteItemAsync: async (k: string) => void mockStore.delete(k),
}));

const MIN = 60_000;
const arrival = (mac: string, at: number, id = `arrival:${mac}:${at}`) => ({ id, kind: 'arrival' as const, mac, name: mac, at });

beforeEach(() => {
  mockStore.clear();
  useInbox.setState({ items: [], loaded: true });
});

test('newest first, unread, MACs lower-cased', () => {
  useInbox.getState().add([arrival('AA:01', 1 * MIN), arrival('aa:02', 30 * MIN)]);
  const items = useInbox.getState().items;
  expect(items.map((e) => e.mac)).toEqual(['aa:02', 'aa:01']);
  expect(unreadCount(items)).toBe(2);
});

test('the same alert from the app and from the shade is kept once', () => {
  useInbox.getState().add([arrival('aa:01', 0)]);
  useInbox.getState().add([arrival('AA:01', 2 * MIN, 'notification-id')]);
  expect(useInbox.getState().items).toHaveLength(1);
  // Later, the same device arriving again is a new alert.
  useInbox.getState().add([arrival('aa:01', 60 * MIN)]);
  expect(useInbox.getState().items).toHaveLength(2);
});

test('keeps the newest INBOX_MAX', () => {
  useInbox.getState().add(Array.from({ length: INBOX_MAX + 5 }, (_, i) => arrival(`aa:${i}`, i * 20 * MIN)));
  const items = useInbox.getState().items;
  expect(items).toHaveLength(INBOX_MAX);
  expect(items[0].mac).toBe(`aa:${INBOX_MAX + 4}`);
});

test('read, removed and cleared, and stored across restarts', async () => {
  useInbox.getState().add([arrival('aa:01', 0), arrival('aa:02', 30 * MIN)]);
  useInbox.getState().markAllRead();
  expect(unreadCount(useInbox.getState().items)).toBe(0);
  useInbox.getState().remove(useInbox.getState().items[0].id);

  useInbox.setState({ items: [], loaded: false });
  await useInbox.getState().load();
  expect(useInbox.getState().items.map((e) => [e.mac, e.read])).toEqual([['aa:01', true]]);

  useInbox.getState().clear();
  useInbox.setState({ items: [], loaded: false });
  await useInbox.getState().load();
  expect(useInbox.getState().items).toEqual([]);
});

test('an alert raised before the stored inbox is read does not erase it', async () => {
  useInbox.getState().add([arrival('aa:01', 0)]);
  useInbox.setState({ items: [], loaded: false });
  useInbox.getState().add([arrival('aa:02', 30 * MIN)]);
  await useInbox.getState().load();
  expect(useInbox.getState().items.map((e) => e.mac)).toEqual(['aa:02', 'aa:01']);
  useInbox.setState({ items: [], loaded: false });
  await useInbox.getState().load();
  expect(useInbox.getState().items).toHaveLength(2);
});

test('a device notification becomes an entry; others are skipped', () => {
  const note = (title: string, data: Record<string, unknown>) =>
    ({ date: 1000, request: { identifier: 'n1', content: { title, body: 'corps', data } } }) as never;
  expect(entryOf(note(t('alerts.title'), { mac: 'AA:01' }))).toMatchObject({ id: 'n1', kind: 'arrival', mac: 'AA:01', text: 'corps', at: 1000 });
  expect(entryOf(note(t('alerts.refusedTitle'), { mac: 'aa:02' }))?.kind).toBe('refused');
  expect(entryOf(note('Kergui surveille', {}))).toBeNull();
});

test('an announcement (#80) becomes a message entry, keyed by its message', () => {
  const note = { date: 5000, request: { identifier: 'n9', content: { title: 'Coupure', body: 'Ce soir.', data: { kind: 'announcement', messageId: 'M1', title: 'Coupure', body: 'Ce soir.' } } } } as never;
  expect(entryOf(note)).toEqual({ id: 'message:M1', kind: 'message', mac: '', name: 'Coupure', text: 'Ce soir.', at: 5000 });
  // Two messages within minutes are two entries (no device to dedupe on).
  useInbox.getState().add([entryOf(note)!, { id: 'message:M2', kind: 'message', mac: '', name: 'Autre', at: 6000 }]);
  expect(useInbox.getState().items).toHaveLength(2);
});
