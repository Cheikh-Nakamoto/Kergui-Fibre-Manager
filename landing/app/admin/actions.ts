'use server';

import { cookies } from 'next/headers';
import { redirect } from 'next/navigation';
import { revalidatePath } from 'next/cache';
import { hub, HubError } from '../../lib/hub';
import { ACCESS_COOKIE, REFRESH_COOKIE, REFRESH_MAX_AGE, cookieOptions, hubURL } from '../../lib/session';

export type FormState = { error?: string; ok?: string; code?: string };

function message(e: unknown): string {
  if (e instanceof HubError) return e.message;
  return 'Le serveur Kergui ne répond pas. Réessayez dans un instant.';
}

/** Sign-in: the hub's own login, then a check that the account is a platform admin. */
export async function login(_: FormState, form: FormData): Promise<FormState> {
  const email = String(form.get('email') ?? '').trim();
  const password = String(form.get('password') ?? '');
  if (!email || !password) return { error: 'Saisissez votre e-mail et votre mot de passe.' };

  let tokens: { accessToken?: string; refreshToken?: string };
  try {
    const res = await fetch(`${hubURL()}/api/v1/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
      cache: 'no-store',
    });
    if (res.status === 401 || res.status === 400) return { error: 'E-mail ou mot de passe incorrect.' };
    if (res.status === 429) return { error: 'Trop de tentatives. Patientez une minute.' };
    if (!res.ok) return { error: `Le serveur Kergui a répondu ${res.status}.` };
    tokens = await res.json();
  } catch {
    return { error: 'Le serveur Kergui ne répond pas.' };
  }
  if (!tokens.accessToken || !tokens.refreshToken) return { error: 'Réponse inattendue du serveur.' };

  const me = await fetch(`${hubURL()}/api/v1/admin/me`, {
    headers: { Authorization: `Bearer ${tokens.accessToken}` },
    cache: 'no-store',
  }).catch(() => null);
  if (!me || me.status === 403) {
    return { error: 'Ce compte n’est pas administrateur de Kergui.' };
  }
  if (!me.ok) return { error: `Le serveur Kergui a répondu ${me.status}.` };

  const jar = await cookies();
  jar.set(ACCESS_COOKIE, tokens.accessToken, cookieOptions);
  jar.set(REFRESH_COOKIE, tokens.refreshToken, { ...cookieOptions, maxAge: REFRESH_MAX_AGE });
  redirect('/admin');
}

export async function logout(): Promise<void> {
  const jar = await cookies();
  const refresh = jar.get(REFRESH_COOKIE)?.value;
  if (refresh) {
    await fetch(`${hubURL()}/api/v1/auth/logout`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refreshToken: refresh }),
      cache: 'no-store',
    }).catch(() => null);
  }
  jar.delete({ name: ACCESS_COOKIE, path: '/admin' });
  jar.delete({ name: REFRESH_COOKIE, path: '/admin' });
  redirect('/admin/login');
}

/** Grants a pass by hand (support), with a reason the audit keeps. */
export async function grantPass(_: FormState, form: FormData): Promise<FormState> {
  const body = {
    accountId: String(form.get('accountId') ?? ''),
    boxId: String(form.get('boxId') ?? ''),
    passCode: String(form.get('passCode') ?? ''),
    reason: String(form.get('reason') ?? '').trim(),
  };
  if (body.reason.length < 3) return { error: 'Indiquez la raison du geste (elle est enregistrée).' };
  try {
    const out = await hub<{ tier: string; expiresAt: string; boxId: string }>('/api/v1/admin/passes/grant', {
      method: 'POST',
      body: JSON.stringify(body),
    });
    revalidatePath('/admin/passes');
    const until = new Date(out.expiresAt).toLocaleDateString('fr-FR', { timeZone: 'Africa/Dakar' });
    return {
      ok: out.boxId
        ? `Pass accordé : ${out.tier} jusqu’au ${until} sur la box ${out.boxId}.`
        : 'Pass accordé : il attend la prochaine connexion de ce numéro sur sa box.',
    };
  } catch (e) {
    return { error: message(e) };
  }
}

/** Starts linking the WhatsApp number: returns the pairing code to type. */
export async function linkWhatsApp(_: FormState, form: FormData): Promise<FormState> {
  const number = String(form.get('number') ?? '').trim();
  try {
    const out = await hub<{ code: string }>('/api/v1/admin/whatsapp/link', {
      method: 'POST',
      body: number ? JSON.stringify({ number }) : undefined,
    });
    return { code: out.code };
  } catch (e) {
    return { error: message(e) };
  }
}

export async function unlinkWhatsApp(): Promise<void> {
  await hub('/api/v1/admin/whatsapp', { method: 'DELETE' });
  revalidatePath('/admin/whatsapp');
}
