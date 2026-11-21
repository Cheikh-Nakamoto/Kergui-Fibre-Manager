import Constants from 'expo-constants';
import * as SecureStore from 'expo-secure-store';
import { Platform } from 'react-native';
import { create } from 'zustand';

import type { OtpRequested } from './api';
import { hub, setHubSession } from './hub';

// The Kergui account: a phone number verified by a WhatsApp code
// (RFC-006, Accounts). The kgs_ session token is the only secret, and it
// stays in SecureStore. Several phones of a household can sign in to the
// same account.

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
    set({ account });
  },

  signOut: async () => {
    if (get().account) {
      // Best effort: the session ends on the hub too when it can be reached.
      await hub.signOut().catch(() => undefined);
    }
    setHubSession(null);
    await forget();
    set({ account: null });
  },

  invalidate: async () => {
    setHubSession(null);
    await forget();
    set({ account: null });
  },
}));
