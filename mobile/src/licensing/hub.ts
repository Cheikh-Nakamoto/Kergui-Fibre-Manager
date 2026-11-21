import { createHubClient } from './api';
import { config } from './config';

// The app's one hub client. The account store tells it the session token
// whenever it changes (sign-in, load, sign-out).
let session: string | null = null;

export function setHubSession(token: string | null) {
  session = token;
}

export const hub = createHubClient({ baseUrl: config.hubUrl, session: () => session });
