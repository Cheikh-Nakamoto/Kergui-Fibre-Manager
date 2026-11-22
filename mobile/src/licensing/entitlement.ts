import * as SecureStore from 'expo-secure-store';
import { create } from 'zustand';

import { api, core } from '../core/client';
import { useAccount } from './account';
import { HubError } from './api';
import { config } from './config';
import type { Tier } from './gate';
import { hub } from './hub';
import { coreKeyList, learn, makeRing, verifyKeyList, type KeyRing, type TrustedKey } from './keyring';
import { acceptAnswer, tierAt, verifyToken, type Claims } from './token';

// What this phone may do on the current box: the hub's signed entitlement
// token for the box, verified offline with the hub keys the app trusts
// (RFC-006, Entitlement token). It is cached in SecureStore so Pro keeps
// working on a home Wi-Fi whose Internet is down, until the pass expiry plus
// a 3-day grace, and refreshed whenever the hub can be reached.
//
// The trusted keys are those shipped in the app plus those learned from the
// hub's signed key list at every refresh, minus the revoked ones
// (docs/licensing-key-rotation.md). A token of a revoked key is dropped, and
// the same refresh fetches a fresh one, so a rotation signs nobody out.

const STORE_KEY = 'kergui.hub.entitlement';
const KEYS_STORE_KEY = 'kergui.hub.keys';
/** A refresh closer than this to the last one is skipped, unless forced. */
const MIN_REFRESH_MS = 60_000;

type Stored = { boxId: string | null; token: string | null };
/** What was learned from the hub: keys beyond the embedded ones, and revocations. */
type StoredKeys = { keys: TrustedKey[]; revoked: string[] };

const embeddedRing = (): KeyRing => makeRing(config.publicKeys);

type State = {
  /**
   * The box serial number: null while unknown (not read yet, box
   * unreachable), '' when the model does not expose one (Pro unsupported).
   */
  boxId: string | null;
  token: string | null;
  claims: Claims | null;
  /** The hub keys trusted now: embedded plus learned, minus revoked. */
  keys: KeyRing;
  refreshing: boolean;
  /** The last refresh failure, for the Account screen. */
  error: unknown;
  lastRefresh: number;
  load: () => Promise<void>;
  /** Reads the box serial number through the core (GET /api/inspect). */
  identify: () => Promise<void>;
  setBox: (serial: string | null) => Promise<void>;
  /** Asks the hub for a fresh token; keeps the cached one when offline. */
  refresh: (opts?: { force?: boolean }) => Promise<void>;
  /** Drops the box and its token (the phone is moving to another box). */
  forgetBox: () => Promise<void>;
};

async function persistKeys(ring: KeyRing) {
  const embedded = new Set(config.publicKeys.map((k) => k.keyId));
  const s: StoredKeys = { keys: ring.keys.filter((k) => !embedded.has(k.keyId)), revoked: ring.revoked };
  try {
    await SecureStore.setItemAsync(KEYS_STORE_KEY, JSON.stringify(s));
  } catch {
    // Not kept: the next refresh learns the list again.
  }
}

async function loadKeys(): Promise<KeyRing> {
  try {
    const raw = await SecureStore.getItemAsync(KEYS_STORE_KEY);
    if (raw) {
      const s = JSON.parse(raw) as Partial<StoredKeys>;
      const keys = Array.isArray(s.keys) ? s.keys : [];
      const revoked = Array.isArray(s.revoked) ? s.revoked : [];
      return makeRing([...config.publicKeys, ...keys], revoked);
    }
  } catch {
    // Unreadable: start again from the embedded keys.
  }
  return embeddedRing();
}

async function persist(s: Stored) {
  try {
    await SecureStore.setItemAsync(STORE_KEY, JSON.stringify(s));
  } catch {
    // A cache that cannot be written only costs a refresh on next launch.
  }
}

function normalise(serial: string | null): string | null {
  return serial === null ? null : serial.trim().toUpperCase();
}

export const useEntitlement = create<State>((set, get) => ({
  boxId: null,
  token: null,
  claims: null,
  keys: embeddedRing(),
  refreshing: false,
  error: null,
  lastRefresh: 0,

  load: async () => {
    let stored: Stored = { boxId: null, token: null };
    try {
      const raw = await SecureStore.getItemAsync(STORE_KEY);
      if (raw) stored = JSON.parse(raw) as Stored;
    } catch {
      stored = { boxId: null, token: null };
    }
    const keys = await loadKeys();
    let claims: Claims | null = null;
    if (stored.token) {
      const r = verifyToken(keys, stored.token, Date.now());
      if (r.ok && r.claims.boxId === stored.boxId) claims = r.claims;
    }
    set({ boxId: stored.boxId ?? null, token: claims ? stored.token : null, claims, keys });
  },

  identify: async () => {
    try {
      const rep = await api.inspect();
      await get().setBox(rep.Info?.SerialNumber ?? '');
    } catch {
      // Box unreachable: keep the serial read last time.
    }
  },

  setBox: async (serial) => {
    const boxId = normalise(serial);
    if (boxId === get().boxId) return;
    // Another box: the cached token is for the old one.
    set({ boxId, token: null, claims: null, error: null, lastRefresh: 0 });
    await persist({ boxId, token: null });
  },

  refresh: async ({ force = false } = {}) => {
    const { boxId, refreshing, lastRefresh } = get();
    const account = useAccount.getState().account;
    if (!boxId || !account || !config.hubUrl || refreshing) return;
    if (!force && Date.now() - lastRefresh < MIN_REFRESH_MS) return;
    set({ refreshing: true });
    try {
      // Bind the box first: the hub ties to it what waits on the account
      // (RFC-006), so the token that follows covers it. Binding again is
      // harmless, so every refresh does it, the first sign-in on a box
      // included.
      await hub.bindBox(boxId);
      await learnKeys(get, set);
      const answer = await hub.token(boxId);
      const r = acceptAnswer(answer, { keys: get().keys, boxId, nowMs: Date.now() });
      if (get().boxId !== boxId) return; // the box changed meanwhile
      if (r.kind === 'paid') {
        set({ token: r.token, claims: r.claims, error: null });
        await persist({ boxId, token: r.token });
      } else if (r.kind === 'free') {
        set({ token: null, claims: null, error: null });
        await persist({ boxId, token: null });
      } else {
        // Not signed by a key this app trusts, or for another box: keep
        // what verified before, and say so.
        set({ error: new HubError(200, 'BAD_RESPONSE', `entitlement token refused: ${r.reason}`) });
      }
    } catch (e) {
      if (e instanceof HubError && e.sessionInvalid) await useAccount.getState().invalidate();
      set({ error: e });
    } finally {
      set({ refreshing: false, lastRefresh: Date.now() });
    }
  },

  forgetBox: async () => {
    set({ boxId: null, token: null, claims: null, error: null, lastRefresh: 0 });
    await persist({ boxId: null, token: null });
  },
}));

/**
 * Reads the hub's signed key list and learns it when a trusted key signed
 * it. A token the new ring no longer honours (its key revoked) is dropped
 * at once: the token call that follows replaces it. A hub without the list
 * (older), or one that cannot be reached, changes nothing.
 */
async function learnKeys(get: () => State, set: (p: Partial<State>) => void): Promise<void> {
  let signed: unknown;
  try {
    signed = (await hub.publicKeys())?.signed;
  } catch {
    return;
  }
  if (typeof signed !== 'string') return;
  const v = verifyKeyList(get().keys, signed);
  if (!v.ok) return;
  const keys = learn(config.publicKeys, get().keys, v.list);
  if (coreKeyList(keys) === coreKeyList(get().keys)) return;
  set({ keys });
  await persistKeys(keys);
  const { token, boxId } = get();
  if (token && !verifyToken(keys, token, Date.now()).ok) {
    set({ token: null, claims: null });
    await persist({ boxId, token: null });
  }
}

/**
 * Hands the Go core what its own gate needs: the trusted keys (and the
 * revoked key ids) and the current token. The core refuses a paid write by itself, so the lock does
 * not rest on the screens alone. Called once the core is open; afterwards
 * every token change is pushed by the subscription below. It never throws:
 * a core that is not open yet gets both at its next open.
 */
export async function syncCore(): Promise<void> {
  try {
    await core.setPublicKeys(coreKeyList(useEntitlement.getState().keys));
    await core.setEntitlement(useEntitlement.getState().token ?? '');
  } catch {
    // Not open yet, or no native core (tests): boot calls this again.
  }
}

useEntitlement.subscribe((s, prev) => {
  if (s.keys !== prev.keys && coreKeyList(s.keys) !== coreKeyList(prev.keys)) {
    void Promise.resolve()
      .then(() => core.setPublicKeys(coreKeyList(s.keys)))
      .catch(() => undefined);
  }
  if (s.token === prev.token) return;
  void Promise.resolve()
    .then(() => core.setEntitlement(s.token ?? ''))
    .catch(() => undefined);
});

/** The tier the cached token gives on the current box now. */
export function currentTier(s: Pick<State, 'claims' | 'boxId'>, nowMs = Date.now()): Tier {
  return tierAt(s.claims, s.boxId, nowMs);
}
