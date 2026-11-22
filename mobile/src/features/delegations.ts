import { create } from 'zustand';

import { owner, type BundleChange } from '../core/owner';
import { remoteCore, type BundleDelegation, type MemberAccess } from '../core/remote';
import type { HouseholdMember, HubBundle } from '../licensing/api';
import { hub } from '../licensing/hub';
import { withHousehold } from '../licensing/household';

// Member delegations (issue #47, RFC-005 Trust model). The owner delegates
// "voir" or "contrôler" to a member's own key — created in the member
// phone's Go core and published on PUT members/me/key — in the bundle the
// owner's core signs. A delegation lasts 30 days; the owner's app renews it
// a week before the end (renewDue, run with remote mode's detection), so it
// only runs out when the owner's phone is gone. "Réservé au propriétaire"
// (owner_only) pauses every delegation, and "Révoquer" ends one: the relays
// read both from the bundle and refuse a member's command whatever the hub
// lets through. Nothing here holds a key: JS carries, the core signs.

export const delegationsKey = ['hub', 'delegations'] as const;

/** The household's delegations as the core read them from the bundle. */
export type DelegationsView = {
  /** This phone's owner key signed the bundle. */
  owner: boolean;
  ownerOnly: boolean;
  /** A relay is paired: remote access needs one. */
  hasRelay: boolean;
  /** The members' delegations (owner's phone only). */
  delegations: BundleDelegation[];
  /** This phone's own access (a member's phone only). */
  member: MemberAccess | null;
};

const EMPTY: DelegationsView = { owner: false, ownerOnly: false, hasRelay: false, delegations: [], member: null };

/** What the household's bundle delegates, read by the core. */
export function loadDelegations(): Promise<DelegationsView> {
  return withHousehold(async (tok, tenantId) => {
    const bundle = await hub.trustBundle(tok, tenantId);
    if (!bundle) return EMPTY;
    const v = await remoteCore.relays(tenantId, bundle);
    return { owner: v.owner, ownerOnly: v.ownerOnly, hasRelay: v.relays.length > 0, delegations: v.delegations, member: v.member };
  });
}

/** The delegation a member's published key holds, if any. */
export function delegationOf(m: HouseholdMember, view: DelegationsView | undefined): BundleDelegation | undefined {
  const id = m.keys?.keyId;
  return id ? view?.delegations.find((d) => d.keyId === id) : undefined;
}

/** The owner's core signs the next bundle with one change, and the hub stores it. */
export function changeBundle(c: BundleChange): Promise<void> {
  return withHousehold(async (tok, tenantId) => {
    const current = await hub.trustBundle(tok, tenantId);
    const next = await owner.signBundle(tenantId, current, c);
    await hub.putTrustBundle(tok, tenantId, next);
  });
}

export const delegations = {
  /** Grants a member's published key view or control for 30 days. */
  grant: (m: HouseholdMember, scope: 'view' | 'control') => {
    if (!m.keys) return Promise.reject(new Error('this member has not published a key yet'));
    return changeBundle({ delegate: { ed25519: m.keys.ed25519, x25519: m.keys.x25519, scope, label: m.phone } });
  },
  /** Ends a member key's delegation now. */
  revoke: (keyId: string) => changeBundle({ revoke: keyId }),
  /** Keeps the box to the owner, or gives the members their delegations back. */
  setOwnerOnly: (on: boolean) => changeBundle({ ownerOnly: on }),
  /** The six digits the owner's screen shows for a member's key. */
  code: (m: HouseholdMember) => (m.keys ? owner.pairingCode(m.keys.ed25519, m.keys.x25519) : Promise.resolve('')),
  /**
   * A member's phone publishes its member key (created in its core on the
   * first call) when the household does not list it yet. True when sent.
   */
  publishKey: async (you: HouseholdMember): Promise<boolean> => {
    const keys = await remoteCore.memberKeys();
    if (you.keys?.keyId === keys.keyId) return false;
    await withHousehold((tok, tenantId) => hub.putMemberKey(tok, tenantId, { ed25519: keys.ed25519, x25519: keys.x25519 }));
    return true;
  },
};

/**
 * The owner's automatic renewal: when a member's delegation ends within a
 * week, the core signs the next bundle with it moved to 30 days from now,
 * and the hub stores it. Returns the bundle now in force. A renewal that
 * fails leaves the current bundle: the next detection tries again, and a
 * week is many detections.
 */
export async function renewDue(tok: string, tenantId: string, bundle: HubBundle): Promise<HubBundle> {
  try {
    const next = await owner.renewDelegations(tenantId, bundle);
    if (!next) return bundle;
    await hub.putTrustBundle(tok, tenantId, next);
    return next;
  } catch {
    return bundle;
  }
}

/** "482 913": six digits as both screens show them. */
export const spacedCode = (code: string) => (code.length === 6 ? `${code.slice(0, 3)} ${code.slice(3)}` : code);

// The demo has no hub and nothing is signed: the example household's
// members can be given "voir" or "contrôler", see their 30 days, be revoked,
// and the household made owner-only, in memory only.

type DemoState = {
  byMember: Record<string, BundleDelegation | undefined>;
  ownerOnly: boolean;
  grant: (membershipId: string, scope: 'view' | 'control', now?: number) => void;
  revoke: (membershipId: string) => void;
  setOwnerOnly: (on: boolean) => void;
};

const DAY_MS = 24 * 3600 * 1000;

export const useDemoDelegations = create<DemoState>((set) => ({
  byMember: {},
  ownerOnly: false,
  grant: (membershipId, scope, now = Date.now()) =>
    set((s) => ({
      byMember: {
        ...s.byMember,
        [membershipId]: { keyId: `demo-${membershipId}`, scope, expiresAt: new Date(now + 30 * DAY_MS).toISOString() },
      },
    })),
  revoke: (membershipId) => set((s) => ({ byMember: { ...s.byMember, [membershipId]: undefined } })),
  setOwnerOnly: (on) => set({ ownerOnly: on }),
}));

/** The demo members' codes: made up, shown the way a real one is. */
export const DEMO_CODES: Record<string, string> = { 'demo-2': '482913', 'demo-3': '730254' };
