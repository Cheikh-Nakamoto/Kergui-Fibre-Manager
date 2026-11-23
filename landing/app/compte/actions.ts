'use server';

import { cookies } from 'next/headers';
import { redirect } from 'next/navigation';
import { revalidatePath } from 'next/cache';
import { licensing, HubError, SIGN_IN } from '../../lib/account';
import { CLIENT_COOKIE, CLIENT_PHONE_COOKIE, clientCookieOptions, hubURL } from '../../lib/session';

export type SignInState = { step: 'phone' | 'code'; phone?: string; error?: string; info?: string };
export type FormState = { error?: string; ok?: string };

const DOWN = 'Le serveur Kergui ne répond pas. Réessayez dans un instant.';

// The hub's error codes, in words a customer understands.
const messages: Record<string, string> = {
  INVALID_PHONE: 'Ce numéro n’est pas valide. Exemple : 77 123 45 67.',
  OTP_RATE_LIMITED: 'Un code vient d’être envoyé. Patientez un peu avant d’en demander un autre.',
  OTP_INVALID: 'Ce code est incorrect.',
  OTP_EXPIRED: 'Ce code a expiré. Demandez-en un nouveau.',
  OTP_LOCKED: 'Trop d’essais avec ce code. Demandez-en un nouveau.',
  WHATSAPP_UNAVAILABLE: 'WhatsApp est momentanément indisponible. Réessayez dans quelques minutes.',
  INVALID_BOX: 'Ce numéro de série de box n’est pas valide.',
  UNKNOWN_PASS: 'Ce pass n’existe pas.',
  MAX_ACTIVE: 'Un pass Max est actif sur cette box : un pass Basic n’y ajouterait rien. Prenez un pass Max pour prolonger.',
  PAYMENTS_OFF: 'Les paiements ne sont pas encore ouverts.',
  PAYMENT_UNAVAILABLE: 'Le service de paiement ne répond pas. Réessayez dans quelques minutes.',
  TRIAL_PHONE_USED: 'Ce numéro a déjà eu son essai gratuit.',
  TRIAL_BOX_USED: 'Cette box a déjà eu son essai gratuit.',
};

function message(e: unknown): string {
  if (e instanceof HubError) return messages[e.code] ?? e.message;
  return DOWN;
}

async function post(path: string, body: unknown): Promise<{ status: number; body: Record<string, unknown> }> {
  const res = await fetch(`${hubURL()}/licensing/v1/${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    cache: 'no-store',
  });
  return { status: res.status, body: await res.json().catch(() => ({})) };
}

function errorCode(body: Record<string, unknown>): string {
  return ((body.error as { code?: string } | undefined)?.code ?? '') as string;
}

/** Step 1: a 6-digit code goes to the number on WhatsApp. Step 2: the code opens the session. */
export async function signIn(prev: SignInState, form: FormData): Promise<SignInState> {
  const intent = String(form.get('intent') ?? '');
  if (intent === 'change') return { step: 'phone', phone: prev.phone };

  const phone = String(form.get('phone') ?? prev.phone ?? '').trim();
  if (!phone) return { step: 'phone', error: 'Saisissez votre numéro WhatsApp.' };

  if (prev.step === 'phone' || intent === 'resend') {
    try {
      const r = await post('otp', { phone });
      if (r.status === 202) {
        return { step: 'code', phone: String(r.body.phone ?? phone), info: 'Code envoyé sur WhatsApp.' };
      }
      const code = errorCode(r.body);
      return { step: prev.step, phone, error: messages[code] ?? `Le serveur Kergui a répondu ${r.status}.` };
    } catch {
      return { step: prev.step, phone, error: DOWN };
    }
  }

  const code = String(form.get('code') ?? '').replace(/\D/g, '');
  if (code.length !== 6) return { ...prev, info: undefined, error: 'Le code a 6 chiffres.' };
  let out: { sessionToken?: string; expiresAt?: string; phone?: string };
  try {
    const r = await post('otp/verify', { phone, code, device: 'web' });
    if (r.status !== 200) {
      return { ...prev, info: undefined, error: messages[errorCode(r.body)] ?? `Le serveur Kergui a répondu ${r.status}.` };
    }
    out = r.body;
  } catch {
    return { ...prev, info: undefined, error: DOWN };
  }
  if (!out.sessionToken) return { ...prev, error: 'Réponse inattendue du serveur.' };

  const maxAge = out.expiresAt ? Math.max(60, Math.floor((new Date(out.expiresAt).getTime() - Date.now()) / 1000)) : undefined;
  const jar = await cookies();
  jar.set(CLIENT_COOKIE, out.sessionToken, { ...clientCookieOptions, maxAge });
  jar.set(CLIENT_PHONE_COOKIE, out.phone ?? phone, { ...clientCookieOptions, maxAge });
  redirect('/compte');
}

export async function signOut(): Promise<void> {
  try {
    await licensing('session', { method: 'DELETE' });
  } catch {
    // The cookie goes anyway.
  }
  const jar = await cookies();
  jar.delete({ name: CLIENT_COOKIE, path: '/compte' });
  jar.delete({ name: CLIENT_PHONE_COOKIE, path: '/compte' });
  redirect(SIGN_IN);
}

/** Opens a NabooPay checkout and sends the payer to its page. */
export async function checkout(_: FormState, form: FormData): Promise<FormState> {
  const pass = String(form.get('pass') ?? '');
  const boxChoice = String(form.get('box') ?? '');
  const boxId = (boxChoice === '__new' ? String(form.get('newBox') ?? '') : boxChoice).trim().toUpperCase();
  if (!pass) return { error: 'Choisissez un pass.' };
  if (boxChoice === '__new' && !boxId) return { error: 'Saisissez le numéro de série de la box, ou choisissez « Pas encore de box ».' };
  let url: string;
  try {
    const out = await licensing<{ checkoutUrl: string }>('checkout', {
      method: 'POST',
      body: JSON.stringify({ pass, boxId: boxId === '__none' ? '' : boxId }),
    });
    url = out.checkoutUrl;
  } catch (e) {
    return { error: message(e) };
  }
  if (!url) return { error: 'Le service de paiement n’a pas donné de page de paiement.' };
  redirect(url);
}

/** Asks for the 7-day Max trial; the days start when the app connects to the box. */
export async function requestTrial(): Promise<FormState> {
  try {
    await licensing('trial', { method: 'POST' });
  } catch (e) {
    return { error: message(e) };
  }
  revalidatePath('/compte');
  return { ok: 'Essai demandé. Il démarre à la première connexion de l’application Kergui à votre box.' };
}
