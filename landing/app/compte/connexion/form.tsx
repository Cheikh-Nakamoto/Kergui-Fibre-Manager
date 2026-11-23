'use client';

import { useActionState } from 'react';
import { signIn, type SignInState } from '../actions';
import { reloadOnStale } from '../../../lib/fresh';

export function SignInForm() {
  const [state, action, pending] = useActionState<SignInState, FormData>(reloadOnStale(signIn), { step: 'phone' });

  if (state.step === 'phone') {
    return (
      <form action={action} className="cl-form">
        <label>
          Numéro WhatsApp
          <input
            name="phone"
            type="tel"
            inputMode="tel"
            autoComplete="tel"
            placeholder="77 123 45 67"
            defaultValue={state.phone}
            required
            autoFocus
          />
        </label>
        {state.error ? (
          <p className="cl-error" role="alert">
            {state.error}
          </p>
        ) : null}
        <button type="submit" className="cl-btn" disabled={pending}>
          {pending ? 'Envoi…' : 'Recevoir le code sur WhatsApp'}
        </button>
      </form>
    );
  }

  return (
    <form action={action} className="cl-form">
      <p>
        Code envoyé au <strong className="cl-mono">{state.phone}</strong>.
      </p>
      <label>
        Code reçu sur WhatsApp
        <input
          name="code"
          type="text"
          inputMode="numeric"
          autoComplete="one-time-code"
          pattern="[0-9 ]{6,7}"
          maxLength={7}
          className="cl-code"
          required
          autoFocus
        />
      </label>
      {state.error ? (
        <p className="cl-error" role="alert">
          {state.error}
        </p>
      ) : state.info ? (
        <p className="cl-info" role="status">
          {state.info}
        </p>
      ) : null}
      <button type="submit" className="cl-btn" disabled={pending}>
        {pending ? 'Vérification…' : 'Se connecter'}
      </button>
      <button type="submit" name="intent" value="resend" className="cl-btn ghost" formNoValidate disabled={pending}>
        Renvoyer un code
      </button>
      <button type="submit" name="intent" value="change" className="cl-btn link" formNoValidate disabled={pending}>
        Changer de numéro
      </button>
    </form>
  );
}
