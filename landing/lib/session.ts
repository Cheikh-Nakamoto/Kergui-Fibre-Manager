// The back-office session: the hub's access and refresh tokens, in httpOnly
// cookies scoped to /admin. Page scripts never see them; only the Next
// server reads them to call the hub.

export const ACCESS_COOKIE = 'kg_admin_at';
export const REFRESH_COOKIE = 'kg_admin_rt';

export const cookieOptions = {
  httpOnly: true,
  secure: process.env.NODE_ENV === 'production',
  sameSite: 'strict' as const,
  path: '/admin',
};

/** The refresh token lives as long as the hub keeps it (30 days by default). */
export const REFRESH_MAX_AGE = 30 * 24 * 3600;

/** The hub, as the Next server reaches it (runtime env, not baked in the build). */
export function hubURL(): string {
  const raw = process.env.KERGUI_HUB_URL || process.env.NEXT_PUBLIC_HUB_URL || 'http://localhost:8090';
  return raw.replace(/\/+$/, '');
}

/** Seconds until the JWT expires (negative when it has), or null if unreadable. */
export function secondsLeft(jwt: string | undefined): number | null {
  if (!jwt) return null;
  const part = jwt.split('.')[1];
  if (!part) return null;
  try {
    const json = JSON.parse(Buffer.from(part.replace(/-/g, '+').replace(/_/g, '/'), 'base64').toString('utf8'));
    return typeof json.exp === 'number' ? json.exp - Math.floor(Date.now() / 1000) : null;
  } catch {
    return null;
  }
}

// The customer area's session (/compte): the hub's kgs_ licensing token
// (signed in with a WhatsApp code), in an httpOnly cookie scoped to /compte.
// It lasts as long as the hub keeps it (a year); there is no refresh.
export const CLIENT_COOKIE = 'kg_client';
/** The signed-in number, for display only (never trusted for access). */
export const CLIENT_PHONE_COOKIE = 'kg_client_phone';

export const clientCookieOptions = {
  httpOnly: true,
  secure: process.env.NODE_ENV === 'production',
  sameSite: 'lax' as const, // lax: NabooPay sends the payer back with a top-level GET
  path: '/compte',
};
