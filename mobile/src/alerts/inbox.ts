import * as SecureStore from 'expo-secure-store';
import { create } from 'zustand';

// The notifications' own space (owner request, 2026-11-23): every alert the
// household got — a device nobody approved that joined, a device auto-reject
// refused, the hub's push — is kept here, read from the bell at the top
// right, whether or not the phone's notification shade still shows it.
// Kept on the phone (SecureStore, the only store the app has), the newest
// MAX entries only, so the stored value stays small.

// « update »: a new version of the website APK (#74); its mac is empty, its
// name the version, its id update:<build>. src/update keeps one at most.
// « message »: an announcement from the Kergui team (#80), pushed by the
// hub; its mac is empty, its name the title, its text the message, its id
// message:<messageId>.
export type InboxKind = 'arrival' | 'refused' | 'update' | 'message';

export type InboxEntry = {
  /** The notification's identifier, or kind:mac:time for one raised in the app. */
  id: string;
  kind: InboxKind;
  mac: string;
  name: string;
  /** The notification's text, when it came from the shade (the hub's push, the Android watch). */
  text?: string;
  /** Milliseconds since the epoch. */
  at: number;
  read: boolean;
};

const KEY = 'kergui.inbox';
export const INBOX_MAX = 30;
const DEDUP_MS = 10 * 60 * 1000;

type InboxState = {
  items: InboxEntry[];
  loaded: boolean;
  load: () => Promise<void>;
  add: (entries: Omit<InboxEntry, 'read'>[]) => void;
  markAllRead: () => void;
  remove: (id: string) => void;
  clear: () => void;
};

function save(items: InboxEntry[]) {
  void SecureStore.setItemAsync(KEY, JSON.stringify(items)).catch(() => undefined);
}

export const useInbox = create<InboxState>((set, get) => ({
  items: [],
  loaded: false,
  load: async () => {
    if (get().loaded) return;
    let stored: InboxEntry[] = [];
    try {
      const raw = await SecureStore.getItemAsync(KEY);
      if (raw) stored = (JSON.parse(raw) as InboxEntry[]).filter((e) => e && typeof e.id === 'string');
    } catch {
      stored = [];
    }
    // Entries added before the store was read are kept too, and saved now.
    const pending = get().items;
    const items = merge(pending, stored);
    set({ loaded: true, items });
    if (pending.length) save(items);
  },
  add: (entries) => {
    // One alert can reach the inbox twice (raised in the app, then read back
    // from the shade): same kind and device within DEDUP_MS is the same alert.
    // Entries without a device (messages, updates) are told apart by id only.
    const dup = (e: Omit<InboxEntry, 'read'>, x: InboxEntry) =>
      x.id === e.id || (!!e.mac && x.kind === e.kind && x.mac === e.mac.toLowerCase() && Math.abs(x.at - e.at) < DEDUP_MS);
    const fresh = entries
      .filter((e, i) => !get().items.some((x) => dup(e, x)) && !entries.slice(0, i).some((x) => dup(e, { ...x, mac: x.mac.toLowerCase(), read: false })))
      .map((e) => ({ ...e, mac: e.mac.toLowerCase(), read: false }));
    if (!fresh.length) return;
    const items = merge(fresh, get().items);
    set({ items });
    // Before load, saving would overwrite what is stored: load saves the merge.
    if (get().loaded) save(items);
  },
  markAllRead: () => {
    if (!get().items.some((e) => !e.read)) return;
    const items = get().items.map((e) => (e.read ? e : { ...e, read: true }));
    set({ items });
    save(items);
  },
  remove: (id) => {
    const items = get().items.filter((e) => e.id !== id);
    set({ items });
    save(items);
  },
  clear: () => {
    set({ items: [] });
    save([]);
  },
}));

/** Newest first, one entry per id, at most INBOX_MAX. */
function merge(a: InboxEntry[], b: InboxEntry[]): InboxEntry[] {
  const seen = new Set<string>();
  return [...a, ...b]
    .filter((e) => (seen.has(e.id) ? false : (seen.add(e.id), true)))
    .sort((x, y) => y.at - x.at)
    .slice(0, INBOX_MAX);
}

export function unreadCount(items: InboxEntry[]): number {
  return items.filter((e) => !e.read).length;
}
