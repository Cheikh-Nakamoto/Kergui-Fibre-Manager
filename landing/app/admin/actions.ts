'use server';

import { cookies } from 'next/headers';
import { redirect } from 'next/navigation';
import { revalidatePath } from 'next/cache';
import { hub, HubError, type Recheck } from '../../lib/hub';
import { statusLabel } from '../../lib/format';
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

// --- releases (in-app updates of the website APK) ---------------------------

/** The hub's 409s on releases, said in French; the rest as for any call. */
function releaseMessage(e: unknown, op: string): string {
  if (e instanceof HubError && e.status === 409) {
    // The hub's codes (internal/hub/releases/handler); the text is the fallback.
    const m = e.message;
    if (e.code === 'MINIMUM_NOT_PUBLISHED' || m.includes('minimum must be a published build')) {
      return 'Ce build n’est pas publié : publiez-le avant d’en faire la version minimale.';
    }
    if (e.code === 'MINIMUM_UNCOVERED' || m.includes('minimum stands on this build')) {
      return 'Impossible de retirer : il ne resterait aucune version publiée au-dessus du minimum.';
    }
    if (e.code === 'RELEASE_FILE_CHANGED' || m.includes('another file')) return 'Ce build a déjà été publié avec un autre fichier.';
    if (op === 'publish') return 'Cette version est déjà publiée.';
    if (op === 'withdraw') return 'Cette version n’est pas publiée : il n’y a rien à retirer.';
    return 'L’état de cette version ne permet pas cette action. Rechargez la page.';
  }
  if (e instanceof HubError && e.status === 404) return 'Cette version n’existe plus. Rechargez la page.';
  return message(e);
}

/** Publishes or withdraws a release; the button pressed says which. */
export async function releaseAction(_: FormState, form: FormData): Promise<FormState> {
  const id = String(form.get('id') ?? '');
  const op = String(form.get('op') ?? '');
  if (!id || (op !== 'publish' && op !== 'withdraw')) return { error: 'Action inconnue.' };
  try {
    await hub(`/api/v1/admin/releases/${encodeURIComponent(id)}/${op}`, { method: 'POST' });
    revalidatePath('/admin/versions');
    return { ok: op === 'publish' ? 'Version publiée.' : 'Version retirée.' };
  } catch (e) {
    return { error: releaseMessage(e, op) };
  }
}

/** Replaces a release's notes, shown to users in the update prompt. */
export async function saveReleaseNotes(_: FormState, form: FormData): Promise<FormState> {
  const id = String(form.get('id') ?? '');
  const notes = String(form.get('notes') ?? '').trim();
  if (notes.length > 4000) return { error: 'Les notes dépassent 4 000 caractères.' };
  try {
    await hub(`/api/v1/admin/releases/${encodeURIComponent(id)}/notes`, {
      method: 'PUT',
      body: JSON.stringify({ notes }),
    });
    revalidatePath('/admin/versions');
    return { ok: 'Notes enregistrées.' };
  } catch (e) {
    return { error: releaseMessage(e, 'notes') };
  }
}

/** Sets the build below which the app refuses to run; build 0 clears it. */
export async function setMinimumBuild(_: FormState, form: FormData): Promise<FormState> {
  const body = {
    platform: String(form.get('platform') ?? ''),
    channel: String(form.get('channel') ?? ''),
    build: Number(form.get('build') ?? NaN),
  };
  if (!Number.isInteger(body.build) || body.build < 0) return { error: 'Build invalide.' };
  try {
    const out = await hub<{ minimumBuild: number }>('/api/v1/admin/releases/minimum', {
      method: 'PUT',
      body: JSON.stringify(body),
    });
    revalidatePath('/admin/versions');
    return {
      ok: out.minimumBuild ? `Version minimale : build ${out.minimumBuild}.` : 'Version minimale effacée.',
    };
  } catch (e) {
    return { error: releaseMessage(e, 'minimum') };
  }
}

// --- support (issue #78) ------------------------------------------------------

/** The hub's support refusals, said in French; the rest as for any call. */
function supportMessage(e: unknown): string {
  if (e instanceof HubError) {
    const m = e.message;
    if (e.status === 404) return 'Ce compte n’existe plus. Rechargez la page.';
    if (e.status === 409) return 'Un pass tourne déjà sur la box de destination : fusionner deux passes se fait à la main.';
    if (m.includes('no active pass')) return 'Aucun pass payé sur cette box : offrez-en un depuis « Passes ».';
    if (m.includes('already on this box')) return 'Le pass est déjà sur cette box.';
    if (m.includes('serial')) return 'Numéro de série de box invalide (lettres et chiffres).';
    if (m.includes('reason')) return 'Indiquez la raison du geste (3 à 500 caractères, elle est enregistrée).';
  }
  return message(e);
}

function reasonOf(form: FormData): string {
  return String(form.get('reason') ?? '').trim();
}

/** Extends a box's pass by some days, with a reason the log keeps. */
export async function extendPass(_: FormState, form: FormData): Promise<FormState> {
  const accountId = String(form.get('accountId') ?? '');
  const body = { boxId: String(form.get('boxId') ?? ''), days: Number(form.get('days') ?? NaN), reason: reasonOf(form) };
  if (!Number.isInteger(body.days) || body.days < 1 || body.days > 365) return { error: 'Entre 1 et 365 jours.' };
  if (body.reason.length < 3) return { error: 'Indiquez la raison du geste (elle est enregistrée).' };
  try {
    const out = await hub<{ boxId: string; expiresAt: string }>(
      `/api/v1/admin/support/accounts/${encodeURIComponent(accountId)}/extend`,
      { method: 'POST', body: JSON.stringify(body) },
    );
    revalidatePath(`/admin/support/${accountId}`);
    const until = new Date(out.expiresAt).toLocaleDateString('fr-FR', { timeZone: 'Africa/Dakar' });
    return { ok: `Pass de la box ${out.boxId} prolongé jusqu’au ${until}.` };
  } catch (e) {
    return { error: supportMessage(e) };
  }
}

/** Moves a pass to another box, past the customer's own limit. */
export async function transferPass(_: FormState, form: FormData): Promise<FormState> {
  const accountId = String(form.get('accountId') ?? '');
  const body = {
    fromBoxId: String(form.get('fromBoxId') ?? ''),
    toBoxId: String(form.get('toBoxId') ?? '').trim().toUpperCase(),
    reason: reasonOf(form),
  };
  if (!body.toBoxId) return { error: 'Saisissez le numéro de série de la nouvelle box.' };
  if (body.reason.length < 3) return { error: 'Indiquez la raison du geste (elle est enregistrée).' };
  try {
    const out = await hub<{ boxId: string }>(
      `/api/v1/admin/support/accounts/${encodeURIComponent(accountId)}/transfer`,
      { method: 'POST', body: JSON.stringify(body) },
    );
    revalidatePath(`/admin/support/${accountId}`);
    return { ok: `Pass déplacé sur la box ${out.boxId}.` };
  } catch (e) {
    return { error: supportMessage(e) };
  }
}

/** Signs every phone of the account out of the app and the hub. */
export async function revokeSessions(_: FormState, form: FormData): Promise<FormState> {
  const accountId = String(form.get('accountId') ?? '');
  const reason = reasonOf(form);
  if (reason.length < 3) return { error: 'Indiquez la raison du geste (elle est enregistrée).' };
  try {
    const out = await hub<{ sessionsRevoked: number }>(
      `/api/v1/admin/support/accounts/${encodeURIComponent(accountId)}/revoke-sessions`,
      { method: 'POST', body: JSON.stringify({ reason }) },
    );
    revalidatePath(`/admin/support/${accountId}`);
    return {
      ok:
        out.sessionsRevoked > 0
          ? `${out.sessionsRevoked} session(s) révoquée(s) : le client doit se reconnecter avec un nouveau code.`
          : 'Aucune session active ; les jetons du hub sont révoqués quand même.',
    };
  } catch (e) {
    return { error: supportMessage(e) };
  }
}

// --- payments (issue #79) -----------------------------------------------------

/** Asks NabooPay about one order; a paid one is credited, once. */
export async function recheckPayment(_: FormState, form: FormData): Promise<FormState> {
  const id = String(form.get('paymentId') ?? '');
  const reason = reasonOf(form);
  if (reason.length > 500) return { error: 'La raison dépasse 500 caractères.' };
  try {
    const out = await hub<Recheck>(`/api/v1/admin/payments/${encodeURIComponent(id)}/recheck`, {
      method: 'POST',
      body: JSON.stringify({ reason }),
    });
    revalidatePath('/admin/paiements');
    if (out.credited) return { ok: 'Payé chez NabooPay : le pass est crédité.' };
    if (out.status === 'pending') return { ok: 'Toujours en attente chez NabooPay : rien à créditer pour l’instant.' };
    return { ok: `NabooPay : ${statusLabel[out.status] ?? out.status}.` };
  } catch (e) {
    if (e instanceof HubError) {
      if (e.status === 404) return { error: 'Ce paiement n’existe plus. Rechargez la page.' };
      if (e.status === 503) return { error: 'NabooPay n’est pas configuré sur ce hub.' };
      if (e.status === 502) return { error: 'NabooPay ne répond pas. Réessayez dans un instant.' };
      if (e.status === 400) return { error: 'Seule une commande NabooPay peut être revérifiée.' };
    }
    return { error: message(e) };
  }
}
