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

/** A member delegation as the owner signs it (issue #47): the member phone's keys, base64. */
export type DelegationToSign = { ed25519: string; x25519: string; scope: 'view' | 'control'; label?: string };

/** What the owner asks to change in the next bundle; every part optional. */
export type BundleChange = {
  relay?: RelayToSign;
  routerPin?: string;
  /** Grants a member's key view or control for 30 days. */
  delegate?: DelegationToSign;
  /** A member key id whose delegation ends now. */
  revoke?: string;
  /** Keeps the box to the owner (true), or gives the members their delegations back (false). */
  ownerOnly?: boolean;
  /** Signs a member's key in as a co-owner, who may take the household over if this phone is lost (#48). */
  coOwner?: { ed25519: string; x25519: string; label?: string };
  /** A co-owner key id to drop. */
  removeCoOwner?: string;
  /**
   * Delegates hub_block to the hub's narrow key (#49): WhatsApp's
   * "Répondre 1 pour bloquer" on. Off is `revoke` with its key id.
   */
  hubKey?: { ed25519: string; x25519: string; label?: string };
  /** Gives a paired relay the owner's name for it (#57): only its label changes. */
  renameRelay?: { agentId: string; label: string };
};

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

function signedBundle(o: Record<string, unknown>): SignedBundle {
  const version = o.version;
  if (typeof version !== 'number' || version < 1) throw new OwnerKeyError("the core's bundle has no version");
  return { payload: str(o, 'payload'), sig: str(o, 'sig'), version };
}

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
    change: BundleChange = {},
  ): Promise<SignedBundle> => {
    const o = parse(await KerguiCore.signTrustBundle(tenantId, current ? JSON.stringify(current) : '', JSON.stringify(change)));
    return signedBundle(o);
  },

  /**
   * The owner's app keeping the members' delegations alive (issue #47): the
   * next bundle with every one due within a week renewed for 30 days, or
   * null when none is due. Signed in the core, like every bundle.
   */
  renewDelegations: async (tenantId: string, current: SignedBundle): Promise<SignedBundle | null> => {
    const raw = await KerguiCore.renewDelegations(tenantId, JSON.stringify(current));
    return raw ? signedBundle(parse(raw)) : null;
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

  /**
   * The household's next relay policy (#36), signed in the core from its
   * own auto-reject rule and entitlement token: the body of PUT
   * …/relay-policy. null when there is nothing to send: this phone's owner
   * key did not sign `bundle` (not the owner's phone), or `current` (the
   * hub's policy, null for none) already says the same.
   */
  signRelayPolicy: async (tenantId: string, bundle: SignedBundle, current: SignedBundle | null): Promise<SignedBundle | null> => {
    const raw = await KerguiCore.signRelayPolicy(tenantId, JSON.stringify(bundle), current ? JSON.stringify(current) : '');
    return raw ? signedBundle(parse(raw)) : null;
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
