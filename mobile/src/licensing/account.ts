import Constants from 'expo-constants';
import * as SecureStore from 'expo-secure-store';
import { Platform } from 'react-native';
import { create } from 'zustand';

import { HubError, type OtpRequested } from './api';
import { hub, hubTokens, setHubSession } from './hub';
import type { Household } from './hubTokens';

// The Kergui account: a phone number verified by a WhatsApp code
// (RFC-006, Accounts). It is also the hub identity (issue #41): the same
// sign-in gives the kgs_ licensing session and the household's kernel token
// pair (hubTokens.ts). Both secrets stay in SecureStore. Several phones of a
// household can sign in to the same account.

export type Account = { accountId: string; phone: string; sessionToken: string; expiresAt: string };

const ACCOUNT_KEY = 'kergui.hub.account';

type State = {
  ready: boolean;
  account: Account | null;
  load: () => Promise<void>;
  requestCode: (phone: string) => Promise<OtpRequested>;
  verify: (phone: string, code: string) => Promise<void>;
  signOut: () => Promise<void>;
  /** The hub refused the session (expired, revoked): forget it. */
  invalidate: () => Promise<void>;
  /** The household this number owns on the hub, once signed in. */
  household: () => Household | null;
  /**
   * A kernel access token for /api/v1/tenants/{tenantId}/..., refreshed when
   * needed. Both sessions refused: the account is signed out.
   */
  hubAccessToken: () => Promise<string>;
  /**
   * Signs the household in again from the licensing session: after being
   * removed from a household, or invited into one (issue #42).
   */
  rejoinHousehold: () => Promise<Household | null>;
};

function deviceName(): string {
  return (Constants.deviceName || `${Platform.OS} ${Platform.Version}`).slice(0, 64);
}

async function forget() {
  try {
    await SecureStore.deleteItemAsync(ACCOUNT_KEY);
  } catch {
    // Nothing stored, or a keystore that fails: the account is gone either way.
  }
}

export const useAccount = create<State>((set, get) => ({
  ready: false,
  account: null,

  load: async () => {
    let account: Account | null = null;
    try {
      const raw = await SecureStore.getItemAsync(ACCOUNT_KEY);
      const parsed = raw ? (JSON.parse(raw) as Account) : null;
      if (parsed?.sessionToken && parsed.accountId) account = parsed;
    } catch {
      account = null;
    }
    setHubSession(account?.sessionToken ?? null);
    if (account) await hubTokens.load();
    else await hubTokens.clear();
    set({ ready: true, account });
  },

  requestCode: (phone) => hub.requestOtp(phone.trim()),

  verify: async (phone, code) => {
    const out = await hub.verifyOtp(phone.trim(), code.trim(), deviceName());
    const account: Account = { accountId: out.accountId, phone: out.phone, sessionToken: out.sessionToken, expiresAt: out.expiresAt };
    await SecureStore.setItemAsync(ACCOUNT_KEY, JSON.stringify(account), {
      keychainAccessible: SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY,
    });
    setHubSession(account.sessionToken);
    // A hub without the household bridge sends no pair; the first call that
    // needs one asks the licensing session for it.
    if (out.hub) await hubTokens.save(out.hub);
    else await hubTokens.clear();
    set({ account });
  },

  signOut: async () => {
    if (get().account) {
      // Best effort: the session ends on the hub too when it can be reached.
      await hub.signOut().catch(() => undefined);
    }
    await hubTokens.clear({ revoke: true });
    setHubSession(null);
    await forget();
    set({ account: null });
  },

  invalidate: async () => {
    setHubSession(null);
    await hubTokens.clear();
    await forget();
    set({ account: null });
  },

  household: () => hubTokens.household(),

  rejoinHousehold: async () => {
    try {
      return await hubTokens.rejoin();
    } catch (e) {
      if (e instanceof HubError && e.sessionInvalid) await get().invalidate();
      throw e;
    }
  },

  hubAccessToken: async () => {
    try {
      return await hubTokens.accessToken();
    } catch (e) {
      if (e instanceof HubError && e.sessionInvalid) await get().invalidate();
      throw e;
    }
  },
}));
