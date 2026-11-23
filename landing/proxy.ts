import { NextResponse, type NextRequest } from 'next/server';
import { ACCESS_COOKIE, REFRESH_COOKIE, REFRESH_MAX_AGE, cookieOptions, hubURL, secondsLeft } from './lib/session';

// Before any /admin page renders: send a visitor with no session to the
// sign-in page, and renew an access token about to expire, so the pages
// (which cannot set cookies while rendering) always hold a valid one.
export async function proxy(request: NextRequest) {
  const { pathname } = request.nextUrl;
  if (pathname === '/admin/login') return NextResponse.next();

  const access = request.cookies.get(ACCESS_COOKIE)?.value;
  const refresh = request.cookies.get(REFRESH_COOKIE)?.value;
  const left = secondsLeft(access);
  if (access && left !== null && left > 30) return NextResponse.next();

  const toLogin = () => {
    const res = NextResponse.redirect(new URL('/admin/login', request.url));
    res.cookies.delete({ name: ACCESS_COOKIE, path: '/admin' });
    res.cookies.delete({ name: REFRESH_COOKIE, path: '/admin' });
    return res;
  };
  if (!refresh) return toLogin();

  let tokens: { accessToken?: string; refreshToken?: string };
  try {
    const r = await fetch(`${hubURL()}/api/v1/auth/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refreshToken: refresh }),
      cache: 'no-store',
    });
    if (!r.ok) return toLogin();
    tokens = await r.json();
  } catch {
    return toLogin();
  }
  if (!tokens.accessToken || !tokens.refreshToken) return toLogin();

  // The renewed tokens reach this render (request) and the browser (response).
  request.cookies.set(ACCESS_COOKIE, tokens.accessToken);
  request.cookies.set(REFRESH_COOKIE, tokens.refreshToken);
  const res = NextResponse.next({ request });
  res.cookies.set(ACCESS_COOKIE, tokens.accessToken, cookieOptions);
  res.cookies.set(REFRESH_COOKIE, tokens.refreshToken, { ...cookieOptions, maxAge: REFRESH_MAX_AGE });
  return res;
}

export const config = { matcher: ['/admin/:path*'] };
