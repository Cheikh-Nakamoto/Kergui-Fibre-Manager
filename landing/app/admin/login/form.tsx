'use client';

import { useActionState } from 'react';
import { login, type FormState } from '../actions';

export function LoginForm() {
  const [state, action, pending] = useActionState<FormState, FormData>(login, {});
  return (
    <form action={action} className="ad-form">
      <label>
        E-mail
        <input name="email" type="email" autoComplete="username" required />
      </label>
      <label>
        Mot de passe
        <input name="password" type="password" autoComplete="current-password" required />
      </label>
      {state.error ? (
        <p className="ad-error" role="alert">
          {state.error}
        </p>
      ) : null}
      <button type="submit" className="ad-btn" disabled={pending}>
        {pending ? 'Connexion…' : 'Se connecter'}
      </button>
    </form>
  );
}
