import { createHubClient, HUB_ERROR_CODES, HubError } from '../licensing/api';
import { hubErrorMessage } from '../i18n';

type Call = { url: string; init: RequestInit };

function fakeFetch(status: number, body: unknown, headers: Record<string, string> = {}) {
  const calls: Call[] = [];
  const fn = jest.fn(async (url: string, init: RequestInit) => {
    calls.push({ url, init });
    return new Response(body === undefined ? null : typeof body === 'string' ? body : JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json', ...headers },
    });
  });
  return { fn: fn as unknown as typeof fetch, calls };
}

const client = (f: typeof fetch, session: string | null = 'kgs_abc') =>
  createHubClient({ baseUrl: 'https://hub.test/', fetch: f, session: () => session });

test('sends JSON to /licensing/v1 with the session as a bearer', async () => {
  const { fn, calls } = fakeFetch(200, { boxId: 'ZTEG00000001', tier: 'max', token: 'a.b' });
  const out = await client(fn).token('ZTEG00000001');
  expect(out.tier).toBe('max');
  expect(calls[0].url).toBe('https://hub.test/licensing/v1/token');
  expect(calls[0].init.method).toBe('POST');
  expect(JSON.parse(String(calls[0].init.body))).toEqual({ boxId: 'ZTEG00000001' });
  const h = calls[0].init.headers as Record<string, string>;
  expect(h.Authorization).toBe('Bearer kgs_abc');
  expect(h['Content-Type']).toBe('application/json');
});

test('public routes carry no bearer', async () => {
  const { fn, calls } = fakeFetch(202, { phone: '+221771234567', expiresAt: 'x', resendAt: 'y' });
  await client(fn).requestOtp('77 123 45 67');
  expect((calls[0].init.headers as Record<string, string>).Authorization).toBeUndefined();
  expect(JSON.parse(String(calls[0].init.body))).toEqual({ phone: '77 123 45 67' });
});

test('every route has the body the hub decodes (it refuses unknown fields)', async () => {
  const { fn, calls } = fakeFetch(200, {});
  const c = client(fn);
  await c.verifyOtp('+221771234567', '123456', 'Pixel 7');
  await c.bindBox('B1');
  await c.token('B1');
  await c.transfer('B1', 'B2');
  await c.subscriptions();
  await c.signOut();
  expect(calls.map((x) => `${x.init.method} ${x.url.replace('https://hub.test/licensing/v1/', '')}`)).toEqual([
    'POST otp/verify',
    'POST bind',
    'POST token',
    'POST transfer',
    'GET subscriptions',
    'DELETE session',
  ]);
  expect(calls.map((x) => (x.init.body ? JSON.parse(String(x.init.body)) : null))).toEqual([
    { phone: '+221771234567', code: '123456', device: 'Pixel 7' },
    { boxId: 'B1' },
    { boxId: 'B1' },
    { fromBoxId: 'B1', toBoxId: 'B2' },
    null,
    null,
  ]);
});

test('the client has no payment route (RFC-006, decision of 2026-11-21)', () => {
  const c = client(fakeFetch(200, {}).fn) as Record<string, unknown>;
  // The members routes (issue #42) are household management, and the
  // relay routes (issue #43) pairing and trust material, the sealed box
  // password included (issue #44), and the remote device list and block /
  // unblock through the relay (issue #46), and a member phone's key for its
  // delegation (issue #47), and the alerts' WhatsApp opt-in, hub key and
  // push token (issue #49), and the relay's generic command route (#50):
  // not payment.
  expect(Object.keys(c).sort()).toEqual([
    'agents',
    'bindBox',
    'cancelInvite',
    'changeRole',
    'command',
    'deletePushToken',
    'deviceCommand',
    'hubKey',
    'hubSession',
    'inviteMember',
    'issuePairing',
    'members',
    'network',
    'publicKey',
    'publicKeys',
    'putCredentials',
    'putMemberKey',
    'putNetwork',
    'putPushToken',
    'putTrustBundle',
    'refreshHubTokens',
    'relayStatus',
    'removeMember',
    'requestOtp',
    'revokeAgent',
    'routerCommand',
    'routerDevices',
    'signOut',
    'signOutHub',
    'subscriptions',
    'token',
    'transfer',
    'trustBundle',
    'verifyOtp',
    'whatsapp',
    'whatsappOptIn',
  ]);
});

test('the alerts routes of the household (issue #49), with the kernel token', async () => {
  const { fn, calls } = fakeFetch(200, { enabled: true });
  const c = client(fn);
  await c.whatsapp('jwt', 'T1');
  await c.whatsappOptIn('jwt', 'T1');
  expect(await c.hubKey('jwt', 'T1')).toEqual({ enabled: true });
  await c.putPushToken('jwt', 'T1', { token: 'ExponentPushToken[a b]', platform: 'android' });
  await c.deletePushToken('jwt', 'T1', 'ExponentPushToken[a b]');
  expect(calls.map((x) => `${x.init.method} ${x.url}`)).toEqual([
    'GET https://hub.test/api/v1/tenants/T1/whatsapp',
    'POST https://hub.test/api/v1/tenants/T1/whatsapp/optin',
    'GET https://hub.test/api/v1/tenants/T1/hub-key',
    'PUT https://hub.test/api/v1/tenants/T1/push-tokens',
    'DELETE https://hub.test/api/v1/tenants/T1/push-tokens/ExponentPushToken%5Ba%20b%5D',
  ]);
  expect(JSON.parse(String(calls[3].init.body))).toEqual({ token: 'ExponentPushToken[a b]', platform: 'android' });
  expect((calls[0].init.headers as Record<string, string>).Authorization).toBe('Bearer jwt');
});

test('the relay routes of the household (issue #43), with the kernel token', async () => {
  const { fn, calls } = fakeFetch(200, { agents: [{ id: 'A1' }] });
  const c = client(fn);
  await c.issuePairing('jwt', 'T 1', 'pc');
  expect(await c.agents('jwt', 'T 1')).toEqual([{ id: 'A1' }]);
  await c.revokeAgent('jwt', 'T 1', 'A1');
  await c.trustBundle('jwt', 'T 1');
  await c.putTrustBundle('jwt', 'T 1', { payload: 'P', sig: 'S', version: 2 });
  await c.putNetwork('jwt', 'T 1', { ssids: [], bssids: [], routerPin: 'ab' });
  expect(calls.map((x) => `${x.init.method} ${x.url.replace('https://hub.test/api/v1/tenants/T%201/', '')}`)).toEqual([
    'POST agents/pairing',
    'GET agents',
    'DELETE agents/A1',
    'GET trust-bundle',
    'PUT trust-bundle',
    'PUT network',
  ]);
  expect(calls.map((x) => (x.init.body ? JSON.parse(String(x.init.body)) : null))).toEqual([
    { kind: 'pc' },
    null,
    null,
    null,
    { payload: 'P', sig: 'S', version: 2 },
    { ssids: [], bssids: [], routerPin: 'ab' },
  ]);
  for (const x of calls) expect((x.init.headers as Record<string, string>).Authorization).toBe('Bearer jwt');
});

test('a household without a trust bundle reads as none, other refusals still throw', async () => {
  const missing = fakeFetch(404, { error: { code: 'NOT_FOUND', message: 'no bundle' } });
  await expect(client(missing.fn).trustBundle('jwt', 'T1')).resolves.toBeNull();
  const forbidden = fakeFetch(403, { error: { code: 'FORBIDDEN', message: 'no' } });
  await expect(client(forbidden.fn).trustBundle('jwt', 'T1')).rejects.toBeInstanceOf(HubError);
});

test('the kernel routes of the hub session (issue #41)', async () => {
  const { fn, calls } = fakeFetch(200, { accessToken: 'a2', refreshToken: 'r2' });
  const c = client(fn);
  expect(await c.refreshHubTokens('r1')).toEqual({ accessToken: 'a2', refreshToken: 'r2' });
  await c.signOutHub('r2');
  await c.hubSession();
  expect(calls.map((x) => `${x.init.method} ${x.url}`)).toEqual([
    'POST https://hub.test/api/v1/auth/refresh',
    'POST https://hub.test/api/v1/auth/logout',
    'POST https://hub.test/licensing/v1/session/hub',
  ]);
  const auth = calls.map((x) => (x.init.headers as Record<string, string>).Authorization);
  // The kernel's refresh and logout take the refresh token in the body; only
  // the reissue goes with the licensing session.
  expect(auth).toEqual([undefined, undefined, 'Bearer kgs_abc']);
  expect(JSON.parse(String(calls[0].init.body))).toEqual({ refreshToken: 'r1' });
  expect(JSON.parse(String(calls[1].init.body))).toEqual({ refreshToken: 'r2' });
});

test('subscriptions unwraps the list', async () => {
  const sub = { boxId: 'B1', tier: 'max', effectiveTier: 'max', active: true, expiresAt: 'x', channel: 'prepaid' };
  const { fn } = fakeFetch(200, { subscriptions: [sub] });
  expect(await client(fn).subscriptions()).toEqual([sub]);
});

test('an error envelope becomes a HubError with the hub code', async () => {
  const { fn } = fakeFetch(409, { error: { code: 'TRANSFER_LIMIT', message: 'limit' } });
  const e = await client(fn).transfer('B1', 'B2').catch((x) => x);
  expect(e).toBeInstanceOf(HubError);
  expect(e).toMatchObject({ status: 409, code: 'TRANSFER_LIMIT', message: 'limit' });
});

test('a rate-limited code request carries Retry-After', async () => {
  const { fn } = fakeFetch(429, { error: { code: 'OTP_RATE_LIMITED', message: 'wait' } }, { 'Retry-After': '42' });
  const e: HubError = await client(fn).requestOtp('+221771234567').catch((x) => x);
  expect(e.code).toBe('OTP_RATE_LIMITED');
  expect(e.retryAfter).toBe(42);
});

test('a session the hub refuses is flagged', async () => {
  const { fn } = fakeFetch(401, { error: { code: 'SESSION_INVALID', message: 'no' } });
  const e: HubError = await client(fn).subscriptions().catch((x) => x);
  expect(e.sessionInvalid).toBe(true);
});

test('no body, or a body that is not the envelope, still maps by status', async () => {
  const e1: HubError = await client(fakeFetch(502, '<html>bad gateway</html>').fn).publicKey().catch((x) => x);
  expect(e1).toMatchObject({ status: 502, code: 'HTTP_502' });
  const e2: HubError = await client(fakeFetch(200, 'not json').fn).publicKey().catch((x) => x);
  expect(e2).toMatchObject({ code: 'BAD_RESPONSE' });
});

test('a network failure is NETWORK with status 0', async () => {
  const f = jest.fn(async () => {
    throw new TypeError('Network request failed');
  }) as unknown as typeof fetch;
  const e: HubError = await client(f).publicKey().catch((x) => x);
  expect(e).toMatchObject({ status: 0, code: 'NETWORK' });
  expect(e.offline).toBe(true);
});

test('a client with no hub configured refuses without calling out', async () => {
  const { fn } = fakeFetch(200, {});
  const c = createHubClient({ baseUrl: '', fetch: fn, session: () => null });
  const e: HubError = await c.publicKey().catch((x) => x);
  expect(e.code).toBe('NOT_CONFIGURED');
  expect(fn).not.toHaveBeenCalled();
});

test('an authenticated route without a session fails as SESSION_INVALID locally', async () => {
  const { fn } = fakeFetch(200, {});
  const e: HubError = await client(fn, null).token('B1').catch((x) => x);
  expect(e.code).toBe('SESSION_INVALID');
  expect(fn).not.toHaveBeenCalled();
});

test('every hub code has a French sentence; unknown codes fall back', () => {
  for (const code of HUB_ERROR_CODES) {
    const m = hubErrorMessage(new HubError(400, code, 'x'));
    expect(m).not.toMatch(/^Erreur/);
  }
  expect(hubErrorMessage(new HubError(500, 'INTERNAL_ERROR', 'boom'))).toMatch(/^Erreur/);
  expect(hubErrorMessage(new HubError(429, 'OTP_RATE_LIMITED', 'x', 90))).toContain('2 min');
});

test('a wrong, an expired and a locked code each say what to do', () => {
  const wrong = hubErrorMessage(new HubError(401, 'OTP_INVALID', 'x'));
  const expired = hubErrorMessage(new HubError(401, 'OTP_EXPIRED', 'x'));
  const locked = hubErrorMessage(new HubError(401, 'OTP_LOCKED', 'x'));
  expect(new Set([wrong, expired, locked]).size).toBe(3);
  expect(wrong).toMatch(/incorrect/i);
  expect(expired).toMatch(/expiré/);
  expect(expired).toMatch(/nouveau code/);
  expect(locked).toMatch(/nouveau code/);
});

test('the per-address rate limit (kernel body, no code) is worded in French', async () => {
  const { fn } = fakeFetch(429, { error: 'rate_limit_exceeded' });
  const e: HubError = await client(fn).requestOtp('+221771234567').catch((x) => x);
  expect(e).toMatchObject({ status: 429, code: 'HTTP_429' });
  expect(hubErrorMessage(e)).not.toMatch(/^Erreur/);
  expect(hubErrorMessage(e)).toMatch(/Trop de demandes/);
});

test('household members routes: the kernel access token as a bearer, the bodies the hub decodes', async () => {
  const { fn, calls } = fakeFetch(200, {});
  const c = client(fn);
  await c.members('jwt_1', 'T1');
  await c.inviteMember('jwt_1', 'T1', '77 123 45 67', 'viewer');
  await c.cancelInvite('jwt_1', 'T1', 'I1');
  await c.changeRole('jwt_1', 'T1', 'M1', 'admin');
  await c.removeMember('jwt_1', 'T1', 'M1');
  expect(calls.map((x) => `${x.init.method} ${x.url.replace('https://hub.test', '')}`)).toEqual([
    'GET /api/v1/tenants/T1/members',
    'POST /api/v1/tenants/T1/members/invites',
    'DELETE /api/v1/tenants/T1/members/invites/I1',
    'PATCH /api/v1/tenants/T1/members/M1',
    'DELETE /api/v1/tenants/T1/members/M1',
  ]);
  expect(calls.map((x) => (x.init.body ? JSON.parse(String(x.init.body)) : null))).toEqual([
    null,
    { phone: '77 123 45 67', role: 'viewer' },
    null,
    { role: 'admin' },
    null,
  ]);
  for (const x of calls) expect((x.init.headers as Record<string, string>).Authorization).toBe('Bearer jwt_1');
});

test('the members refusals have French wording', () => {
  for (const code of ['ALREADY_MEMBER', 'OWNER_ONLY', 'INVITE_LIMIT', 'INVALID_ROLE', 'OWNER_UNTOUCHED', 'FORBIDDEN']) {
    expect(HUB_ERROR_CODES).toContain(code);
    expect(hubErrorMessage(new HubError(400, code, 'x'))).not.toMatch(/^Erreur/);
  }
});
