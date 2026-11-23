import 'server-only';
import { cookies } from 'next/headers';
import { redirect } from 'next/navigation';
import { CLIENT_COOKIE, hubURL } from './session';
import { HubError } from './hub';

export { HubError };

export const SIGN_IN = '/compte/connexion';

/** Calls the hub's licensing API as the signed-in customer. 401 sends back to sign-in. */
export async function licensing<T>(path: string, init: RequestInit = {}): Promise<T> {
  const token = (await cookies()).get(CLIENT_COOKIE)?.value;
  if (!token) redirect(SIGN_IN);
  const res = await fetch(`${hubURL()}/licensing/v1/${path}`, {
    ...init,
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}`, ...(init.headers ?? {}) },
    cache: 'no-store',
  });
  if (res.status === 401) redirect(`${SIGN_IN}?expire=1`);
  if (res.status === 204) return {} as T;
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = (body as { error?: { code?: string; message?: string } }).error ?? {};
    throw new HubError(res.status, err.code ?? 'ERROR', err.message ?? `hub answered ${res.status}`);
  }
  return body as T;
}

/** The public catalogue (no session). */
export async function catalogue(): Promise<Catalogue> {
  const res = await fetch(`${hubURL()}/licensing/v1/passes`, { cache: 'no-store' });
  if (!res.ok) throw new HubError(res.status, 'ERROR', `hub answered ${res.status}`);
  return res.json();
}

export * from './passes';
import type { Catalogue } from './passes';
