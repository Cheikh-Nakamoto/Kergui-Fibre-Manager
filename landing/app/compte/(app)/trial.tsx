'use client';

import { useActionState } from 'react';
import { requestTrial, type FormState } from '../actions';
import { reloadOnStale } from '../../../lib/fresh';

export function TrialButton() {
  const [state, action, pending] = useActionState<FormState, FormData>(
    reloadOnStale(() => requestTrial()),
    {},
  );
  return (
    <form action={action} className="cl-form">
      {state.error ? (
        <p className="cl-error" role="alert">
          {state.error}
        </p>
      ) : null}
      {state.ok ? (
        <p className="cl-ok" role="status">
          {state.ok}
        </p>
      ) : (
        <div>
          <button type="submit" className="cl-btn" disabled={pending}>
            {pending ? 'Demande…' : 'Demander l’essai gratuit'}
          </button>
        </div>
      )}
    </form>
  );
}
