import { api } from '../core/client';
import { useAccount } from '../licensing/account';
import { HubError } from '../licensing/api';
import { config } from '../licensing/config';
import { hub } from '../licensing/hub';
import { withHousehold } from '../licensing/household';
import { useSession } from '../session/store';

// Approvals (issue #58). An approved device never alerts again; any other
// alerts each time it arrives. « Autoriser » (the alert, the device sheet)
// and giving the device a name approve it.
//
// The core keeps the list for the box: the Android watch service reads it,
// and its own « Autoriser » lands there. Signed in to a household, the hub
// keeps the household's list, shared by every phone and the relay: what this
// phone approved on its own (unsynced: the notification's button, names
// given before #58) is pushed there first, then the hub's list replaces the
// core's, so a withdrawal on another phone reaches this one.

export type ApprovalsDeps = {
  core: {
    list: () => Promise<{ macs: string[]; unsynced: string[] }>;
    replace: (macs: string[]) => Promise<void>;
    approve: (mac: string) => Promise<void>;
    withdraw: (mac: string) => Promise<void>;
  };
  hub: {
    signedIn: () => boolean;
    approved: () => Promise<string[]>;
    approve: (mac: string) => Promise<void>;
    approveMany: (macs: string[]) => Promise<void>;
    withdraw: (mac: string) => Promise<void>;
  };
  now?: () => number;
  /** How long a hub read is reused, nothing being left to push (ms). */
  every?: number;
};

const norm = (m: string) => m.toLowerCase();

export function createApprovals(d: ApprovalsDeps) {
  const now = d.now ?? (() => Date.now());
  let lastHubRead = -Infinity;

  /** Through the relay (#46) the core is not the box's: the hub alone. */
  let remoteCache: { at: number; set: Set<string> } | null = null;
  const remoteSync = async (): Promise<Set<string>> => {
    if (remoteCache && d.every && now() - remoteCache.at < d.every) return remoteCache.set;
    const set = new Set((await d.hub.approved()).map(norm));
    remoteCache = { at: now(), set };
    return set;
  };

  const sync = async ({ remote = false }: { remote?: boolean } = {}): Promise<Set<string>> => {
    if (remote) return remoteSync();
    const local = await d.core.list();
    const mine = new Set((local.macs ?? []).map(norm));
    if (!d.hub.signedIn()) return mine;
    const unsynced = local.unsynced ?? [];
    if (!unsynced.length && d.every && now() - lastHubRead < d.every) return mine;
    if (unsynced.length) {
      try {
        await d.hub.approveMany(unsynced.map(norm));
      } catch (e) {
        // A member the hub does not let approve: the household's list wins.
        // Anything else (offline, no inventory yet): try again later.
        if (!(e instanceof HubError && e.status === 403)) return mine;
      }
    }
    let theirs: string[];
    try {
      theirs = (await d.hub.approved()).map(norm);
    } catch {
      return mine;
    }
    await d.core.replace(theirs);
    lastHubRead = now();
    return new Set(theirs);
  };

  return {
    sync,
    approve: async (mac: string, { remote = false }: { remote?: boolean } = {}) => {
      if (!remote) await d.core.approve(norm(mac));
      if (remote || d.hub.signedIn()) await d.hub.approve(norm(mac));
      lastHubRead = -Infinity;
      remoteCache = null;
    },
    withdraw: async (mac: string, { remote = false }: { remote?: boolean } = {}) => {
      if (!remote) await d.core.withdraw(norm(mac));
      if (remote || d.hub.signedIn()) await d.hub.withdraw(norm(mac));
      lastHubRead = -Infinity;
      remoteCache = null;
    },
  };
}

const onHub = () =>
  !!config.hubUrl && !!useAccount.getState().account && useSession.getState().session?.mode !== 'demo';

export const approvals = createApprovals({
  core: {
    list: () => api.approvals(),
    replace: async (macs) => void (await api.setApprovals(macs)),
    approve: async (mac) => void (await api.approve(mac)),
    withdraw: async (mac) => void (await api.withdrawApproval(mac)),
  },
  hub: {
    signedIn: onHub,
    approved: () =>
      withHousehold(async (tok, id) => ((await hub.inventory(tok, id))?.approved ?? []).map((a) => a.mac)),
    approve: (mac) => withHousehold((tok, id) => hub.approveDevice(tok, id, mac)),
    approveMany: (macs) => withHousehold((tok, id) => hub.approveDevices(tok, id, macs)),
    withdraw: (mac) => withHousehold((tok, id) => hub.withdrawApproval(tok, id, mac)),
  },
  every: 60_000,
});
