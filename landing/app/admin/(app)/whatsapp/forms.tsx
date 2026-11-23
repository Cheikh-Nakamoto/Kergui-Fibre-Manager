'use client';

import { useActionState, useTransition } from 'react';
import { linkWhatsApp, unlinkWhatsApp, type FormState } from '../../actions';
import { reloadOnStale } from '../../../../lib/fresh';

export function LinkForm() {
  const [state, action, pending] = useActionState<FormState, FormData>(reloadOnStale(linkWhatsApp), {});
  return (
    <form action={action} className="bo-form">
      <label>
        Numéro (facultatif)
        <input name="number" type="tel" placeholder="+221 77 000 00 00" autoComplete="off" />
      </label>
      {state.error ? (
        <p className="bo-error" role="alert">
          {state.error}
        </p>
      ) : null}
      {state.code ? (
        <div className="bo-code" aria-live="polite">
          <span>Code à saisir sur le téléphone</span>
          <strong>{state.code}</strong>
        </div>
      ) : null}
      <button type="submit" className="bo-btn" disabled={pending}>
        {pending ? 'Demande…' : state.code ? 'Demander un nouveau code' : 'Obtenir le code d’appairage'}
      </button>
    </form>
  );
}

export function UnlinkButton() {
  const [pending, start] = useTransition();
  return (
    <button
      type="button"
      className="bo-btn danger"
      disabled={pending}
      onClick={() => {
        if (confirm('Délier le numéro WhatsApp ? Plus aucun message ne partira jusqu’à un nouvel appairage.')) {
          start(() => unlinkWhatsApp());
        }
      }}
    >
      {pending ? 'Déliaison…' : 'Délier ce numéro'}
    </button>
  );
}
