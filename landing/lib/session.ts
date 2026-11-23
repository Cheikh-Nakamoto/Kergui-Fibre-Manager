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
