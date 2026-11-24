'use client';

import { useActionState } from 'react';
import { answerComplaint, closeComplaint, setComplaintAdmins, type FormState } from '../../actions';
import { reloadOnStale } from '../../../../lib/fresh';

function Feedback({ state }: { state: FormState }) {
  if (state.error) {
    return (
      <p className="bo-error" role="alert">
        {state.error}
      </p>
    );
  }
  return state.ok ? <p className="bo-ok">{state.ok}</p> : null;
}

/** The answer: push to the customer's phones, WhatsApp if they opted in. */
export function AnswerForm({ id, max }: { id: string; max: number }) {
  const [state, action, pending] = useActionState<FormState, FormData>(reloadOnStale(answerComplaint), {});
  return (
    <form
      action={action}
      className="bo-form wide"
      onSubmit={(e) => {
        if (!confirm('Envoyer cette réponse au client ?')) e.preventDefault();
      }}
    >
      <input type="hidden" name="id" value={id} />
      <label>
        Réponse au client
        <textarea name="message" required maxLength={max} rows={5} placeholder="Bonjour, …" />
      </label>
      <Feedback state={state} />
      <div className="bo-row">
        <button type="submit" className="bo-btn small" disabled={pending}>
          {pending ? 'Envoi…' : 'Répondre'}
        </button>
      </div>
    </form>
  );
}

/** Closes the complaint; the action log keeps who did. */
export function CloseForm({ id, number }: { id: string; number: number }) {
  const [state, action, pending] = useActionState<FormState, FormData>(reloadOnStale(closeComplaint), {});
  return (
    <form
      action={action}
      className="bo-form"
      onSubmit={(e) => {
        if (!confirm(`Clore la plainte n° ${number} ? Le client ne pourra plus y répondre.`)) e.preventDefault();
      }}
    >
      <input type="hidden" name="id" value={id} />
      <Feedback state={state} />
      <div className="bo-row">
        <button type="submit" className="bo-btn small ghost" disabled={pending}>
          {pending ? 'Clôture…' : 'Clore la plainte'}
        </button>
      </div>
    </form>
  );
}

/** The admins' WhatsApp numbers: one per line, any form the sign-in accepts. */
export function AdminsForm({ numbers, max }: { numbers: string[]; max: number }) {
  const [state, action, pending] = useActionState<FormState, FormData>(reloadOnStale(setComplaintAdmins), {});
  return (
    <form action={action} className="bo-form">
      <label>
        Numéros WhatsApp des admins (un par ligne, {max} au plus)
        <textarea name="numbers" rows={4} defaultValue={numbers.join('\n')} placeholder={'77 000 00 00\n+221 78 000 00 00'} />
      </label>
      <Feedback state={state} />
      <div className="bo-row">
        <button type="submit" className="bo-btn small" disabled={pending}>
          {pending ? 'Enregistrement…' : 'Enregistrer'}
        </button>
      </div>
    </form>
  );
}
