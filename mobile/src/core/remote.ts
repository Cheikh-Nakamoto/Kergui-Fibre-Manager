import KerguiCore from '../../modules/kergui-core';
import { ApiError, NOT_ENTITLED } from './client';
import type { Device } from './types';

// Remote mode in the Go core (issue #46, RFC-005 design rule 1). JS carries
// what the hub answers; the core decides whether to believe it: the relays
// come from the bundle the owner's key signed, a device list is shown only
// once a relay's signature over it checks, a command is signed with the
// owner's key after the paid-action gate, and its outcome is believed only
// once the relay's signature over it checks. Nothing here holds a key.

/** A relay of the owner's signed bundle. */
export type RemoteRelay = { agentId: string; kind: string; label?: string };

/** A member delegation of the owner's bundle (issue #47). */
export type BundleDelegation = { keyId: string; scope: 'view' | 'control'; expiresAt: string; label?: string };

/** What the bundle holds for this phone's member key. */
export type MemberState = 'none' | 'active' | 'expired' | 'revoked' | 'other_owner';
export type MemberAccess = {
  keyId: string;
  state: MemberState;
  scope?: 'view' | 'control';
  expiresAt?: string;
  /** May sign block / unblock now: control, and the household not owner-only. */
  control: boolean;
  /** The six digits the owner's screen shows for this phone's key. */
  code?: string;
};

/**
 * Whether this phone is the owner's and which relays its bundle names; for
 * the owner, the members' delegations; for another phone, its own access.
 */
export type RemoteRelays = {
  owner: boolean;
  ownerOnly: boolean;
  relays: RemoteRelay[];
  delegations: BundleDelegation[];
  member: MemberAccess | null;
};

/** A device list a relay of the owner signed. */
export type VerifiedDevices = { agentId: string; asOf: string; devices: Device[] };

/** A command the owner's core signed: payload and sig go to the hub. */
export type SignedDeviceCommand = { payload: string; sig: string; cmdId: string; mac: string; expiresAt: string };

/** A command outcome a relay of the owner signed. */
export type VerifiedResult = { agentId: string; status: number; body?: unknown; executedAt: string };

/** The core refused to believe what the hub served (bad signature, unknown relay…). */
export class RemoteTrustError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'RemoteTrustError';
  }
}

const OWNER_ONLY = 'owner_only';
const NOT_DELEGATED = 'not_delegated';

const scopeOf = (v: unknown): 'view' | 'control' | undefined => (v === 'view' || v === 'control' ? v : undefined);
const MEMBER_STATES: readonly MemberState[] = ['none', 'active', 'expired', 'revoked', 'other_owner'];

function memberAccess(v: unknown): MemberAccess | null {
  if (!v || typeof v !== 'object') return null;
  const o = v as Record<string, unknown>;
  const state = MEMBER_STATES.includes(o.state as MemberState) ? (o.state as MemberState) : 'none';
  return {
    keyId: typeof o.keyId === 'string' ? o.keyId : '',
    state,
    scope: scopeOf(o.scope),
    expiresAt: typeof o.expiresAt === 'string' && o.expiresAt ? o.expiresAt : undefined,
    control: state === 'active' && o.control === true,
    code: typeof o.code === 'string' && /^\d{6}$/.test(o.code) ? o.code : undefined,
  };
}

const message = (e: unknown) => (e instanceof Error ? e.message : String(e));

async function ask(run: () => Promise<string>): Promise<Record<string, unknown>> {
  let raw: string;
  try {
    raw = await run();
  } catch (e) {
    const m = message(e);
    // The core's paid-action gate: a lock, never an error.
    if (m.includes(NOT_ENTITLED)) throw new ApiError(403, m, NOT_ENTITLED);
    // A member's core refusing to sign (issue #47): said, not shown as an error.
    if (m.includes(OWNER_ONLY)) throw new RemoteCommandError('owner_only');
    if (m.includes(NOT_DELEGATED)) throw new RemoteCommandError('not_delegated');
    throw new RemoteTrustError(m);
  }
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    throw new RemoteTrustError('the core answered something that is not JSON');
  }
  if (!data || typeof data !== 'object') throw new RemoteTrustError('the core answered something that is not an object');
  return data as Record<string, unknown>;
}

function str(o: Record<string, unknown>, k: string): string {
  const v = o[k];
  if (typeof v !== 'string' || !v) throw new RemoteTrustError(`the core's answer has no ${k}`);
  return v;
}

export const remoteCore = {
  relays: async (tenantId: string, bundle: unknown): Promise<RemoteRelays> => {
    const o = await ask(() => KerguiCore.remoteRelays(tenantId, bundle ? JSON.stringify(bundle) : ''));
    const list = Array.isArray(o.relays) ? o.relays : [];
    const dels = Array.isArray(o.delegations) ? o.delegations : [];
    const owner = o.owner === true;
    return {
      owner,
      ownerOnly: o.ownerOnly === true,
      relays: list
        .filter((r): r is Record<string, unknown> => !!r && typeof r === 'object')
        .map((r) => ({ agentId: String(r.agentId ?? ''), kind: String(r.kind ?? ''), label: typeof r.label === 'string' ? r.label : undefined }))
        .filter((r) => r.agentId),
      delegations: dels
        .filter((d): d is Record<string, unknown> => !!d && typeof d === 'object')
        .flatMap((d) => {
          const scope = scopeOf(d.scope);
          const keyId = typeof d.keyId === 'string' ? d.keyId : '';
          const expiresAt = typeof d.expiresAt === 'string' ? d.expiresAt : '';
          if (!scope || !keyId || !expiresAt) return [];
          return [{ keyId, scope, expiresAt, label: typeof d.label === 'string' && d.label ? d.label : undefined }];
        }),
      member: owner ? null : memberAccess(o.member),
    };
  },

  /** This phone's member key (issue #47), created in the core on the first call. */
  memberKeys: async (): Promise<{ keyId: string; ed25519: string; x25519: string }> => {
    const o = await ask(() => KerguiCore.memberKeys());
    return { keyId: str(o, 'keyId'), ed25519: str(o, 'ed25519'), x25519: str(o, 'x25519') };
  },

  /** A member's block / unblock, signed with this phone's member key under the owner's delegation. */
  signMember: async (tenantId: string, bundle: unknown, mac: string, action: 'block' | 'unblock', boxId: string): Promise<SignedDeviceCommand> => {
    const o = await ask(() => KerguiCore.signMemberCommand(tenantId, JSON.stringify(bundle), mac, action, boxId));
    return { payload: str(o, 'payload'), sig: str(o, 'sig'), cmdId: str(o, 'cmdId'), mac: str(o, 'mac'), expiresAt: str(o, 'expiresAt') };
  },

  devices: async (tenantId: string, bundle: unknown, snapshot: unknown): Promise<VerifiedDevices> => {
    const o = await ask(() => KerguiCore.openDeviceSnapshot(tenantId, JSON.stringify(bundle), JSON.stringify(snapshot)));
    if (!Array.isArray(o.devices)) throw new RemoteTrustError("the core's device list is not a list");
    return { agentId: str(o, 'agentId'), asOf: str(o, 'asOf'), devices: o.devices as Device[] };
  },

  sign: async (tenantId: string, mac: string, action: 'block' | 'unblock', boxId: string): Promise<SignedDeviceCommand> => {
    const o = await ask(() => KerguiCore.signDeviceCommand(tenantId, mac, action, boxId));
    return { payload: str(o, 'payload'), sig: str(o, 'sig'), cmdId: str(o, 'cmdId'), mac: str(o, 'mac'), expiresAt: str(o, 'expiresAt') };
  },

  verify: async (tenantId: string, cmdId: string, bundle: unknown, result: unknown): Promise<VerifiedResult> => {
    const o = await ask(() => KerguiCore.verifyCommandResult(tenantId, cmdId, JSON.stringify(bundle), JSON.stringify(result)));
    if (typeof o.status !== 'number') throw new RemoteTrustError("the core's result has no status");
    return { agentId: str(o, 'agentId'), status: o.status, body: o.body, executedAt: str(o, 'executedAt') };
  },
};

/** Why a remote block / unblock did not happen. */
export type RemoteFailure =
  /** The hub has not heard the relay lately: nothing was sent. */
  | 'relay_offline'
  /** No relay ever reported: nothing was sent. */
  | 'relay_none'
  /** Sent, but no relay picked it up before it expired. */
  | 'expired'
  /** The relay refused it (its checks, not the box). */
  | 'refused'
  /** The relay ran it and the box refused. */
  | 'box'
  /** A member's phone: the household is owner-only, nothing was signed (issue #47). */
  | 'owner_only'
  /** A member's phone: no control delegation (view only, expired, revoked), nothing was signed. */
  | 'not_delegated';

export class RemoteCommandError extends Error {
  constructor(
    readonly reason: RemoteFailure,
    /** When the relay was last heard, for relay_offline. */
    readonly since?: string,
    /** What the box answered, for box. */
    readonly status?: number,
  ) {
    super(`remote command: ${reason}`);
    this.name = 'RemoteCommandError';
  }
}
