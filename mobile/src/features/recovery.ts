import { create } from 'zustand';

import { recoveryCore, RecoveryError, type FoundRelays, type Recovered } from '../core/recovery';
import { remoteCore, type BundleCoOwner, type MemberAccess } from '../core/remote';
import type { HouseholdMember } from '../licensing/api';
import { hub } from '../licensing/hub';
import { withHousehold } from '../licensing/household';
import { changeBundle } from './delegations';

// Recovery of a lost owner phone (issue #48, RFC-005 "Lost owner phone").
// The owner names co-owners in advance — a member's key, signed into the
// bundle; one of them can take the household over from their own phone. With
// no co-owner, the owner's new phone proves the box password to a relay on
// the home Wi-Fi. Either way the core signs the household's next bundle with
// this phone's key, the old owner key revoked, and the hub stores it like
// any other: the relays check the handover against the bundle they hold, and
// refuse the old key's commands from then on. A co-owner must be an
// administrator: only they may store a bundle on the hub.

export const recoveryKey = ['hub', 'recovery'] as const;

/** What the household's bundle says about recovery, read by the core. */
export type RecoveryView = {
  /** This phone's owner key signed the bundle. */
  owner: boolean;
  hasRelay: boolean;
  /** The co-owners (owner's phone only). */
  coOwners: BundleCoOwner[];
  /** This phone's member key in the bundle (another phone), coOwner included. */
  member: MemberAccess | null;
};

const EMPTY: RecoveryView = { owner: false, hasRelay: false, coOwners: [], member: null };

export function loadRecovery(): Promise<RecoveryView> {
  return withHousehold(async (tok, tenantId) => {
    const bundle = await hub.trustBundle(tok, tenantId);
    if (!bundle) return EMPTY;
    const v = await remoteCore.relays(tenantId, bundle);
    return { owner: v.owner, hasRelay: v.relays.length > 0, coOwners: v.coOwners, member: v.member };
  });
}

/** Whether the owner signed this member's published key in as a co-owner. */
export function isCoOwner(m: HouseholdMember, view: RecoveryView | undefined): boolean {
  const id = m.keys?.keyId;
  return !!id && !!view?.coOwners.some((c) => c.keyId === id);
}

export const recovery = {
  /** The owner signs a member's published key in as a co-owner. */
  designate: (m: HouseholdMember) => {
    if (!m.keys) return Promise.reject(new Error('this member has not published a key yet'));
    return changeBundle({ coOwner: { ed25519: m.keys.ed25519, x25519: m.keys.x25519, label: m.phone } });
  },
  /** The owner drops a co-owner. */
  remove: (keyId: string) => changeBundle({ removeCoOwner: keyId }),
  /** A co-owner's phone takes the household over; the hub stores the new owner's bundle. */
  takeOver: (): Promise<Recovered> =>
    withHousehold(async (tok, tenantId) => {
      const current = await hub.trustBundle(tok, tenantId);
      const r = await recoveryCore.takeOver(tenantId, current);
      await hub.putTrustBundle(tok, tenantId, r.bundle);
      return r;
    }),
  /**
   * The owner's new phone, at home, finds the household's relay on the
   * Wi-Fi by itself (#117): the relays of the signed-in household whose
   * bundle the owner the hub knows signed, the most up to date first.
   */
  findRelays: (): Promise<FoundRelays> =>
    withHousehold(async (tok, tenantId) => {
      const bundle = await hub.trustBundle(tok, tenantId);
      return recoveryCore.findRelays(tenantId, bundle);
    }),
  /**
   * The owner's new phone, at home: the relay on the Wi-Fi checks the box
   * password, and the hub stores the bundle this phone then signs. The relay
   * must be the signed-in household's.
   */
  atHome: (relayAddress: string, boxPassword: string): Promise<Recovered> =>
    withHousehold(async (tok, tenantId) => {
      const r = await recoveryCore.atHome(relayAddress, boxPassword);
      if (r.tenantId !== tenantId) throw new RecoveryError('other_household', 'the relay belongs to another household');
      await hub.putTrustBundle(tok, tenantId, r.bundle);
      return r;
    }),
};

// The demo has no hub and nothing is signed: the example household's admin
// can be made a co-owner, and the recovery at home is played against the
// demo box's password, in memory only.

/** The demo relay's address on the Wi-Fi, found by the simulated search. */
export const DEMO_RELAY_ADDRESS = '192.168.1.20';
/** How long the demo's search seems to take. */
export const DEMO_FIND_MS = 800;

/** The demo's search on the Wi-Fi (#117): the demo relay, after a moment. Nothing is asked. */
export function demoFindRelays(delayMs = DEMO_FIND_MS): Promise<FoundRelays> {
  const found: FoundRelays = { relays: [{ address: DEMO_RELAY_ADDRESS, agentId: 'demo-relay', version: 1 }], others: 0 };
  return new Promise((ok) => setTimeout(() => ok(found), delayMs));
}
/** The demo box's password (mobilecore.DemoPassword). */
export const DEMO_BOX_PASSWORD = 'admin';

type DemoState = {
  coOwners: Record<string, boolean>;
  recovered: boolean;
  designate: (membershipId: string) => void;
  remove: (membershipId: string) => void;
  /** Plays the relay's check; throws a RecoveryError as the core would. */
  recover: (relayAddress: string, boxPassword: string) => void;
};

const LAN = /^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|127\.)/;

export const useDemoRecovery = create<DemoState>((set) => ({
  coOwners: {},
  recovered: false,
  designate: (id) => set((s) => ({ coOwners: { ...s.coOwners, [id]: true } })),
  remove: (id) => set((s) => ({ coOwners: { ...s.coOwners, [id]: false } })),
  recover: (relayAddress, boxPassword) => {
    if (!LAN.test(relayAddress.trim())) throw new RecoveryError('not_lan', 'demo: not a home address');
    if (boxPassword !== DEMO_BOX_PASSWORD) throw new RecoveryError('proof', 'demo: wrong password');
    set({ recovered: true });
  },
}));
