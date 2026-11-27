import KerguiCore from '../../modules/kergui-core';
import type { SignedBundle } from './owner';

// Recovery of a lost owner phone in the Go core (issue #48, RFC-005 "Lost
// owner phone"). Two ways to hand the household to this phone's owner key:
//
//   - a co-owner: the owner signed this phone's member key in advance, and
//     that key endorses this phone's owner key;
//   - at home, on the Wi-Fi: this phone proves the box password to a relay,
//     which endorses this phone's owner key. The proof is argon2id-stretched
//     and only ever sent to an address on the home network: never through
//     the hub.
//
// Either way the core signs the household's next bundle, the old owner key
// revoked, and JS uploads it like any other. Nothing here holds a key.

/** What a recovery hands back: the bundle to PUT, and the old owner key now refused. */
export type Recovered = { tenantId: string; bundle: SignedBundle; revoked: string };

/** Why a recovery did not happen. */
export type RecoveryFailure =
  | 'not_co_owner'
  | 'not_lan'
  | 'proof'
  | 'rate_limited'
  | 'not_ready'
  | 'unreachable'
  | 'already_owner'
  | 'other_household'
  | 'other';

export class RecoveryError extends Error {
  constructor(
    readonly reason: RecoveryFailure,
    message: string,
  ) {
    super(message);
    this.name = 'RecoveryError';
  }
}

// The core's message prefixes (mobilecore/recovery.go).
const PREFIXES: [string, RecoveryFailure][] = [
  ['not_co_owner', 'not_co_owner'],
  ['recovery_not_lan', 'not_lan'],
  ['recovery_proof', 'proof'],
  ['recovery_rate_limited', 'rate_limited'],
  ['recovery_not_ready', 'not_ready'],
  ['recovery_unreachable', 'unreachable'],
  ['recovery_already_owner', 'already_owner'],
];

export function recoveryFailure(message: string): RecoveryFailure {
  for (const [prefix, reason] of PREFIXES) if (message.includes(prefix)) return reason;
  return 'other';
}

async function ask(run: () => Promise<string>): Promise<Recovered> {
  let raw: string;
  try {
    raw = await run();
  } catch (e) {
    const m = e instanceof Error ? e.message : String(e);
    throw new RecoveryError(recoveryFailure(m), m);
  }
  let o: Record<string, unknown>;
  try {
    o = JSON.parse(raw) as Record<string, unknown>;
  } catch {
    throw new RecoveryError('other', 'the core answered something that is not JSON');
  }
  const b = (o?.bundle ?? {}) as Record<string, unknown>;
  const ok =
    typeof o?.tenantId === 'string' &&
    o.tenantId &&
    typeof b.payload === 'string' &&
    typeof b.sig === 'string' &&
    typeof b.version === 'number' &&
    b.version > 0;
  if (!ok) throw new RecoveryError('other', "the core's recovery answer is incomplete");
  return {
    tenantId: o.tenantId as string,
    bundle: { payload: b.payload as string, sig: b.sig as string, version: b.version as number },
    revoked: typeof o.revoked === 'string' ? o.revoked : '',
  };
}

/** One relay of the household on the home Wi-Fi (#117). */
export type FoundRelay = { address: string; agentId: string; version: number };
/** The search's answer: the household's relays, the best first; others are not the household's. */
export type FoundRelays = { relays: FoundRelay[]; others: number };

function parseFound(raw: string): FoundRelays {
  let o: Record<string, unknown>;
  try {
    o = JSON.parse(raw) as Record<string, unknown>;
  } catch {
    throw new RecoveryError('other', 'the core answered something that is not JSON');
  }
  const list = Array.isArray(o?.relays) ? (o.relays as Record<string, unknown>[]) : [];
  const relays = list
    .filter((r) => typeof r?.address === 'string' && r.address !== '')
    .map((r) => ({
      address: r.address as string,
      agentId: typeof r.agentId === 'string' ? r.agentId : '',
      version: typeof r.version === 'number' ? r.version : 0,
    }));
  return { relays, others: typeof o?.others === 'number' ? o.others : 0 };
}

export const recoveryCore = {
  /**
   * « Récupérer à la maison » without an address (#117): the core asks the
   * home Wi-Fi for the relays of the household whose bundle (the hub's) the
   * owner signed. Nothing secret is sent.
   */
  findRelays: async (tenantId: string, bundle: unknown): Promise<FoundRelays> => {
    let raw: string;
    try {
      raw = await KerguiCore.findRelaysAtHome(tenantId, bundle ? JSON.stringify(bundle) : '');
    } catch (e) {
      const m = e instanceof Error ? e.message : String(e);
      throw new RecoveryError(recoveryFailure(m), m);
    }
    return parseFound(raw);
  },
  /** The co-owner path: this phone takes over the household whose bundle names its member key as a co-owner. */
  takeOver: (tenantId: string, bundle: unknown): Promise<Recovered> =>
    ask(() => KerguiCore.takeOverHousehold(tenantId, bundle ? JSON.stringify(bundle) : '')),
  /** The path at home: proves the box password to the relay at that address on the Wi-Fi. */
  atHome: (relayAddress: string, boxPassword: string): Promise<Recovered> =>
    ask(() => KerguiCore.recoverAtHome(relayAddress.trim(), boxPassword)),
};
