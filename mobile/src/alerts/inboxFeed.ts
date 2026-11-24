import * as Notifications from 'expo-notifications';
import { useEffect } from 'react';
import { AppState } from 'react-native';

import { t } from '../i18n';
import { useInbox, type InboxEntry } from './inbox';

// What reaches the inbox from the phone's notifications: the hub's push
// (issue #49) and the Android watch post theirs where the app's JS does not
// run. The ones still in the shade are read back when the app comes to the
// foreground, and one received while the app is open is added at once.

type Note = Notifications.Notification;

const str = (v: unknown) => (typeof v === 'string' ? v : '');

/**
 * The inbox entry of a notification, or null: a device notification (its
 * data carries the MAC), or an announcement of the Kergui team (#80, data
 * {kind: "announcement", messageId, title, body}).
 */
export function entryOf(n: Note): Omit<InboxEntry, 'read'> | null {
  const c = n.request.content;
  const data = (c.data ?? {}) as Record<string, unknown>;
  if (data.kind === 'announcement') {
    const title = str(data.title) || c.title || '';
    const body = str(data.body) || c.body || '';
    if (!title && !body) return null;
    const messageId = str(data.messageId);
    return {
      // The same announcement read back from the shade is the same entry.
      id: messageId ? `message:${messageId}` : n.request.identifier || `message:${n.date}`,
      kind: 'message',
      mac: '',
      name: title,
      text: body || undefined,
      at: n.date > 0 ? n.date : Date.now(),
    };
  }
  const mac = typeof data.mac === 'string' ? data.mac : '';
  if (!mac) return null;
  const kind = c.title === t('alerts.refusedTitle') ? 'refused' : 'arrival';
  const name = typeof data.name === 'string' && data.name ? data.name : mac;
  return {
    id: n.request.identifier || `${kind}:${mac.toLowerCase()}:${n.date}`,
    kind,
    mac,
    name,
    text: c.body ?? undefined,
    at: n.date > 0 ? n.date : Date.now(),
  };
}

function addNotes(notes: Note[]) {
  const entries = notes.map(entryOf).filter((e): e is Omit<InboxEntry, 'read'> => e !== null);
  if (entries.length) useInbox.getState().add(entries);
}

async function sweep() {
  try {
    addNotes(await Notifications.getPresentedNotificationsAsync());
  } catch {
    // Not available (web, tests): the in-app alerts still reach the inbox.
  }
}

export function useInboxFeed() {
  useEffect(() => {
    void useInbox.getState().load().then(sweep);
    const received = Notifications.addNotificationReceivedListener((n) => addNotes([n]));
    const app = AppState.addEventListener('change', (s) => {
      if (s === 'active') void sweep();
    });
    return () => {
      received.remove();
      app.remove();
    };
  }, []);
}
