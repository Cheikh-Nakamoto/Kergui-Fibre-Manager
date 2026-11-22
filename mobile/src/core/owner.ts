import KerguiCore from '../../modules/kergui-core';

// The owner's keys (issue #43, RFC-005 Trust model). They are generated in
// the Go core and kept in its AES-GCM vault, beside the box password: what
// reaches JS is the public halves, the QR payload, the six digits and signed
// bundles, never a private key. Everything the core answers is read field by
// field, so nothing it might add later leaks into the app's state by itself.

/** The owner's public keys: what the QR fingerprint and the bundle name. */
export type OwnerKeys = { keyId: string; ed25519: string; x25519: string };

/** A signed trust bundle, the body of PUT …/trust-bundle. */
export type SignedBundle = { payload: string; sig: string; version: number };

/** A relay as the owner signs it into the bundle. */
export type RelayToSign = { agentId: string; kind: string; label?: string; ed25519: string; x25519: string };

/** The box password sealed for one relay: ciphertext, the body of PUT …/credentials. */
export type SealedForRelay = { agentId: string; blob: string; version: number };

/**
 * What the core sealed: for every relay of the owner's bundle (or the one
 * asked for), and whether it holds a box password at all.
 */
export type SealedCredentials = { version: number; boxPassword: boolean; relays: SealedForRelay[] };

/** The relay the demo plays, with the code its own screen would show. */
export type DemoRelay = RelayToSign & { name: string; code: string };

export class OwnerKeyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'OwnerKeyError';
  }
}

function parse(raw: string): Record<string, unknown> {
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    throw new OwnerKeyError('the core answered something that is not JSON');
  }
  if (!data || typeof data !== 'object') throw new OwnerKeyError('the core answered something that is not an object');
  return data as Record<string, unknown>;
}

function str(o: Record<string, unknown>, k: string): string {
  const v = o[k];
  if (typeof v !== 'string' || !v) throw new OwnerKeyError(`the core's answer has no ${k}`);
  return v;
}

const SIX_DIGITS = /^\d{6}$/;

export const owner = {
  /** The owner's public keys, created once, the first time they are asked for. */
  keys: async (): Promise<OwnerKeys> => {
    const o = parse(await KerguiCore.ownerKeys());
    return { keyId: str(o, 'keyId'), ed25519: str(o, 'ed25519'), x25519: str(o, 'x25519') };
  },

  /** What the QR shows for a pairing code: the code and the owner's fingerprint. */
  invite: (code: string): Promise<string> => KerguiCore.pairingInvite(code),

  /** The six digits for a relay's keys as the hub listed them. */
  pairingCode: async (relayEd25519: string, relayX25519: string): Promise<string> => {
    const code = await KerguiCore.pairingCode(relayEd25519, relayX25519);
    if (!SIX_DIGITS.test(code)) throw new OwnerKeyError('the core did not answer six digits');
    return code;
  },

  /**
   * The household's next trust bundle, signed in the core. `current` is the
   * hub's bundle (null when it has none); the version is always above it
   * and above the last one this phone signed.
   */
  signBundle: async (
    tenantId: string,
    current: SignedBundle | null,
    change: { relay?: RelayToSign; routerPin?: string } = {},
  ): Promise<SignedBundle> => {
    const o = parse(await KerguiCore.signTrustBundle(tenantId, current ? JSON.stringify(current) : '', JSON.stringify(change)));
    const version = o.version;
    if (typeof version !== 'number' || version < 1) throw new OwnerKeyError("the core's bundle has no version");
    return { payload: str(o, 'payload'), sig: str(o, 'sig'), version };
  },

  /**
   * The box password the core logged in with, sealed in the core for the
   * relays of the owner's bundle (or only agentId). What comes back is
   * ciphertext only a relay opens; the password itself never crosses the
   * bridge. The version moves when the password does.
   */
  sealCredentials: async (tenantId: string, bundle: SignedBundle, agentId = ''): Promise<SealedCredentials> => {
    const o = parse(await KerguiCore.sealCredentials(tenantId, JSON.stringify(bundle), agentId));
    const version = o.version;
    if (typeof version !== 'number' || version < 0) throw new OwnerKeyError("the core's sealed credentials have no version");
    if (typeof o.boxPassword !== 'boolean' || !Array.isArray(o.relays)) throw new OwnerKeyError("the core's sealed credentials are incomplete");
    const relays = o.relays.map((r: unknown) => {
      if (!r || typeof r !== 'object') throw new OwnerKeyError("the core's sealed credentials are incomplete");
      const e = r as Record<string, unknown>;
      if (typeof e.version !== 'number' || e.version < 1) throw new OwnerKeyError("a sealed blob has no version");
      return { agentId: str(e, 'agentId'), blob: str(e, 'blob'), version: e.version };
    });
    return { version, boxPassword: o.boxPassword, relays };
  },

  /** Demo only: the relay's side of pairing, played by the core on the QR. */
  demoRelay: async (invite: string): Promise<DemoRelay> => {
    const o = parse(await KerguiCore.demoRelayRedeem(invite));
    return {
      agentId: str(o, 'agentId'),
      name: str(o, 'name'),
      kind: str(o, 'kind'),
      ed25519: str(o, 'ed25519'),
      x25519: str(o, 'x25519'),
      code: str(o, 'code'),
    };
  },
};
