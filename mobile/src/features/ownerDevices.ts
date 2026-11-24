import { api } from '../core/client';
import { owner } from '../core/owner';
import { remoteCore } from '../core/remote';
import { useAccount } from '../licensing/account';
import { config } from '../licensing/config';
import { hub } from '../licensing/hub';
import { withHousehold } from '../licensing/household';
import { useSession } from '../session/store';

// The owner's phones (issue #87, parent #69). The hub keeps the household's
// list: the MAC each owner phone has on the box (Android's private MAC is
// stable per Wi-Fi). Every phone fetches it with the household data and
// hands it to the core, as it does the approvals; the core marks those
// devices « Owner », and the Devices tab leaves them out like this phone.
//
// The owner's phone declares itself: once the core found it on the box
// (Self) and the bundle in force is signed by its owner key, it records that
// MAC on the hub. A new owner key (recovery, ownership change) drops the old
// phone there; the new phone declares itself the same way.

export type OwnerDevicesDeps = {
  signedIn: () => boolean;
  hub: {
    /** The household's owner phones, as the inventory lists them. */
    list: () => Promise<string[]>;
    /** Records `mac` as this owner's phone, under its owner key. */
    declare: (mac: string, ownerKey: string) => Promise<void>;
  };
  /** Hands the list to the core: for the box, or for a household's remote list. */
  core: { set: (macs: string[], tenantId?: string) => Promise<void> };
  /** Whether this phone's owner key signed the household's bundle in force. */
  isOwner: () => Promise<{ owner: boolean; keyId: string }>;
  now?: () => number;
  /** How long a hub read is reused (ms). */
  every?: number;
};

/** How long a phone that may not declare itself is left alone (ms). */
const RETRY_AFTER = 10 * 60_000;

const norm = (m: string) => m.toLowerCase();

export function createOwnerDevices(d: OwnerDevicesDeps) {
  const now = d.now ?? (() => Date.now());
  let read: { at: number; macs: string[] } | null = null;
  // Where the current read was handed: "box", or a household id.
  let handed = new Set<string>();
  // A MAC this phone may not declare (a member, a refused key), until when.
  let skip: { mac: string; until: number } | null = null;

  const fresh = () => read && d.every && now() - read.at < d.every;

  return {
    /** Fetches the hub's list (reused for `every`) and hands it to the core. */
    sync: async ({ tenantId }: { tenantId?: string } = {}): Promise<void> => {
      if (!d.signedIn()) return;
      if (!fresh()) {
        read = { at: now(), macs: (await d.hub.list()).map(norm) };
        handed = new Set();
      }
      const where = tenantId ?? 'box';
      if (handed.has(where)) return;
      await d.core.set(read!.macs, tenantId);
      handed.add(where);
    },

    /** On the owner's phone, records the MAC the core marked Self. Best effort. */
    declare: async (devices: { MAC: string; Mark?: string }[]): Promise<void> => {
      if (!d.signedIn()) return;
      const me = devices.find((x) => x.Mark === 'Self');
      if (!me) return;
      const mac = norm(me.MAC);
      if (read?.macs.includes(mac)) return;
      if (skip && skip.mac === mac && now() < skip.until) return;
      try {
        const o = await d.isOwner();
        if (!o.owner || !o.keyId) {
          skip = { mac, until: now() + RETRY_AFTER };
          return;
        }
        await d.hub.declare(mac, o.keyId);
        read = null; // read it back with the next sync
      } catch {
        skip = { mac, until: now() + RETRY_AFTER };
      }
    },
  };
}

const onHub = () =>
  !!config.hubUrl && !!useAccount.getState().account && useSession.getState().session?.mode !== 'demo';

export const ownerDevices = createOwnerDevices({
  signedIn: onHub,
  hub: {
    list: () => withHousehold(async (tok, id) => ((await hub.inventory(tok, id))?.owner_devices ?? []).map((o) => o.mac)),
    declare: (mac, ownerKey) => withHousehold((tok, id) => hub.declareOwnerDevice(tok, id, mac, ownerKey)),
  },
  core: { set: async (macs, tenantId) => void (await api.setOwnerDevices(macs, tenantId)) },
  isOwner: () =>
    withHousehold(async (tok, id) => {
      const bundle = await hub.trustBundle(tok, id);
      if (!bundle || !(await remoteCore.relays(id, bundle)).owner) return { owner: false, keyId: '' };
      return { owner: true, keyId: (await owner.keys()).keyId };
    }),
  every: 60_000,
});
