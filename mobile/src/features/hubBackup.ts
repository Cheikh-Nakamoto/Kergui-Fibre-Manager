import * as SecureStore from 'expo-secure-store';

import { api } from '../core/client';
import { useAccount } from '../licensing/account';
import { HubError } from '../licensing/api';
import { config } from '../licensing/config';
import { hub } from '../licensing/hub';
import { withHousehold } from '../licensing/household';
import { useSession } from '../session/store';

// The household's backup on the hub (issue #89, parent #70). The archive is
// the core's own export (GET /api/export: appareils.csv and conso.csv, never
// the box password, the vault or a key), the same « Faire une sauvegarde »
// shares and « Importer une sauvegarde » reads; the hub keeps one per
// household and box (the core's box id) and merges its senders with the
// import's rules.
//
// When it is sent: 30 s after the last change that matters (a name, an
// approval), at most once a day otherwise (the consumption), and on
// « Sauvegarder maintenant ». Only an owner or an admin may write: a member's
// 403 stops the automatic sends without a word.
//
// When it is imported — « older data »: the phone keeps, per box, the time of
// the hub's backup it is in step with (`at`: the hub's updatedAt of its last
// send or import). The hub's backup is imported when the phone has no such
// time for the box (an empty, new or reinstalled phone), or when the hub's
// updatedAt is later (another phone, the PC relay). It is checked once per
// launch while the phone has no time for its box, and after each sign-in to
// the account. A change of this phone not sent yet wins: it is sent first and
// nothing is imported over it.

export type HubBackupCopy = { boxId: string; updatedAt: string; archive: string; name: string };

export type HubBackupDeps = {
  /** A hub, an account, a real box (not the demo). */
  available: () => boolean;
  core: {
    export: () => Promise<{ archive: string; name: string; boxId: string }>;
    import: (archive: string) => Promise<{ devices: number; named: number; usage: number }>;
  };
  hub: {
    /** The household's backup of the box, or null when there is none yet. */
    get: (boxId: string) => Promise<HubBackupCopy | null>;
    put: (boxId: string, archive: string) => Promise<{ boxId: string; updatedAt: string; merged: boolean }>;
  };
  store: { load: () => Promise<string | null>; save: (v: string) => Promise<void> };
  now?: () => number;
  /** Quiet time after a change before it is sent (ms). */
  debounceMs?: number;
  /** At most one automatic send per this period, nothing changed (ms). */
  every?: number;
  /** Wait after an automatic send that failed (offline, hub down) (ms). */
  retryAfter?: number;
  /** Between two looks of tick, nothing pending (ms): it reads the core's export. */
  tickEvery?: number;
  /** Told after a send or an import, for Réglages. */
  onChange?: () => void;
};

/** « Sauvegarder maintenant » without a hub: the demo, no Kergui account. */
export class BackupUnavailable extends Error {
  constructor() {
    super('no hub backup here');
    this.name = 'BackupUnavailable';
  }
}

type State = {
  /**
   * Per box: the hub's updatedAt this phone is in step with, and when (this
   * phone's clock, ms) it sent or imported it — the daily send counts from there.
   */
  boxes: Record<string, { at: string; syncedAt: number }>;
  /** The box the core pointed at last. */
  box?: string;
  /** A change not sent yet. */
  dirty?: boolean;
  /** A sign-in asks for the hub's backup at the next tick. */
  restore?: boolean;
};

const later = (a: string, b: string) => Date.parse(a) > Date.parse(b);

export function createHubBackup(d: HubBackupDeps) {
  const now = d.now ?? (() => Date.now());
  const debounceMs = d.debounceMs ?? 30_000;
  const every = d.every ?? 24 * 3600_000;
  const retryAfter = d.retryAfter ?? 10 * 60_000;
  const tickEvery = d.tickEvery ?? 5 * 60_000;
  let lastTick = -Infinity;

  let state: State | null = null;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let forbidden = false; // a member: the hub refuses its writes
  let failedAt = -Infinity;
  const checked = new Set<string>(); // boxes whose hub copy was looked at this launch
  let queue: Promise<unknown> = Promise.resolve();

  // One send or import at a time.
  const serial = <T>(fn: () => Promise<T>): Promise<T> => {
    const run = queue.then(fn, fn);
    queue = run.catch(() => undefined);
    return run;
  };

  const load = async (): Promise<State> => {
    if (state) return state;
    let s: State = { boxes: {} };
    try {
      const raw = await d.store.load();
      if (raw) s = { ...s, ...(JSON.parse(raw) as State) };
    } catch {
      // Unreadable: as an empty phone.
    }
    state = state ?? s;
    return state;
  };
  const save = async () => {
    if (state) await d.store.save(JSON.stringify(state)).catch(() => undefined);
  };

  const send = async () => {
    const s = await load();
    const exp = await d.core.export();
    const saved = await d.hub.put(exp.boxId, exp.archive);
    s.boxes[exp.boxId] = { at: saved.updatedAt, syncedAt: now() };
    s.box = exp.boxId;
    s.dirty = false;
    await save();
    d.onChange?.();
    return saved;
  };

  // An automatic send: a member's 403 and a failure are quiet.
  const quietSend = async () => {
    if (forbidden || now() - failedAt < retryAfter) return;
    try {
      await send();
    } catch (e) {
      if (e instanceof HubError && e.status === 403) {
        forbidden = true;
        const s = await load();
        s.dirty = false;
        await save();
        return;
      }
      failedAt = now();
    }
  };

  const restore = async (s: State, boxId: string): Promise<boolean> => {
    const copy = await d.hub.get(boxId);
    checked.add(boxId);
    s.restore = false;
    const mine = s.boxes[boxId]?.at;
    if (!copy || (mine && !later(copy.updatedAt, mine))) {
      await save();
      return false;
    }
    await d.core.import(copy.archive);
    s.boxes[boxId] = { at: copy.updatedAt, syncedAt: now() };
    s.box = boxId;
    await save();
    d.onChange?.();
    return true;
  };

  return {
    /** A name, an approval: sent once nothing changed for debounceMs. */
    changed: (): void => {
      if (!d.available()) return;
      void load().then((s) => {
        if (!s.dirty) {
          s.dirty = true;
          void save();
        }
      });
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => {
        timer = null;
        void serial(quietSend);
      }, debounceMs);
    },

    /** The account signed in: the hub's backup is looked at the next tick. */
    signedIn: (): void => {
      forbidden = false;
      checked.clear();
      void load().then((s) => {
        s.restore = true;
        void save();
      });
    },

    /**
     * On each refresh of the box's list (local mode): the hub's backup when
     * it is due, then a send when something changed or a day went by. Best
     * effort, never throws.
     */
    tick: (): Promise<void> =>
      serial(async () => {
        if (!d.available()) return;
        try {
          const s = await load();
          if (!s.dirty && !s.restore && now() - lastTick < tickEvery) return;
          lastTick = now();
          const boxId = (await d.core.export()).boxId;
          const mine = s.boxes[boxId]?.at;
          if (!s.dirty && (s.restore || (!mine && !checked.has(boxId)))) {
            if (await restore(s, boxId)) return;
          }
          const synced = s.boxes[boxId]?.syncedAt;
          if (s.dirty || synced === undefined || now() - synced >= every) await quietSend();
        } catch {
          failedAt = now();
        }
      }),

    /** « Sauvegarder maintenant ». Throws BackupUnavailable, or the hub's refusal. */
    sendNow: () => {
      if (!d.available()) return Promise.reject(new BackupUnavailable());
      if (timer) clearTimeout(timer);
      timer = null;
      return serial(send);
    },

    /** The hub's backup of this box as a zip, null when there is none. */
    download: async (): Promise<{ archive: string; name: string } | null> => {
      if (!d.available()) throw new BackupUnavailable();
      const copy = await d.hub.get((await d.core.export()).boxId);
      return copy ? { archive: copy.archive, name: copy.name } : null;
    },

    /** For Réglages: whether it works here, and the last backup of the box. */
    status: async (): Promise<{ available: boolean; lastAt: string | null }> => {
      const s = await load();
      return { available: d.available(), lastAt: (s.box && s.boxes[s.box]?.at) || null };
    },
  };
}

const STATE_KEY = 'kergui.hub.backup';

export const hubBackupKey = ['hub', 'backup'] as const;

let listeners: (() => void)[] = [];
/** Réglages refreshes its « dernière sauvegarde » line. */
export function onHubBackupChange(fn: () => void): () => void {
  listeners.push(fn);
  return () => {
    listeners = listeners.filter((x) => x !== fn);
  };
}

export const hubBackup = createHubBackup({
  available: () =>
    !!config.hubUrl && !!useAccount.getState().account && useSession.getState().session?.mode === 'router',
  core: {
    export: () => api.exportBackup(),
    import: (archive) => api.importBackup(archive),
  },
  hub: {
    get: (boxId) => withHousehold((tok, id) => hub.backup(tok, id, boxId)),
    put: (boxId, archive) => withHousehold((tok, id) => hub.putBackup(tok, id, boxId, archive)),
  },
  store: {
    load: () => SecureStore.getItemAsync(STATE_KEY),
    save: (v) => SecureStore.setItemAsync(STATE_KEY, v),
  },
  onChange: () => listeners.forEach((fn) => fn()),
});

// A sign-in (not the launch's load of a stored account) asks for the hub's
// backup: a new or reinstalled phone gets the household's names back.
useAccount.subscribe((s, prev) => {
  if (prev.ready && s.account && s.account.accountId !== prev.account?.accountId) hubBackup.signedIn();
});
