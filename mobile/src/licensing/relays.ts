import { owner, type RelayToSign } from '../core/owner';
import type { HubAgent } from './api';
import { hub } from './hub';
import { withHousehold } from './household';

// Pairing a relay (issue #43, RFC-005 "Pairing does not trust the hub"):
//
// 1. the household gets a first trust bundle signed by the owner's core if it
//    has none, so the relay has a key to pin;
// 2. the hub hands out a one-shot code and the QR shows it with the owner's
//    key fingerprint;
// 3. the relay redeems it and appears in the household's agents; the owner's
//    screen derives six digits from its keys as the hub lists them, the
//    relay's screen from the owner's keys it pinned;
// 4. same digits: the owner's core signs the relay into the next bundle and
//    the app uploads it. Different digits: something swapped a key, and the
//    relay is removed at once.

/** What a relay is to the household: a computer, or a phone left at home. */
export type RelayKind = 'pc' | 'dedicated';

/** What the QR screen needs. */
export type PairingStarted = {
  /** The QR payload (kgp1.…): the pairing code and the owner's fingerprint. */
  invite: string;
  expiresAt: string;
  /** The relays already there, so the new one is the one not in this list. */
  known: string[];
};

export const relays = {
  /**
   * Prepares the household and issues a pairing code. routerPin is the box
   * certificate the relays must stand in front of, when the app knows it.
   */
  start: (kind: RelayKind, routerPin = ''): Promise<PairingStarted> =>
    withHousehold(async (tok, tenantId) => {
      const current = await hub.trustBundle(tok, tenantId);
      if (!current) {
        const first = await owner.signBundle(tenantId, null, routerPin ? { routerPin } : {});
        await hub.putTrustBundle(tok, tenantId, first);
      }
      if (routerPin) {
        const net = await hub.network(tok, tenantId);
        if (!net?.routerPin) await hub.putNetwork(tok, tenantId, { ssids: net?.ssids ?? [], bssids: net?.bssids ?? [], routerPin });
      }
      const known = (await hub.agents(tok, tenantId)).map((a) => a.id);
      const issued = await hub.issuePairing(tok, tenantId, kind);
      return { invite: await owner.invite(issued.code), expiresAt: issued.expiresAt, known };
    }),

  /** The relay that redeemed the code, once it has: one not known before, not revoked. */
  arrived: (known: string[]): Promise<HubAgent | null> =>
    withHousehold(async (tok, tenantId) => {
      const list = await hub.agents(tok, tenantId);
      return list.find((a) => !known.includes(a.id) && !a.revokedAt) ?? null;
    }),

  /** The six digits the owner's screen shows for that relay. */
  code: (agent: Pick<HubAgent, 'ed25519' | 'x25519'>): Promise<string> => owner.pairingCode(agent.ed25519, agent.x25519),

  /** "Oui, c'est le même code": the relay is signed into the next bundle. */
  confirm: (agent: Pick<HubAgent, 'id' | 'kind' | 'name' | 'ed25519' | 'x25519'>): Promise<number> =>
    withHousehold(async (tok, tenantId) => {
      const relay: RelayToSign = { agentId: agent.id, kind: agent.kind, label: agent.name, ed25519: agent.ed25519, x25519: agent.x25519 };
      const next = await owner.signBundle(tenantId, await hub.trustBundle(tok, tenantId), { relay });
      await hub.putTrustBundle(tok, tenantId, next);
      return next.version;
    }),

  /** "Non, le code est différent": the relay is removed, nothing is signed. */
  reject: (agent: Pick<HubAgent, 'id'>): Promise<void> => withHousehold((tok, tenantId) => hub.revokeAgent(tok, tenantId, agent.id)),
};
