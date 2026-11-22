import type { HubClient } from './api';
import { HubError } from './api';
import { decodeBase64Url, utf8 } from './base64';

// The hub session (issue #41). The number verified over WhatsApp is the hub
// account: the same sign-in gives the kgs_ licensing session (account.ts)
// and a kernel token pair for the household's routes,
// /api/v1/tenants/{tenantId}/... The access token lives 15 minutes; the
// refresh token rotates at every use (POST /api/v1/auth/refresh), and
// presenting a spent one makes the kernel revoke them all. So: one refresh
// at a time, the rotated token saved before it is used, and when the kernel
// refuses, the licensing session asks for a new pair.

export type HubTokens = { userId: string; tenantId: string; accessToken: string; refreshToken: string };
export type Household = { tenantId: string; userId: string };

export type HubTokensClient = Pick<HubClient, 'refreshHubTokens' | 'hubSession' | 'signOutHub'>;

export type TokenStore = {
  get: (key: string) => Promise<string | null>;
  set: (key: string, value: string) => Promise<void>;
  del: (key: string) => Promise<void>;
};

export const HUB_TOKENS_KEY = 'kergui.hub.kernel';
/** Refresh this long before the access token's exp, for clock skew and latency. */
const EARLY_S = 60;

/** The `exp` claim (seconds) of a JWT, or null when it cannot be read. */
export function accessTokenExpiry(jwt: string): number | null {
  const parts = jwt.split('.');
  if (parts.length !== 3) return null;
  const bytes = decodeBase64Url(parts[1]);
  const text = bytes && utf8(bytes);
  if (!text) return null;
  try {
    const exp = (JSON.parse(text) as { exp?: unknown }).exp;
    return typeof exp === 'number' && Number.isFinite(exp) ? exp : null;
  } catch {
    return null;
  }
}

function valid(t: Partial<HubTokens> | null | undefined): t is HubTokens {
  return !!t && !!t.accessToken && !!t.refreshToken && !!t.tenantId && !!t.userId;
}

export function createHubTokens(opts: { client: HubTokensClient; store: TokenStore; now?: () => number }) {
  const now = opts.now ?? (() => Date.now() / 1000);
  let current: HubTokens | null = null;
  let inflight: Promise<string> | null = null;
  // Bumped by clear: a refresh that was in flight at sign-out must not
  // write its pair back.
  let generation = 0;

  async function save(t: HubTokens | null) {
    current = t;
    try {
      if (t) await opts.store.set(HUB_TOKENS_KEY, JSON.stringify(t));
      else await opts.store.del(HUB_TOKENS_KEY);
    } catch {
      // A keystore that fails keeps the pair in memory for this run.
    }
  }

  async function renew(): Promise<string> {
    const gen = generation;
    const keep = async (t: HubTokens) => {
      if (gen === generation) await save(t);
      else throw new HubError(401, 'SESSION_INVALID', 'signed out');
    };
    const held = current;
    if (held) {
      try {
        const r = await opts.client.refreshHubTokens(held.refreshToken);
        await keep({ ...held, accessToken: r.accessToken, refreshToken: r.refreshToken });
        return r.accessToken;
      } catch (e) {
        // Offline or a hub hiccup: keep the pair, the next call retries.
        if (!(e instanceof HubError) || e.status !== 401) throw e;
      }
    }
    // No pair, or the kernel refused it (expired, revoked after a reuse):
    // the licensing session is the same sign-in and asks for a new one.
    try {
      const s = await opts.client.hubSession();
      await keep({ userId: s.userId, tenantId: s.tenantId, accessToken: s.accessToken, refreshToken: s.refreshToken });
      return s.accessToken;
    } catch (e) {
      if (e instanceof HubError && e.sessionInvalid && gen === generation) await save(null);
      throw e;
    }
  }

  return {
    save,

    load: async (): Promise<HubTokens | null> => {
      try {
        const raw = await opts.store.get(HUB_TOKENS_KEY);
        const parsed = raw ? (JSON.parse(raw) as Partial<HubTokens>) : null;
        current = valid(parsed) ? parsed : null;
      } catch {
        current = null;
      }
      return current;
    },

    household: (): Household | null => (current ? { tenantId: current.tenantId, userId: current.userId } : null),

    /** A kernel access token good for at least a minute, refreshed if needed. */
    accessToken: async (): Promise<string> => {
      if (current) {
        const exp = accessTokenExpiry(current.accessToken);
        if (exp !== null && exp - EARLY_S > now()) return current.accessToken;
      }
      if (!inflight) inflight = renew().finally(() => (inflight = null));
      return inflight;
    },

    /**
     * A new pair from the licensing session, skipping the refresh token: a
     * refresh keeps the household it was issued for, and the member may
     * have left it, or joined another (issue #42).
     */
    rejoin: async (): Promise<Household | null> => {
      const gen = generation;
      const s = await opts.client.hubSession();
      if (gen !== generation) throw new HubError(401, 'SESSION_INVALID', 'signed out');
      await save({ userId: s.userId, tenantId: s.tenantId, accessToken: s.accessToken, refreshToken: s.refreshToken });
      return { tenantId: s.tenantId, userId: s.userId };
    },

    /** Forget the pair; with revoke, end the kernel session on the hub too (best effort). */
    clear: async ({ revoke = false }: { revoke?: boolean } = {}) => {
      const held = current;
      generation++;
      await save(null);
      if (revoke && held) await opts.client.signOutHub(held.refreshToken).catch(() => undefined);
    },
  };
}

export type HubTokensStore = ReturnType<typeof createHubTokens>;
