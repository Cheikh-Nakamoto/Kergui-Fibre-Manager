import * as SecureStore from 'expo-secure-store';

import { api } from '../core/client';
import type { Device, DeviceLabel } from '../core/types';
import { useEntitlement } from '../licensing/entitlement';
import { remoteDevices, remoteLabels, type RemoteHousehold } from './remote';

// The household's names and notes are the same at home and through the relay
// (issue #95). Each side keeps its own copy, with when each label was set:
// the owner's phone in its core, the relay in its own core, which lists them
// in the device list it signs (LabelAt). They meet here, and the later edit
// wins:
//
//   - the owner's phone sends its labels to the relay, in a command the
//     owner signs (POST /api/labels; the hub only carries it): after a
//     rename, at home as well as away, and whenever the relay's list shows
//     it is behind — a new relay, one that took over the lease, one that
//     missed a send. The relay keeps a label only if it has no later one.
//   - a rename made through the relay (a member's, or the owner's away) is
//     kept by the relay, and the owner's phone takes it into its core when it
//     reads the relay's list: labels later than its own are applied.
//   - until the relay has them, the list shown through the relay is merged
//     with this phone's labels by MAC, the later one winning: a name never
//     looks « reset ».
//
// Names grant things on the relay (a named device is accepted under
// auto-reject), so the relay takes them only from the owner's key or a
// member's under a control delegation, never from the hub's backup.

/** The relay's signed list, as remoteDevices verified it. */
export type RelayList = { agentId: string | null; asOf: string | null; devices: Device[] };

export type LabelSyncDeps = {
  core: {
    /** This phone's labels; throws when the core keeps none (a member's phone with no box). */
    labels: () => Promise<DeviceLabel[]>;
    apply: (labels: DeviceLabel[]) => Promise<string[]>;
    /** A rename on this phone's core, stamped by it; throws 404 when it never saw the device. */
    rename: (mac: string, name: string) => Promise<unknown>;
  };
  relay: {
    list: (h: RemoteHousehold) => Promise<RelayList>;
    send: (h: RemoteHousehold, labels: DeviceLabel[]) => Promise<string[]>;
  };
  store: { load: () => Promise<string | null>; save: (v: string) => Promise<void> };
  now?: () => number;
  /** Between two looks at whether the relay is behind, nothing changed here (ms). */
  every?: number;
  /** Wait after a send that failed (offline, a hub or relay too old) (ms). */
  retryAfter?: number;
  /** This phone's core took labels from the relay: its backup is due. */
  onChange?: () => void;
};

const norm = (m: string) => m.toLowerCase();
const ms = (at: string | undefined | null) => {
  const n = at ? Date.parse(at) : NaN;
  return Number.isNaN(n) ? 0 : n;
};

/** The label a device of a list carries, when it carries a time. */
export function labelOf(d: Device): DeviceLabel | null {
  if (!d.LabelAt || !ms(d.LabelAt)) return null;
  return { mac: norm(d.MAC), name: d.CustomName ?? '', notes: d.Notes ?? '', at: d.LabelAt };
}

/** The latest label per MAC. */
function latest(labels: DeviceLabel[]): Map<string, DeviceLabel> {
  const out = new Map<string, DeviceLabel>();
  for (const l of labels) {
    const k = norm(l.mac);
    const cur = out.get(k);
    if (!cur || ms(l.at) > ms(cur.at)) out.set(k, l);
  }
  return out;
}

/**
 * A list from the relay with the labels this phone knows, by MAC: a label
 * later than the one the relay listed (or than none) wins. Last write wins.
 */
export function mergeLabels(devices: Device[], labels: DeviceLabel[]): Device[] {
  const mine = latest(labels);
  if (!mine.size) return devices;
  return devices.map((d) => {
    const l = mine.get(norm(d.MAC));
    if (!l || (d.LabelAt && ms(l.at) <= ms(d.LabelAt))) return d;
    return { ...d, CustomName: l.name ?? d.CustomName, Notes: l.notes ?? d.Notes, LabelAt: l.at };
  });
}

/** Per household: what was last sent, to which relay. */
type Sent = { agentId: string; sentAt: number; maxAt: number };

export function createLabelSync(d: LabelSyncDeps) {
  const now = d.now ?? (() => Date.now());
  const every = d.every ?? 5 * 60_000;
  const retryAfter = d.retryAfter ?? 15 * 60_000;
  let sent: Record<string, Sent> | null = null;
  let lastLook = -Infinity;
  let failedAt = -Infinity;
  // Renames sent through the relay this session (a member's phone keeps no
  // labels of its own), shown until the relay's list carries them.
  const pending = new Map<string, DeviceLabel>();
  let queue: Promise<unknown> = Promise.resolve();
  const serial = <T>(fn: () => Promise<T>): Promise<T> => {
    const run = queue.then(fn, fn);
    queue = run.catch(() => undefined);
    return run;
  };

  const load = async (): Promise<Record<string, Sent>> => {
    if (sent) return sent;
    let s: Record<string, Sent> = {};
    try {
      const raw = await d.store.load();
      if (raw) s = JSON.parse(raw) as Record<string, Sent>;
    } catch {
      // Unreadable: everything is sent once more, which the relay ignores.
    }
    sent = sent ?? s;
    return sent;
  };
  const save = async () => {
    if (sent) await d.store.save(JSON.stringify(sent)).catch(() => undefined);
  };

  const mine = () => d.core.labels().catch((): DeviceLabel[] => []);

  /** The relay's labels later than this phone's go into its core. */
  const pull = async (list: Device[], have: DeviceLabel[]): Promise<DeviceLabel[]> => {
    const known = latest(have);
    const newer = list
      .map(labelOf)
      .filter((l): l is DeviceLabel => !!l && ms(l.at) > ms(known.get(l.mac)?.at));
    if (!newer.length) return have;
    try {
      const changed = await d.core.apply(newer);
      if (changed.length) d.onChange?.();
      return await mine();
    } catch {
      return have; // a core with no box: the merge still shows them
    }
  };

  // The owner's labels to the relay, when it is behind. Never throws.
  const push = async (h: RemoteHousehold, opts: { list?: RelayList; force?: boolean }) => {
    if (h.member) return;
    const t = now();
    if (!opts.force && (t - lastLook < every || t - failedAt < retryAfter)) return;
    lastLook = t;
    let list = opts.list ?? null;
    if (!list) list = await d.relay.list(h).catch(() => null);
    let labels = await mine();
    if (list) labels = await pull(list.devices, labels);
    if (!labels.length) return;
    const s = (await load())[h.tenantId];
    const agentId = list?.agentId ?? '';
    const maxAt = Math.max(...labels.map((l) => ms(l.at)));
    // Behind: the relay read the box after the last send and still lists an
    // older label (or none) for a device this phone labelled.
    const listed = new Map((list?.devices ?? []).map((x) => [norm(x.MAC), x] as const));
    const behind =
      !!list &&
      (!s || ms(list.asOf) > s.sentAt) &&
      labels.some((l) => {
        const x = listed.get(norm(l.mac));
        return !!x && ms(l.at) > ms(x.LabelAt);
      });
    const due = opts.force || !s || (agentId && s.agentId !== agentId) || maxAt > s.maxAt || behind;
    if (!due) return;
    try {
      await d.relay.send(h, labels);
    } catch {
      failedAt = now();
      return;
    }
    failedAt = -Infinity;
    (await load())[h.tenantId] = { agentId: agentId || s?.agentId || '', sentAt: now(), maxAt };
    await save();
  };

  return {
    /**
     * The list through the relay as the household should see it: the
     * relay's labels later than this phone's taken into its core, then the
     * phone's own (and the renames it just sent) merged in by MAC. On the
     * owner's phone it also sends the relay what it lacks, in the background.
     */
    view: async (h: RemoteHousehold, list: RelayList): Promise<Device[]> => {
      const labels = await pull(list.devices, await mine());
      for (const [mac, l] of pending) {
        const x = list.devices.find((dev) => norm(dev.MAC) === mac);
        if (x && ms(x.LabelAt) >= ms(l.at)) pending.delete(mac);
      }
      if (!h.member) void serial(() => push(h, { list }));
      return mergeLabels(list.devices, [...labels, ...pending.values()]);
    },

    /**
     * At home: the relay is not where the list comes from, but it must keep
     * the owner's names for when the phone is away, and for the members.
     * Best effort, at most every few minutes.
     */
    tick: (h: RemoteHousehold | null): Promise<void> => (h && !h.member ? serial(() => push(h, {})) : Promise.resolve()),

    /**
     * « Renommer ». The owner's phone renames in its own core (stamped by
     * it), then sends its labels to the relay: a relay that cannot be
     * reached gets them later, the name stays on this phone meanwhile. A
     * member's phone sends the rename through the relay, and is told when it
     * could not.
     */
    rename: async (h: RemoteHousehold | null, mac: string, name: string): Promise<void> => {
      const key = norm(mac);
      if (!h?.member) {
        try {
          await d.core.rename(mac, name);
        } catch (e) {
          // The core never saw it (it joined while the phone was away):
          // the label alone, which its next list picks up.
          if (!h) throw e;
          await d.core.apply([{ mac: key, name, at: new Date(now()).toISOString() }]);
        }
        if (h) await serial(() => push(h, { force: true }));
        return;
      }
      const label: DeviceLabel = { mac: key, name, at: new Date(now()).toISOString() };
      await d.relay.send(h, [label]);
      pending.set(key, label);
      await d.core.apply([label]).catch(() => undefined);
    },
  };
}

const STATE_KEY = 'kergui.labels.sent';

let changeListeners: (() => void)[] = [];
/** This phone's core took names from the relay (its hub backup is due). */
export function onLabelsChange(fn: () => void): () => void {
  changeListeners.push(fn);
  return () => {
    changeListeners = changeListeners.filter((x) => x !== fn);
  };
}

export const labelSync = createLabelSync({
  core: {
    labels: () => api.labels(),
    apply: async (labels) => (await api.applyLabels(labels))?.changed ?? [],
    rename: (mac, name) => api.rename(mac, name),
  },
  relay: {
    list: async (h) => {
      const r = await remoteDevices(h);
      return { agentId: r.relay.agentId ?? r.agentId ?? null, asOf: r.asOf, devices: r.devices };
    },
    send: (h, labels) => remoteLabels(h, labels, { boxId: useEntitlement.getState().boxId ?? '' }),
  },
  store: {
    load: () => SecureStore.getItemAsync(STATE_KEY),
    save: (v) => SecureStore.setItemAsync(STATE_KEY, v),
  },
  onChange: () => changeListeners.forEach((fn) => fn()),
});
