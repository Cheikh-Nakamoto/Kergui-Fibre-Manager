import type { Tier } from './gate';

// Typed client of the hub's licensing API (internal/hub/licensing/handler).
// Every route lives under /licensing/v1; the authenticated ones take the
// kgs_ session token as a bearer. Errors come back as
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
export type SignedIn = { sessionToken: string; accountId: string; phone: string; expiresAt: string };
export type Subscription = {
  boxId: string;
  tier: Tier;
  effectiveTier: Tier;
  active: boolean;
  expiresAt: string;
  /** How the hub credited it; the app does not act on it. */
  channel: string;
};
export type TokenAnswer = {
  boxId: string;
  tier: Tier;
  /** Absent on Free: there is nothing to unlock. */
  token?: string;
  expiresAt?: string;
  issuedAt?: string;
  keyId?: string;
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

export type HubClientOptions = {
  /** e.g. https://hub.kergui.example; empty = no hub in this build. */
  baseUrl: string;
  /** The current session token, read on every call. */
  session: () => string | null;
  fetch?: typeof fetch;
  timeoutMs?: number;
};

export type HubClient = ReturnType<typeof createHubClient>;

export function createHubClient(opts: HubClientOptions) {
  const base = opts.baseUrl.trim().replace(/\/+$/, '');
  const timeoutMs = opts.timeoutMs ?? 15_000;

  async function call<T>(method: string, path: string, body?: unknown, auth = true): Promise<T> {
    if (!base) throw new HubError(0, 'NOT_CONFIGURED', 'no hub configured in this build');
    const headers: Record<string, string> = { Accept: 'application/json' };
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (auth) {
      const s = opts.session();
      if (!s) throw new HubError(401, 'SESSION_INVALID', 'not signed in');
      headers.Authorization = `Bearer ${s}`;
    }
    const doFetch = opts.fetch ?? fetch;
    const ctrl = typeof AbortController !== 'undefined' ? new AbortController() : null;
    const timer = ctrl ? setTimeout(() => ctrl.abort(), timeoutMs) : null;
    let res: Response;
    try {
      res = await doFetch(`${base}/licensing/v1/${path}`, {
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
    publicKey: () => call<PublicKeyAnswer>('GET', 'public-key', undefined, false),
    publicKeys: () => call<PublicKeysAnswer>('GET', 'public-keys', undefined, false),
    subscriptions: async () => (await call<{ subscriptions?: Subscription[] }>('GET', 'subscriptions'))?.subscriptions ?? [],
    bindBox: (boxId: string) => call<BindAnswer>('POST', 'bind', { boxId }),
    token: (boxId: string) => call<TokenAnswer>('POST', 'token', { boxId }),
    transfer: (fromBoxId: string, toBoxId: string) => call<Subscription>('POST', 'transfer', { fromBoxId, toBoxId }),
  };
}
