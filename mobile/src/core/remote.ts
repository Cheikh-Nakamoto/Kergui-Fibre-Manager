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

/** Whether this phone is the owner's and which relays its bundle names. */
export type RemoteRelays = { owner: boolean; relays: RemoteRelay[] };

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

const message = (e: unknown) => (e instanceof Error ? e.message : String(e));

async function ask(run: () => Promise<string>): Promise<Record<string, unknown>> {
  let raw: string;
  try {
    raw = await run();
  } catch (e) {
    const m = message(e);
    // The core's paid-action gate: a lock, never an error.
    if (m.includes(NOT_ENTITLED)) throw new ApiError(403, m, NOT_ENTITLED);
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
    return {
      owner: o.owner === true,
      relays: list
        .filter((r): r is Record<string, unknown> => !!r && typeof r === 'object')
        .map((r) => ({ agentId: String(r.agentId ?? ''), kind: String(r.kind ?? ''), label: typeof r.label === 'string' ? r.label : undefined }))
        .filter((r) => r.agentId),
    };
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
  | 'box';

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
