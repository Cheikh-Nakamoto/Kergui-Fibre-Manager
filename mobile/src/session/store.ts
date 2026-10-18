import * as Crypto from 'expo-crypto';
import * as SecureStore from 'expo-secure-store';
import { create } from 'zustand';

import { api, core } from '../core/client';

// What the app remembers between launches. The router password is not here:
// the core keeps it encrypted in its own database (vault key below).
export type Session =
  | { mode: 'demo' }
  | { mode: 'router'; routerUrl: string; pin: string };

const SESSION_KEY = 'kergui.session';
const VAULT_KEY = 'kergui.vaultKey';

export const DEMO_CREDENTIALS = { username: 'admin', password: 'admin' };

type State = {
  ready: boolean;
  session: Session | null;
  bootError: string | null;
  boot: () => Promise<void>;
  enterDemo: () => Promise<void>;
  connected: (s: Session) => Promise<void>;
  signOut: () => Promise<void>;
};

async function vaultKey(): Promise<string> {
  const existing = await SecureStore.getItemAsync(VAULT_KEY);
  if (existing) return existing;
  const bytes = Crypto.getRandomBytes(32);
  const key = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
  await SecureStore.setItemAsync(VAULT_KEY, key, { keychainAccessible: SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY });
  return key;
}

async function startDemo() {
  await core.startDemo();
  await api.login(DEMO_CREDENTIALS.username, DEMO_CREDENTIALS.password);
}

export const useSession = create<State>((set) => ({
  ready: false,
  session: null,
  bootError: null,

  boot: async () => {
    try {
      await core.open(await vaultKey());
      const raw = await SecureStore.getItemAsync(SESSION_KEY);
      const session = raw ? (JSON.parse(raw) as Session) : null;
      if (session?.mode === 'demo') await startDemo();
      if (session?.mode === 'router') await core.configure(session.routerUrl, session.pin);
      set({ ready: true, session, bootError: null });
    } catch (e) {
      set({ ready: true, session: null, bootError: e instanceof Error ? e.message : String(e) });
    }
  },

  enterDemo: async () => {
    await startDemo();
    const session: Session = { mode: 'demo' };
    await SecureStore.setItemAsync(SESSION_KEY, JSON.stringify(session));
    set({ session });
  },

  connected: async (session) => {
    await SecureStore.setItemAsync(SESSION_KEY, JSON.stringify(session));
    set({ session });
  },

  signOut: async () => {
    await core.stopDemo();
    await SecureStore.deleteItemAsync(SESSION_KEY);
    set({ session: null });
  },
}));
