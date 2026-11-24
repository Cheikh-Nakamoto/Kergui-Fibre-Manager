'use client';

import { useActionState } from 'react';
import { recheckPayment, type FormState } from '../../actions';
import { reloadOnStale } from '../../../../lib/fresh';

/** « Revérifier »: asks NabooPay about one order, after a confirmation. */
export function RecheckForm({ paymentId, reference, phone }: { paymentId: string; reference: string; phone: string }) {
  const [state, action, pending] = useActionState<FormState, FormData>(reloadOnStale(recheckPayment), {});
  return (
    <form
      action={action}
      className="bo-form"
      onSubmit={(e) => {
        if (!confirm(`Demander à NabooPay l’état de la commande ${reference} (${phone}) ? Si elle est payée, le pass est crédité.`)) {
          e.preventDefault();
        }
      }}
    >
      <input type="hidden" name="paymentId" value={paymentId} />
      <label>
        Raison (facultative, enregistrée)
        <input name="reason" maxLength={500} placeholder="ex. le client a payé, rien reçu" />
      </label>
      {state.error ? (
        <p className="bo-error" role="alert">
          {state.error}
        </p>
      ) : state.ok ? (
        <p className="bo-ok">{state.ok}</p>
      ) : null}
      <div className="bo-row">
        <button type="submit" className="bo-btn small" disabled={pending}>
          {pending ? 'Vérification…' : 'Revérifier'}
        </button>
      </div>
    </form>
  );
}
