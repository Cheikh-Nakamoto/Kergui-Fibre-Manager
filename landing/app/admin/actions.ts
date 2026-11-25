'use server';

import { cookies } from 'next/headers';
import { redirect } from 'next/navigation';
import { revalidatePath } from 'next/cache';
import {
  hub,
  HubError,
  type ComplaintAdmins,
  type ComplaintAnswer,
  type MessagePreview,
  type MessageSent,
  type Notified,
  type Recheck,
} from '../../lib/hub';
import { complaintWhatsAppLabel, reachLabel, statusLabel, tierLabel } from '../../lib/format';
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

// --- a household's pass (issue #98) ---------------------------------------------

/** The hub's refusals on a household's pass gestures, said in French. */
function householdPassMessage(e: unknown): string {
  if (e instanceof HubError) {
    const m = e.message;
    if (e.status === 404) return 'Ce foyer n’existe plus. Rechargez la page.';
    if (m.includes('no phone account')) return 'Ce foyer n’a pas de compte par téléphone : il ne peut pas avoir de pass.';
    if (m.includes('has a lifetime pass')) return 'Cette box a un pass à vie : retirez-le d’abord pour la renouveler.';
    if (m.includes('no lifetime pass')) return 'Cette box n’a pas de pass à vie. Rechargez la page.';
    if (m.startsWith('days')) return 'Entre 1 et 365 jours.';
    if (m.startsWith('amountXof')) return 'Montant invalide (en francs CFA, sans décimales).';
    if (m.startsWith('tier')) return 'Choisissez Basic ou Max.';
    if (m.includes('serial')) return 'Numéro de série de box invalide (lettres et chiffres).';
    if (m.includes('reason')) return 'Indiquez le motif (3 à 500 caractères, il est enregistré).';
  }
  return message(e);
}

/** What the household's phones heard of it. */
function notifiedLabel(n?: Notified): string {
  if (!n || n.off) return ' Notifications de l’app non configurées : le foyer n’a pas été prévenu.';
  if (n.error) return ` Le foyer n’a pas pu être prévenu (${n.error}).`;
  if (n.phones === 0) return ' Aucun téléphone du foyer à notifier.';
  const failed = n.failed > 0 ? `, ${n.failed} échec(s)` : '';
  return ` Notification envoyée à ${n.sent} téléphone(s)${failed}.`;
}

function day(iso: string): string {
  return new Date(iso).toLocaleDateString('fr-FR', { timeZone: 'Africa/Dakar' });
}

function householdPath(form: FormData): { tenantId: string; base: string } {
  const tenantId = String(form.get('tenantId') ?? '');
  return { tenantId, base: `/api/v1/admin/households/${encodeURIComponent(tenantId)}/passes` };
}

/** Renews or extends a box's pass by hand: tier, days, money received (optional). */
export async function renewHouseholdPass(_: FormState, form: FormData): Promise<FormState> {
  const { tenantId, base } = householdPath(form);
  const rawAmount = String(form.get('amountXof') ?? '').replace(/\s/g, '');
  const body = {
    boxId: String(form.get('boxId') ?? '').trim().toUpperCase(),
    tier: String(form.get('tier') ?? ''),
    days: Number(form.get('days') ?? NaN),
    amountXof: rawAmount === '' ? 0 : Number(rawAmount),
    reason: reasonOf(form),
  };
  if (!body.boxId) return { error: 'Choisissez la box ou saisissez son numéro de série.' };
  if (!Number.isInteger(body.days) || body.days < 1 || body.days > 365) return { error: 'Entre 1 et 365 jours.' };
  if (!Number.isInteger(body.amountXof) || body.amountXof < 0) return { error: 'Montant invalide (en francs CFA, sans décimales).' };
  if (body.reason.length < 3) return { error: 'Indiquez le motif (il est enregistré).' };
  try {
    const out = await hub<{ boxId: string; tier: string; expiresAt: string; notified?: Notified }>(`${base}/renew`, {
      method: 'POST',
      body: JSON.stringify(body),
    });
    revalidatePath(`/admin/foyers/${tenantId}`);
    const paid = body.amountXof > 0 ? ` ${body.amountXof} F comptés dans les revenus.` : ' Offert (0 F).';
    return {
      ok: `Pass ${tierLabel[out.tier] ?? out.tier} de la box ${out.boxId} jusqu’au ${day(out.expiresAt)}.${paid}${notifiedLabel(out.notified)}`,
    };
  } catch (e) {
    return { error: householdPassMessage(e) };
  }
}

/** Gives a box a pass with no end, revocable from the same page. */
export async function grantLifetimePass(_: FormState, form: FormData): Promise<FormState> {
  const { tenantId, base } = householdPath(form);
  const body = {
    boxId: String(form.get('boxId') ?? '').trim().toUpperCase(),
    tier: String(form.get('tier') ?? ''),
    reason: reasonOf(form),
  };
  if (!body.boxId) return { error: 'Choisissez la box ou saisissez son numéro de série.' };
  if (body.reason.length < 3) return { error: 'Indiquez le motif (il est enregistré).' };
  try {
    const out = await hub<{ boxId: string; tier: string; notified?: Notified }>(`${base}/lifetime`, {
      method: 'POST',
      body: JSON.stringify(body),
    });
    revalidatePath(`/admin/foyers/${tenantId}`);
    return { ok: `Pass ${tierLabel[out.tier] ?? out.tier} à vie sur la box ${out.boxId}.${notifiedLabel(out.notified)}` };
  } catch (e) {
    return { error: householdPassMessage(e) };
  }
}

/** Takes a lifetime pass back: the pass that ran before returns if it still runs. */
export async function revokeLifetimePass(_: FormState, form: FormData): Promise<FormState> {
  const { tenantId, base } = householdPath(form);
  const body = { boxId: String(form.get('boxId') ?? ''), reason: reasonOf(form) };
  if (body.reason.length < 3) return { error: 'Indiquez le motif (il est enregistré).' };
  try {
    const out = await hub<{ boxId: string; tier: string; expiresAt: string; active: boolean; notified?: Notified }>(
      `${base}/lifetime/revoke`,
      { method: 'POST', body: JSON.stringify(body) },
    );
    revalidatePath(`/admin/foyers/${tenantId}`);
    const now = out.active
      ? `retour au pass ${tierLabel[out.tier] ?? out.tier} jusqu’au ${day(out.expiresAt)}`
      : 'la box repasse en Gratuit';
    return { ok: `Pass à vie retiré de la box ${out.boxId} : ${now}.${notifiedLabel(out.notified)}` };
  } catch (e) {
    return { error: householdPassMessage(e) };
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

// --- messages (issue #80) -----------------------------------------------------

export type MessageState = FormState & {
  preview?: MessagePreview;
  /** What the preview was made for: "channel|audience|value". */
  key?: string;
};

type MessageTarget = { channel: 'push' | 'whatsapp'; audience: string; value: string; numbers: string[] };

/** One number per line (commas and semicolons too), blank lines dropped. */
function numbersOf(text: string): string[] {
  return text
    .split(/[\n,;]+/)
    .map((n) => n.trim())
    .filter(Boolean);
}

function targetOf(form: FormData): MessageTarget {
  const channel = form.get('channel') === 'whatsapp' ? 'whatsapp' : 'push';
  const audience = String(form.get('audience') ?? (channel === 'whatsapp' ? 'household' : 'all'));
  const numbers = audience === 'numbers' ? numbersOf(String(form.get('numbers') ?? '')) : [];
  const value =
    audience === 'tier'
      ? String(form.get('tier') ?? '')
      : audience === 'household'
        ? String(form.get('household') ?? '').trim()
        : audience === 'numbers'
          ? numbers.join('\n')
          : '';
  return { channel, audience, value, numbers };
}

function messageError(e: unknown, channel: 'push' | 'whatsapp'): string {
  if (e instanceof HubError) {
    if (e.status === 404) return 'Aucun foyer pour ce numéro ou cet identifiant.';
    if (e.status === 409)
      return channel === 'whatsapp'
        ? 'Aucun numéro n’a activé WhatsApp dans ce public : rien n’a été envoyé.'
        : 'Aucun téléphone à joindre dans ce public : rien n’a été envoyé.';
    if (e.status === 503)
      return channel === 'whatsapp'
        ? 'WhatsApp n’est pas configuré sur ce hub.'
        : 'Les notifications de l’app ne sont pas configurées sur ce hub.';
    if (e.status === 400) {
      if (e.message.startsWith('title')) return 'Le titre doit faire de 1 à 80 caractères.';
      if (e.message.startsWith('body')) return 'Le texte doit faire de 1 à 500 caractères.';
      if (e.message.startsWith('value')) return 'Indiquez un numéro (77…, +221…) ou l’identifiant du foyer.';
      if (e.message.startsWith('numbers: at most'))
        return `Trop de numéros : ${e.message.match(/\d+/)?.[0] ?? '20'} au plus à la fois.`;
      if (e.message.startsWith('numbers')) return 'Indiquez au moins un numéro, un par ligne.';
    }
  }
  return message(e);
}

function hubBody(t: MessageTarget): Record<string, unknown> {
  return t.audience === 'numbers'
    ? { channel: t.channel, audience: t.audience, numbers: t.numbers }
    : { channel: t.channel, audience: t.audience, value: t.value };
}

/** Counts who a message would reach (and, for WhatsApp, who is refused). */
export async function previewMessage(_: MessageState, form: FormData): Promise<MessageState> {
  const t = targetOf(form);
  if (t.audience === 'household' && !t.value) return { error: 'Indiquez le numéro ou l’identifiant du foyer.' };
  if (t.audience === 'numbers' && t.numbers.length === 0) return { error: 'Indiquez au moins un numéro, un par ligne.' };
  try {
    const preview = await hub<MessagePreview>('/api/v1/admin/messages/preview', {
      method: 'POST',
      body: JSON.stringify(hubBody(t)),
    });
    return { preview, key: `${t.channel}|${t.audience}|${t.value}`, ok: `${reachLabel(preview.households, preview.phones)}.` };
  } catch (e) {
    return { error: messageError(e, t.channel) };
  }
}

/** Sends a message by push, or queues it for WhatsApp. */
export async function sendMessage(_: MessageState, form: FormData): Promise<MessageState> {
  const title = String(form.get('title') ?? '').trim();
  const body = String(form.get('body') ?? '').trim();
  const t = targetOf(form);
  if (!title || [...title].length > 80) return { error: 'Le titre doit faire de 1 à 80 caractères.' };
  if (!body || [...body].length > 500) return { error: 'Le texte doit faire de 1 à 500 caractères.' };
  try {
    const out = await hub<MessageSent>('/api/v1/admin/messages', {
      method: 'POST',
      body: JSON.stringify({ title, body, ...hubBody(t) }),
    });
    revalidatePath('/admin/messages');
    if (t.channel === 'whatsapp') {
      const refused = out.refused?.length ? ` ${out.refused.length} numéro(s) refusé(s).` : '';
      return {
        ok: `En file : ${out.queued} message(s) WhatsApp, un toutes les ${out.intervalSeconds ?? 40} s.${refused}`,
      };
    }
    const failed = out.failed > 0 ? ` ${out.failed} échec(s).` : '';
    return { ok: `Envoyé à ${out.sent} téléphone(s) (${reachLabel(out.households, out.phones)}).${failed}` };
  } catch (e) {
    return { error: messageError(e, t.channel) };
  }
}

// --- complaints (issue #82) -----------------------------------------------------

function complaintError(e: unknown): string {
  if (e instanceof HubError) {
    if (e.status === 404) return 'Cette plainte n’existe plus. Rechargez la page.';
    if (e.status === 409) return 'Cette plainte est close : elle ne prend plus de réponse.';
    if (e.status === 400 && e.message.startsWith('message')) return 'La réponse doit faire de 1 à 2 000 caractères.';
  }
  return message(e);
}

/** Answers a complaint: push to the customer's phones, WhatsApp if opted in. */
export async function answerComplaint(_: FormState, form: FormData): Promise<FormState> {
  const id = String(form.get('id') ?? '');
  const body = String(form.get('message') ?? '').trim();
  if (!body || [...body].length > 2000) return { error: 'La réponse doit faire de 1 à 2 000 caractères.' };
  try {
    const out = await hub<ComplaintAnswer>(`/api/v1/admin/complaints/${encodeURIComponent(id)}/answer`, {
      method: 'POST',
      body: JSON.stringify({ message: body }),
    });
    revalidatePath(`/admin/plaintes/${out.complaint.number}`);
    revalidatePath('/admin/plaintes');
    const d = out.delivery;
    const push = d.pushed > 0 ? `notification sur ${d.pushed} téléphone(s)` : 'aucun téléphone à notifier';
    const failed = d.pushFailed > 0 ? `, ${d.pushFailed} échec(s)` : '';
    const wa = d.whatsapp ? `, ${complaintWhatsAppLabel[d.whatsapp] ?? d.whatsapp}` : '';
    return { ok: `Réponse envoyée : ${push}${failed}${wa}.` };
  } catch (e) {
    return { error: complaintError(e) };
  }
}

/** Closes a complaint (logged). */
export async function closeComplaint(_: FormState, form: FormData): Promise<FormState> {
  const id = String(form.get('id') ?? '');
  try {
    await hub(`/api/v1/admin/complaints/${encodeURIComponent(id)}/close`, { method: 'POST' });
    revalidatePath('/admin/plaintes', 'layout');
    return { ok: 'Plainte close.' };
  } catch (e) {
    return { error: complaintError(e) };
  }
}

/** Sets the admins' WhatsApp numbers: one per line. */
export async function setComplaintAdmins(_: FormState, form: FormData): Promise<FormState> {
  const numbers = numbersOf(String(form.get('numbers') ?? ''));
  try {
    const out = await hub<ComplaintAdmins>('/api/v1/admin/settings/complaint-admins', {
      method: 'PUT',
      body: JSON.stringify({ numbers }),
    });
    revalidatePath('/admin/plaintes');
    return {
      ok: out.numbers.length
        ? `${out.numbers.length} numéro(s) enregistré(s) : ${out.numbers.join(', ')}.`
        : 'Aucun numéro admin : personne ne recevra le résumé des plaintes.',
    };
  } catch (e) {
    if (e instanceof HubError && e.status === 400) {
      if (e.message.startsWith('numbers: not a phone number'))
        return { error: `Numéro invalide : ${e.message.split(': ').pop()}.` };
      if (e.message.startsWith('numbers: at most')) return { error: `Trop de numéros : ${e.message.match(/\d+/)?.[0]} au plus.` };
    }
    return { error: message(e) };
  }
}
