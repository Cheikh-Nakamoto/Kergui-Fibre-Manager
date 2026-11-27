import * as SecureStore from 'expo-secure-store';

import type { Device } from '../core/types';
import { displayName, isHidden } from '../lib/format';

// The last device list read from the box, kept on the phone between launches
// (#124, audit M1): away from home without a relay, the Devices tab shows it,
// dated. Names and states only, never a MAC nor an IP, and the time it was
// read; nothing else. Kept in SecureStore, the only store the app has, and
// forgotten with the box (« Oublier cette box »).

export const LAST_LIST_KEY = 'kergui.devices.last';
/** The value stays small: a household rarely has more. */
const MAX_DEVICES = 40;

export type LastDevice = { name: string; connected: boolean; blocked: boolean };
export type LastList = { at: string; devices: LastDevice[] };

export const lastList = {
  /** Keeps what the box just listed, with the time. Best effort. */
  save: async (devices: Device[], at: Date = new Date()): Promise<void> => {
    const kept: LastList = {
      at: at.toISOString(),
      devices: devices
        .filter((d) => !isHidden(d))
        .slice(0, MAX_DEVICES)
        .map((d) => ({ name: displayName(d), connected: !!d.Connected, blocked: !!d.Blocked })),
    };
    await SecureStore.setItemAsync(LAST_LIST_KEY, JSON.stringify(kept)).catch(() => undefined);
  },

  /** The list kept, or null when none was ever read (or it cannot be read). */
  load: async (): Promise<LastList | null> => {
    try {
      const raw = await SecureStore.getItemAsync(LAST_LIST_KEY);
      if (!raw) return null;
      const v = JSON.parse(raw) as LastList;
      if (typeof v?.at !== 'string' || !Array.isArray(v.devices)) return null;
      return v;
    } catch {
      return null;
    }
  },

  forget: async (): Promise<void> => {
    await SecureStore.deleteItemAsync(LAST_LIST_KEY).catch(() => undefined);
  },
};
