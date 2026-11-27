import type { Tier } from './gate';

// Typed client of the hub's licensing API (internal/hub/licensing/handler).
// Every route lives under /licensing/v1; the authenticated ones take the
// kgs_ session token as a bearer. The kernel's refresh and logout
// (/api/v1/auth/*) carry the household's refresh token in their body
// instead (issue #41, see hubTokens.ts). Errors come back as
// {"error": {"code", "message"}}; `code` names the rule that refused, so the
// app can word it (see hubErrorMessage in src/i18n).

/**
 * The codes the hub's handler answers (handler.go, const block) on the routes
 * this app calls. The app has no payment routes (RFC-006, decision of
 * 2026-11-21); any other code falls back to a generic sentence.
 */
export const HUB_ERROR_CODES = [
  'INVALID_PHONE',
  'INVALID_BOX',
  'OTP_RATE_LIMITED',
  'OTP_INVALID',
  'OTP_EXPIRED',
  'OTP_LOCKED',
  'SESSION_INVALID',
  'NO_ACTIVE_PASS',
  'SAME_BOX',
  'TARGET_BOX_ACTIVE',
  'TRANSFER_LIMIT',
  'WHATSAPP_UNAVAILABLE',
  // Complaints (issue #82, internal/hub/complaints/http.go).
  'COMPLAINT_INVALID',
  'COMPLAINT_NOT_FOUND',
  'COMPLAINT_CLOSED',
  'COMPLAINT_RATE_LIMITED',
  // The household members routes (issue #42, internal/hub/identity/handler.go),
  // and the kernel's own refusal of a household the caller is not in.
  'ALREADY_MEMBER',
  'OWNER_ONLY',
  'OWNER_UNTOUCHED',
  'INVITE_LIMIT',
  'INVALID_ROLE',
  'INVALID_KEY',
  'FORBIDDEN',
  // The hub's per-address limit on the licensing routes: the kernel's
  // middleware answers it with no code of its own.
  'HTTP_429',
  // Raised by this client, never by the hub.
  'NETWORK',
  'NOT_CONFIGURED',
  'BAD_RESPONSE',
] as const;

/** A known code, or whatever else the hub sent (kernel codes, HTTP_<status>). */
export type HubErrorCode = (typeof HUB_ERROR_CODES)[number] | (string & {});

export class HubError extends Error {
  constructor(
    /** HTTP status; 0 when the hub was not reached. */
    readonly status: number,
    readonly code: HubErrorCode,
    message: string,
    /** Seconds to wait, from Retry-After (the code request's rate limit). */
    readonly retryAfter?: number,
  ) {
    super(message);
    this.name = 'HubError';
  }

  /** The session token is unknown, revoked or expired: sign in again. */
  get sessionInvalid() {
    return this.code === 'SESSION_INVALID';
  }

  /** The hub could not be reached: keep what is cached. */
  get offline() {
    return this.code === 'NETWORK';
  }
}

export type OtpRequested = { phone: string; expiresAt: string; resendAt: string };
/**
 * The kernel side of the same sign-in (issue #41): the household the number
 * owns and a token pair for /api/v1/tenants/{tenantId}/... Absent from a hub
 * that does not bridge the two.
 */
export type HubSessionAnswer = { userId: string; tenantId: string; accessToken: string; refreshToken: string };
export type HubTokensAnswer = { accessToken: string; refreshToken: string };
export type SignedIn = { sessionToken: string; accountId: string; phone: string; expiresAt: string; hub?: HubSessionAnswer };
export type Subscription = {
  boxId: string;
  tier: Tier;
  effectiveTier: Tier;
  active: boolean;
  expiresAt: string;
  /** How the hub credited it; the app does not act on it. */
  channel: string;
  /** A pass with no end (issue #98): expiresAt is 9999-12-31, shown as « à vie ». Absent from older hubs. */
  lifetime?: boolean;
};
export type TokenAnswer = {
  boxId: string;
  tier: Tier;
  /** Absent on Free: there is nothing to unlock. */
  token?: string;
  expiresAt?: string;
  issuedAt?: string;
  keyId?: string;
  /**
   * The pass has no end (issue #98): the token's expiry is only when to
   * refresh it, so the screens say « à vie » instead of that date. Display
   * only and unsigned: what unlocks is the token.
   */
  lifetime?: boolean;
};
/**
 * The phone signing in on a box. The hub binds whatever waits on the account
 * to that box; the app acts on nothing here and reads the tier from the token.
 */
export type BindAnswer = { boxId: string; subscription: Subscription };
export type PublicKeyAnswer = { algorithm: string; keyId: string; publicKey: string };
/**
 * Every key the hub publishes (signing key rotation). Only `signed`, the
 * same list signed by the hub's active key, is acted on.
 */
export type PublicKeysAnswer = {
  keys: (PublicKeyAnswer & { status: string })[];
  revoked: string[];
  signed: string;
};

/** A household role (issue #42): the kernel's owner / admin / viewer. */
export type MemberRole = 'owner' | 'admin' | 'viewer';
export type HouseholdMember = {
  membershipId: string;
  userId: string;
  role: MemberRole;
  /** Masked for a viewer looking at the others. */
  phone?: string;
  joinedAt?: string;
  /**
   * The public keys this member's phone published (issue #47), base64:
   * listed to the owner, who delegates them, and to the member themself.
   */
  keys?: MemberPublicKeys;
};
/** A member phone's public keys, created in its Go core. */
export type MemberPublicKeys = { keyId: string; ed25519: string; x25519: string };
export type HouseholdInvite = { id: string; phone: string; role: MemberRole; createdAt: string; expiresAt: string };
export type HouseholdMembers = {
  household: { id: string; name: string };
  you: HouseholdMember;
  members: HouseholdMember[];
  /** Only the owner gets the pending invites. */
  invites?: HouseholdInvite[];
};

/** A relay of the household (internal/hub/agents/handler), keys in base64. */
export type HubAgent = {
  id: string;
  name: string;
  kind: string;
  userId?: string;
  keyId: string;
  ed25519: string;
  x25519: string;
  createdAt: string;
  revokedAt?: string;
  lastSeenAt?: string;
};
/** A one-shot pairing code: 128 bits, five minutes. */
export type PairingIssued = { code: string; expiresAt: string };
/** The owner-signed trust bundle as the hub stores it. */
export type HubBundle = { payload: string; sig: string; version: number };
/** The box the relays must stand in front of. */
export type HubNetwork = { ssids: string[] | null; bssids: string[] | null; routerPin: string };

/**
 * GET router/devices (internal/hub/relay/handler): the last device list a
 * relay pushed, as the relay signed it. The core opens `snapshot`; asOf and
 * agentId here are the hub's word only.
 */
/** The household's inventory: only the approvals (#58) and the owner's phones (#87) are read. */
export type HubInventory = {
  approved?: { mac: string; approved_by: string; approved_at: string }[] | null;
  owner_devices?: { mac: string; declared_at: string }[] | null;
};

export type HubDevices = { snapshot: unknown; agentId: string; asOf: string };
/**
 * The household's Kergui backup of a box (#88, internal/hub/backups): the
 * archive of GET /api/export, base64, as the hub merged it from its senders.
 */
export type HubBackupSource = { kind: 'phone' | 'relay'; id: string };
export type HubBackupInfo = { boxId: string; source: HubBackupSource; updatedAt: string; devices: number; months: number };
export type HubBackupFull = HubBackupInfo & { size: number; archive: string; name: string };
export type HubBackupSaved = HubBackupInfo & { size: number; merged: boolean };
/**
 * GET router/auto-reject (#36): "relay" while the household's relay
 * enforces auto-reject, with its last heartbeat's age; "phone" otherwise.
 */
export type HubEnforcer = {
  enforcer: 'phone' | 'relay';
  agentId?: string;
  relayHeartbeatAt?: string;
  relayHeartbeatAgeSeconds?: number;
};
/** GET router/relay: is the household's relay on the box right now (#46). */
/** paused: the relay yielded the box to the owner at home (#94); it is heard and keeps the lease. */
export type HubRelayState = 'active' | 'paused' | 'standby' | 'offline' | 'none';
export type HubRelayStatus = {
  state: HubRelayState;
  agentId?: string;
  lastSeenAt?: string;
  ageSeconds?: number;
  reason?: string;
};
/** POST router/devices/{mac}/block|unblock and GET router/commands/{id}. */
export type HubCommand = {
  commandId: string;
  /** queued | delivered | done | expired */
  status: string;
  /** The relay's signed result, once done: the core verifies it. */
  result?: unknown;
};

/**
 * POST /releases/v1/check (#65, internal/hub/releases): the newest published
 * website APK above the caller's build, or null, and the minimum build.
 * `required` is true when the caller is below that minimum.
 */
export type ReleaseUpdate = {
  versionName: string;
  build: number;
  url: string;
  sha256: string;
  size: number;
  notes: string;
  publishedAt: string;
};
export type ReleaseCheck = { update: ReleaseUpdate | null; minimumBuild: number; required: boolean };
export type ReleaseCheckRequest = { platform: 'android'; channel: 'website'; build: number; installationId: string };

/**
 * A complaint (issue #82, internal/hub/complaints): a household tells Kergui
 * what is wrong. The category is one of these four; the journal is the
 * app's, attached only when the customer agrees (the hub keeps 64 KiB).
 * « compte » (« Mon compte », #118) is the app's word for the pass and
 * account questions: the hub files it under its own category of those,
 * and its answers carry that name, hence a plain string coming back.
 */
export type ComplaintCategory = 'compte' | 'box' | 'application' | 'autre';
export type ComplaintRequest = { category: ComplaintCategory; message: string; journal?: string; source: 'app' };
/** POST complaints: the acknowledgement, with the number the customer quotes. */
export type ComplaintReceipt = { id: string; number: number; status: string; category: string; createdAt: string; hasJournal: boolean };
export type ComplaintMessage = { id: number; from: string; channel: string; body: string; at: string };
/** A complaint as its customer sees it, with its thread. */
export type Complaint = {
  id: string;
  number: number;
  source: string;
  category: string;
  status: string;
  createdAt: string;
  updatedAt: string;
  hasJournal: boolean;
  messages?: ComplaintMessage[];
};

export type HubClientOptions = {
  /** e.g. https://hub.kergui.example; empty = no hub in this build. */
  baseUrl: string;
  /** The current session token, read on every call. */
  session: () => string | null;
  fetch?: typeof fetch;
  timeoutMs?: number;
};

export type HubClient = ReturnType<typeof createHubClient>;

/**
 * GET hub-key (internal/hub/replyblock): the household's narrow key behind
 * WhatsApp's "1 = bloquer", public halves in base64. Disabled on a hub with
 * no KERGUI_HUB_KEY_SEED.
 */
export type HubKeyAnswer = { enabled: boolean; keyId?: string; scope?: string; ed25519?: string; x25519?: string };
/** The messaging provider and the notice its licence may require (none for whatsmeow). */
export type WhatsAppProvider = { name: string; notice?: string };
/** GET whatsapp: who in the household receives the alerts there (numbers masked). */
export type WhatsAppStatus = {
  provider: WhatsAppProvider;
  optIns: { userId: string; phone: string; active: boolean; linkedAt: string }[];
};
/** POST whatsapp/optin: the wa.me link that pre-fills "KERGUI <code>". */
export type WhatsAppInvite = { waLink: string; qrPayload: string; number: string; expiresAt: string; provider: WhatsAppProvider };

function tenantPath(tenantId: string, suffix: string): string {
  return `/api/v1/tenants/${encodeURIComponent(tenantId)}/${suffix}`;
}

function membersPath(tenantId: string, suffix = ''): string {
  return `/api/v1/tenants/${encodeURIComponent(tenantId)}/members${suffix ? `/${suffix}` : ''}`;
}

export function createHubClient(opts: HubClientOptions) {
  const base = opts.baseUrl.trim().replace(/\/+$/, '');
  const timeoutMs = opts.timeoutMs ?? 15_000;

  // auth: true = the kgs_ session, false = none, a string = that bearer (a
  // kernel access token, for the household's routes).
  async function call<T>(method: string, path: string, body?: unknown, auth: boolean | string = true): Promise<T> {
    if (!base) throw new HubError(0, 'NOT_CONFIGURED', 'no hub configured in this build');
    const headers: Record<string, string> = { Accept: 'application/json' };
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (typeof auth === 'string') {
      headers.Authorization = `Bearer ${auth}`;
    } else if (auth) {
      const s = opts.session();
      if (!s) throw new HubError(401, 'SESSION_INVALID', 'not signed in');
      headers.Authorization = `Bearer ${s}`;
    }
    const doFetch = opts.fetch ?? fetch;
    const ctrl = typeof AbortController !== 'undefined' ? new AbortController() : null;
    const timer = ctrl ? setTimeout(() => ctrl.abort(), timeoutMs) : null;
    let res: Response;
    try {
      // A path from the root is a kernel route; the others are licensing ones.
      const url = path.startsWith('/') ? `${base}${path}` : `${base}/licensing/v1/${path}`;
      res = await doFetch(url, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: ctrl?.signal,
      });
    } catch (e) {
      throw new HubError(0, 'NETWORK', e instanceof Error ? e.message : String(e));
    } finally {
      if (timer) clearTimeout(timer);
    }

    let text = '';
    try {
      text = await res.text();
    } catch {
      text = '';
    }
    let data: unknown = null;
    let parsed = false;
    if (text) {
      try {
        data = JSON.parse(text);
        parsed = true;
      } catch {
        data = null;
      }
    }

    if (res.status >= 400) {
      const err = (data as { error?: { code?: unknown; message?: unknown } } | null)?.error;
      const code = typeof err?.code === 'string' && err.code ? err.code : `HTTP_${res.status}`;
      const message = typeof err?.message === 'string' ? err.message : `HTTP ${res.status}`;
      const ra = Number(res.headers.get('Retry-After'));
      throw new HubError(res.status, code, message, Number.isFinite(ra) && ra > 0 ? ra : undefined);
    }
    if (res.status === 204) return undefined as T;
    if (text && !parsed) throw new HubError(res.status, 'BAD_RESPONSE', 'the hub answered something that is not JSON');
    return data as T;
  }

  return {
    requestOtp: (phone: string) => call<OtpRequested>('POST', 'otp', { phone }, false),
    verifyOtp: (phone: string, code: string, device: string) =>
      call<SignedIn>('POST', 'otp/verify', { phone, code, device }, false),
    signOut: () => call<void>('DELETE', 'session'),
    /** Erases the account and everything the hub keeps for it (#37). */
    deleteAccount: () => call<void>('DELETE', 'account'),
    /** A new kernel pair for the household, with the licensing session. */
    hubSession: () => call<HubSessionAnswer>('POST', 'session/hub'),
    /** The kernel's rolling refresh: the presented token is spent. */
    refreshHubTokens: (refreshToken: string) =>
      call<HubTokensAnswer>('POST', '/api/v1/auth/refresh', { refreshToken }, false),
    signOutHub: (refreshToken: string) => call<void>('POST', '/api/v1/auth/logout', { refreshToken }, false),
    publicKey: () => call<PublicKeyAnswer>('GET', 'public-key', undefined, false),
    publicKeys: () => call<PublicKeysAnswer>('GET', 'public-keys', undefined, false),
    subscriptions: async () => (await call<{ subscriptions?: Subscription[] }>('GET', 'subscriptions'))?.subscriptions ?? [],
    bindBox: (boxId: string) => call<BindAnswer>('POST', 'bind', { boxId }),
    token: (boxId: string) => call<TokenAnswer>('POST', 'token', { boxId }),
    transfer: (fromBoxId: string, toBoxId: string) => call<Subscription>('POST', 'transfer', { fromBoxId, toBoxId }),

    // The household's members (issue #42), with a kernel access token.
    members: (token: string, tenantId: string) => call<HouseholdMembers>('GET', membersPath(tenantId), undefined, token),
    inviteMember: (token: string, tenantId: string, phone: string, role: MemberRole) =>
      call<HouseholdInvite>('POST', membersPath(tenantId, 'invites'), { phone, role }, token),
    cancelInvite: (token: string, tenantId: string, inviteId: string) =>
      call<void>('DELETE', membersPath(tenantId, `invites/${encodeURIComponent(inviteId)}`), undefined, token),
    changeRole: (token: string, tenantId: string, membershipId: string, role: MemberRole) =>
      call<HouseholdMember>('PATCH', membersPath(tenantId, encodeURIComponent(membershipId)), { role }, token),
    removeMember: (token: string, tenantId: string, membershipId: string) =>
      call<void>('DELETE', membersPath(tenantId, encodeURIComponent(membershipId)), undefined, token),
    /** This phone's member key (issue #47), for the owner to delegate. */
    putMemberKey: (token: string, tenantId: string, keys: { ed25519: string; x25519: string }) =>
      call<void>('PUT', membersPath(tenantId, 'me/key'), keys, token),

    // The household's relays and trust material (issue #43,
    // internal/hub/agents/handler). The hub only stores what the owner's
    // core signed; the relays check it against the owner's key.
    issuePairing: (token: string, tenantId: string, kind: string) =>
      call<PairingIssued>('POST', tenantPath(tenantId, 'agents/pairing'), { kind }, token),
    agents: async (token: string, tenantId: string) =>
      (await call<{ agents?: HubAgent[] }>('GET', tenantPath(tenantId, 'agents'), undefined, token))?.agents ?? [],
    revokeAgent: (token: string, tenantId: string, agentId: string) =>
      call<void>('DELETE', tenantPath(tenantId, `agents/${encodeURIComponent(agentId)}`), undefined, token),
    /** The stored bundle, or null when the household has none yet. */
    trustBundle: (token: string, tenantId: string) =>
      call<HubBundle>('GET', tenantPath(tenantId, 'trust-bundle'), undefined, token).catch((e: unknown) => {
        if (e instanceof HubError && e.status === 404) return null;
        throw e;
      }),
    putTrustBundle: (token: string, tenantId: string, bundle: HubBundle) =>
      call<void>('PUT', tenantPath(tenantId, 'trust-bundle'), bundle, token),
    /**
     * The relay policy the owner signed (#36): the auto-reject rule and the
     * entitlement token for the household's relays. null when there is none.
     */
    relayPolicy: (token: string, tenantId: string) =>
      call<HubBundle>('GET', tenantPath(tenantId, 'relay-policy'), undefined, token).catch((e: unknown) => {
        if (e instanceof HubError && e.status === 404) return null;
        throw e;
      }),
    /** A 409 means the hub holds a newer version already. */
    putRelayPolicy: (token: string, tenantId: string, policy: HubBundle) =>
      call<void>('PUT', tenantPath(tenantId, 'relay-policy'), policy, token),
    /** Who enforces auto-reject now (#36): 'relay' while the household's relay does. */
    autoRejectEnforcer: (token: string, tenantId: string) =>
      call<HubEnforcer>('GET', tenantPath(tenantId, 'router/auto-reject'), undefined, token),
    /**
     * The box password sealed for one relay (#44): ciphertext the hub
     * stores and cannot open. A 409 means this version is already there.
     */
    putCredentials: (token: string, tenantId: string, agentId: string, sealed: { blob: string; version: number }) =>
      call<void>('PUT', tenantPath(tenantId, `agents/${encodeURIComponent(agentId)}/credentials`), sealed, token),
    /** The registered network, or null when there is none yet. */
    network: (token: string, tenantId: string) =>
      call<HubNetwork>('GET', tenantPath(tenantId, 'network'), undefined, token).catch((e: unknown) => {
        if (e instanceof HubError && e.status === 404) return null;
        throw e;
      }),
    putNetwork: (token: string, tenantId: string, network: HubNetwork) =>
      call<void>('PUT', tenantPath(tenantId, 'network'), network, token),

    // Remote mode (issue #46): the household's box through its relay.
    /** The last device list a relay signed, or null when none was pushed yet. */
    routerDevices: (token: string, tenantId: string) =>
      call<HubDevices>('GET', tenantPath(tenantId, 'router/devices'), undefined, token).catch((e: unknown) => {
        if (e instanceof HubError && e.status === 404) return null;
        throw e;
      }),
    relayStatus: (token: string, tenantId: string) =>
      call<HubRelayStatus>('GET', tenantPath(tenantId, 'router/relay'), undefined, token),
    /** Carries a command the owner's core signed; 202 = not answered yet. */
    deviceCommand: (token: string, tenantId: string, mac: string, action: 'block' | 'unblock', signed: { payload: string; sig: string }) =>
      call<HubCommand>('POST', tenantPath(tenantId, `router/devices/${encodeURIComponent(mac)}/${action}`), signed, token),
    /** Carries any request the core signed for the relay (issue #50); 202 = not answered yet. */
    routerCommand: (token: string, tenantId: string, signed: { payload: string; sig: string; body?: string }) =>
      call<HubCommand>('POST', tenantPath(tenantId, 'router/commands'), signed, token),
    command: (token: string, tenantId: string, commandId: string) =>
      call<HubCommand>('GET', tenantPath(tenantId, `router/commands/${encodeURIComponent(commandId)}`), undefined, token),

    // Alerts (issue #49, internal/hub/alerts): WhatsApp opt-in, the hub
    // key behind "1 = bloquer", and this phone's push token.
    whatsapp: (token: string, tenantId: string) => call<WhatsAppStatus>('GET', tenantPath(tenantId, 'whatsapp'), undefined, token),
    whatsappOptIn: (token: string, tenantId: string) =>
      call<WhatsAppInvite>('POST', tenantPath(tenantId, 'whatsapp/optin'), {}, token),
    hubKey: (token: string, tenantId: string) => call<HubKeyAnswer>('GET', tenantPath(tenantId, 'hub-key'), undefined, token),
    putPushToken: (token: string, tenantId: string, push: { token: string; platform: 'android' | 'ios' }) =>
      call<void>('PUT', tenantPath(tenantId, 'push-tokens'), push, token),
    deletePushToken: (token: string, tenantId: string, pushToken: string) =>
      call<void>('DELETE', tenantPath(tenantId, `push-tokens/${encodeURIComponent(pushToken)}`), undefined, token),

    // Approvals (issue #58, internal/hub/inventory): an approved device never
    // alerts; owner or admin only.
    /** The inventory, or null when the relay pushed nothing yet. */
    inventory: (token: string, tenantId: string) =>
      call<HubInventory>('GET', tenantPath(tenantId, 'router/inventory'), undefined, token).catch((e: unknown) => {
        if (e instanceof HubError && e.status === 404) return null;
        throw e;
      }),
    approveDevice: (token: string, tenantId: string, mac: string) =>
      call<void>('PUT', tenantPath(tenantId, `router/approvals/${encodeURIComponent(mac)}`), {}, token),
    withdrawApproval: (token: string, tenantId: string, mac: string) =>
      call<void>('DELETE', tenantPath(tenantId, `router/approvals/${encodeURIComponent(mac)}`), undefined, token),
    /** Several at once: what this phone approved alone, names given before #58. */
    approveDevices: (token: string, tenantId: string, macs: string[]) =>
      call<void>('PUT', tenantPath(tenantId, 'router/approvals'), { macs }, token),

    /** The owner's phone records the MAC it has on the box (#87), under its owner key. */
    declareOwnerDevice: (token: string, tenantId: string, mac: string, ownerKey: string) =>
      call<void>('PUT', tenantPath(tenantId, 'router/owner-devices'), { mac, ownerKey }, token),

    // The household's Kergui backups (#88/#89): any member reads, the owner
    // or an admin writes (a member gets 403).
    backups: async (token: string, tenantId: string) =>
      (await call<{ backups?: HubBackupInfo[] | null }>('GET', tenantPath(tenantId, 'router/backups'), undefined, token))
        ?.backups ?? [],
    /** The box's backup, or null when none was sent yet. */
    backup: (token: string, tenantId: string, boxId: string) =>
      call<HubBackupFull>('GET', tenantPath(tenantId, `router/backups/${encodeURIComponent(boxId)}`), undefined, token).catch(
        (e: unknown) => {
          if (e instanceof HubError && e.status === 404) return null;
          throw e;
        },
      ),
    putBackup: (token: string, tenantId: string, boxId: string, archive: string) =>
      call<HubBackupSaved>('PUT', tenantPath(tenantId, `router/backups/${encodeURIComponent(boxId)}`), { archive }, token),

    // Complaints (issue #82/#84), with the licensing session.
    createComplaint: (req: ComplaintRequest) => call<ComplaintReceipt>('POST', 'complaints', req),
    complaints: async () => (await call<{ complaints?: Complaint[] }>('GET', 'complaints'))?.complaints ?? [],
    /** The customer's message on the thread of complaint n° `number`. */
    replyComplaint: (number: number, message: string) =>
      call<Complaint>('POST', `complaints/${encodeURIComponent(String(number))}/messages`, { message, source: 'app' }),

    // Self-update of the website APK (#74): public, no session; the
    // installation id is random, tied to no account.
    checkRelease: (req: ReleaseCheckRequest) => call<ReleaseCheck>('POST', '/releases/v1/check', req, false),
  };
}
