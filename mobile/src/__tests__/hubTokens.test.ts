// The hub session (issue #41): the WhatsApp sign-in also hands the app a
// kernel access token and a rotating refresh token for its household. Both
// stay in secure storage; the access token is refreshed before it runs out,
// the rotated refresh token replaces the old one, and when the kernel
// refuses the refresh the licensing session gets a new pair.
import { HubError } from '../licensing/api';
import { accessTokenExpiry, createHubTokens, type HubTokens, type HubTokensClient } from '../licensing/hubTokens';

const NOW = Date.UTC(2026, 10, 22, 12) / 1000;

function b64url(s: string): string {
  const STD = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';
  const bytes = Array.from(new TextEncoder().encode(s));
  let out = '';
  for (let i = 0; i < bytes.length; i += 3) {
    const n = (bytes[i] << 16) | ((bytes[i + 1] ?? 0) << 8) | (bytes[i + 2] ?? 0);
    const chars = [n >> 18, (n >> 12) & 63, (n >> 6) & 63, n & 63].map((v) => STD[v]);
    out += chars.slice(0, bytes.length - i >= 3 ? 4 : bytes.length - i + 1).join('');
  }
  return out;
}

/** A kernel JWT shape: only `exp` is read, the signature is the hub's business. */
function jwt(exp: number, sub = 'user-1'): string {
  return `${b64url('{"alg":"HS256","typ":"JWT"}')}.${b64url(JSON.stringify({ sub, role: 'ROLE_VIEWER', exp, iat: exp - 900 }))}.sig`;
}

function memoryStore() {
  const data = new Map<string, string>();
  return {
    data,
    get: async (k: string) => data.get(k) ?? null,
    set: async (k: string, v: string) => void data.set(k, v),
    del: async (k: string) => void data.delete(k),
  };
}

function fakeClient(over: Partial<HubTokensClient> = {}) {
  let n = 1;
  return {
    refreshHubTokens: jest.fn(async (_rt: string) => {
      n++;
      return { accessToken: jwt(NOW + 900, `rot-${n}`), refreshToken: `rt-${n}` };
    }),
    hubSession: jest.fn(async () => ({ userId: 'user-1', tenantId: 'tenant-1', accessToken: jwt(NOW + 900), refreshToken: 'rt-reissued' })),
    signOutHub: jest.fn(async (_rt: string) => undefined),
    ...over,
  };
}

const first = (exp = NOW + 900): HubTokens => ({ userId: 'user-1', tenantId: 'tenant-1', accessToken: jwt(exp), refreshToken: 'rt-1' });

function setup(over: Partial<HubTokensClient> = {}) {
  const store = memoryStore();
  const client = fakeClient(over);
  let now = NOW;
  const tokens = createHubTokens({ client, store, now: () => now });
  return { store, client, tokens, advance: (s: number) => (now += s) };
}

test('reads exp from the kernel access token', () => {
  expect(accessTokenExpiry(jwt(NOW + 900))).toBe(NOW + 900);
  expect(accessTokenExpiry('garbage')).toBeNull();
  expect(accessTokenExpiry('a.b.c')).toBeNull();
});

test('the pair from the sign-in is kept in secure storage and survives a restart', async () => {
  const { store, client, tokens } = setup();
  await tokens.save(first());
  expect(JSON.parse(store.data.get('kergui.hub.kernel')!)).toMatchObject({ tenantId: 'tenant-1', refreshToken: 'rt-1' });

  const again = createHubTokens({ client, store, now: () => NOW });
  expect(await again.load()).toMatchObject({ tenantId: 'tenant-1', userId: 'user-1' });
  expect(again.household()).toEqual({ tenantId: 'tenant-1', userId: 'user-1' });
  expect(await again.accessToken()).toBe(first().accessToken);
  expect(client.refreshHubTokens).not.toHaveBeenCalled();
});

test('a token close to its end is refreshed, and the rotated refresh token replaces the old one', async () => {
  const { store, client, tokens, advance } = setup();
  await tokens.save(first());
  advance(900 - 30); // within the minute before exp
  const access = await tokens.accessToken();
  expect(client.refreshHubTokens).toHaveBeenCalledWith('rt-1');
  expect(accessTokenExpiry(access!)).toBe(NOW + 900);
  expect(JSON.parse(store.data.get('kergui.hub.kernel')!)).toMatchObject({ refreshToken: 'rt-2', tenantId: 'tenant-1' });

  // The next refresh presents the rotated token, never the spent one.
  advance(900);
  await tokens.accessToken();
  expect(client.refreshHubTokens).toHaveBeenLastCalledWith('rt-2');
});

test('callers at once share one refresh: a refresh token is presented only once', async () => {
  const { client, tokens, advance } = setup();
  await tokens.save(first());
  advance(1000);
  const [a, b, c] = await Promise.all([tokens.accessToken(), tokens.accessToken(), tokens.accessToken()]);
  expect(client.refreshHubTokens).toHaveBeenCalledTimes(1);
  expect(new Set([a, b, c]).size).toBe(1);
});

test('the kernel refuses the refresh: the licensing session gets a new pair', async () => {
  const { store, client, tokens, advance } = setup({
    refreshHubTokens: jest.fn(async () => {
      throw new HubError(401, 'UNAUTHORIZED', 'refresh token revoked');
    }),
  });
  await tokens.save(first());
  advance(1000);
  expect(await tokens.accessToken()).toBeTruthy();
  expect(client.hubSession).toHaveBeenCalledTimes(1);
  expect(JSON.parse(store.data.get('kergui.hub.kernel')!)).toMatchObject({ refreshToken: 'rt-reissued' });
});

test('the licensing session is gone too: the pair is forgotten and the error says sign in again', async () => {
  const { store, tokens, advance } = setup({
    refreshHubTokens: jest.fn(async () => {
      throw new HubError(401, 'UNAUTHORIZED', 'expired');
    }),
    hubSession: jest.fn(async () => {
      throw new HubError(401, 'SESSION_INVALID', 'sign in');
    }),
  });
  await tokens.save(first());
  advance(1000);
  const e: HubError = await tokens.accessToken().catch((x) => x);
  expect(e.sessionInvalid).toBe(true);
  expect(store.data.has('kergui.hub.kernel')).toBe(false);
  expect(tokens.household()).toBeNull();
});

test('offline: the pair is kept for the next try', async () => {
  const { store, tokens, advance } = setup({
    refreshHubTokens: jest.fn(async () => {
      throw new HubError(0, 'NETWORK', 'down');
    }),
  });
  await tokens.save(first());
  advance(1000);
  const e: HubError = await tokens.accessToken().catch((x) => x);
  expect(e.offline).toBe(true);
  expect(JSON.parse(store.data.get('kergui.hub.kernel')!)).toMatchObject({ refreshToken: 'rt-1' });
});

test('signed in on a hub without the household bridge: no pair, the licensing session asks for one', async () => {
  const { client, tokens } = setup();
  expect(await tokens.accessToken()).toBeTruthy();
  expect(client.hubSession).toHaveBeenCalledTimes(1);
  expect(tokens.household()).toEqual({ tenantId: 'tenant-1', userId: 'user-1' });
});

test('sign out ends the kernel session and forgets the pair', async () => {
  const { store, client, tokens } = setup();
  await tokens.save(first());
  await tokens.clear({ revoke: true });
  expect(client.signOutHub).toHaveBeenCalledWith('rt-1');
  expect(store.data.has('kergui.hub.kernel')).toBe(false);
  expect(tokens.household()).toBeNull();
});

test('rejoin asks the licensing session for a new pair: a removed member lands in their own household (#42)', async () => {
  const store = memoryStore();
  const client = fakeClient({
    hubSession: jest.fn(async () => ({ userId: 'user-1', tenantId: 'own-household', accessToken: jwt(NOW + 900), refreshToken: 'rt-own' })),
  });
  const tokens = createHubTokens({ client, store, now: () => NOW });
  await tokens.save({ userId: 'user-1', tenantId: 'their-household', accessToken: jwt(NOW + 900), refreshToken: 'rt-1' });
  const h = await tokens.rejoin();
  expect(h).toEqual({ userId: 'user-1', tenantId: 'own-household' });
  expect(tokens.household()?.tenantId).toBe('own-household');
  expect(client.refreshHubTokens).not.toHaveBeenCalled(); // a refresh keeps the old household
  expect(JSON.parse(store.data.get('kergui.hub.kernel')!).refreshToken).toBe('rt-own');
});
