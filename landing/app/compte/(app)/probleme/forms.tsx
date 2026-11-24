'use client';

import { useActionState } from 'react';
import { replyComplaint, reportProblem, type ComplaintFormState } from '../../actions';
import { reloadOnStale } from '../../../../lib/fresh';
import { COMPLAINT_CATEGORIES, COMPLAINT_MAX_MESSAGE } from '../../../../lib/passes';
import { complaintCategoryLabel } from '../../../../lib/format';

function Notice({ state }: { state: ComplaintFormState }) {
  if (state.error) {
    return (
      <p className="cl-error" role="alert">
        {state.error}
      </p>
    );
  }
  if (state.ok) {
    return (
      <p className="cl-ok" role="status">
        {state.ok}
      </p>
    );
  }
  return null;
}

/** A category and a message; the hub answers with the complaint's number. */
export function ReportForm() {
  const [state, action, pending] = useActionState<ComplaintFormState, FormData>(reloadOnStale(reportProblem), {});
  return (
    <form action={action} className="cl-form">
      <fieldset>
        <legend>De quoi s’agit-il ?</legend>
        <div className="cl-choices">
          {COMPLAINT_CATEGORIES.map((c) => (
            <label key={c} className="cl-choice">
              <input type="radio" name="category" value={c} defaultChecked={state.category === c} required />
              <strong>{complaintCategoryLabel[c]}</strong>
            </label>
          ))}
        </div>
      </fieldset>
      <label>
        Décrivez le problème
        <textarea
          name="message"
          rows={5}
          maxLength={COMPLAINT_MAX_MESSAGE}
          defaultValue={state.message ?? ''}
          placeholder="Ce qui se passe, depuis quand, sur quelle box ou quel paiement."
          required
        />
      </label>
      <Notice state={state} />
      <div>
        <button type="submit" className="cl-btn" disabled={pending}>
          {pending ? 'Envoi…' : 'Envoyer'}
        </button>
      </div>
    </form>
  );
}

/** The customer's answer on the thread of complaint n° `number`. */
export function ReplyForm({ number }: { number: number }) {
  const [state, action, pending] = useActionState<ComplaintFormState, FormData>(reloadOnStale(replyComplaint), {});
  return (
    <form action={action} className="cl-form">
      <input type="hidden" name="number" value={number} />
      <label>
        Répondre
        <textarea
          name="message"
          rows={3}
          maxLength={COMPLAINT_MAX_MESSAGE}
          defaultValue={state.message ?? ''}
          required
        />
      </label>
      <Notice state={state} />
      <div>
        <button type="submit" className="cl-btn small" disabled={pending}>
          {pending ? 'Envoi…' : 'Envoyer'}
        </button>
      </div>
    </form>
  );
}
