import * as SecureStore from 'expo-secure-store';

import { createHubClient } from './api';
import { config } from './config';
import { createHubTokens } from './hubTokens';

// The app's one hub client. The account store tells it the session token
// whenever it changes (sign-in, load, sign-out).
let session: string | null = null;

export function setHubSession(token: string | null) {
  session = token;
}

export const hub = createHubClient({ baseUrl: config.hubUrl, session: () => session });

// The household's kernel pair (issue #41), in SecureStore beside the
// licensing session, readable only on this device while it is unlocked.
export const hubTokens = createHubTokens({
  client: hub,
  store: {
    get: (k) => SecureStore.getItemAsync(k),
    set: (k, v) => SecureStore.setItemAsync(k, v, { keychainAccessible: SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY }),
    del: (k) => SecureStore.deleteItemAsync(k),
  },
});
