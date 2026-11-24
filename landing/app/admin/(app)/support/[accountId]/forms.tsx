'use client';

import { useActionState } from 'react';
import { extendPass, revokeSessions, transferPass, type FormState } from '../../../actions';
import { reloadOnStale } from '../../../../../lib/fresh';

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

function Reason({ placeholder }: { placeholder: string }) {
  return (
    <label>
      Raison (enregistrée dans le journal)
      <input name="reason" required minLength={3} maxLength={500} placeholder={placeholder} />
    </label>
  );
}

/** Adds days to a box's pass: compensation for an outage. */
export function ExtendForm({ accountId, boxes, maxDays }: { accountId: string; boxes: string[]; maxDays: number }) {
  const [state, action, pending] = useActionState<FormState, FormData>(reloadOnStale(extendPass), {});
  return (
    <form
      action={action}
      className="bo-form"
      onSubmit={(e) => {
        const f = new FormData(e.currentTarget);
        if (!confirm(`Prolonger le pass de la box ${f.get('boxId')} de ${f.get('days')} jour(s) ?`)) e.preventDefault();
      }}
    >
      <input type="hidden" name="accountId" value={accountId} />
      <label>
        Box
        <select name="boxId" defaultValue={boxes[0]}>
          {boxes.map((b) => (
            <option key={b} value={b}>
              {b}
            </option>
          ))}
        </select>
      </label>
      <label>
        Jours
        <input name="days" type="number" min={1} max={maxDays} defaultValue={7} required />
      </label>
      <Reason placeholder="ex. panne fibre du 12 au 19 nov." />
      <Feedback state={state} />
      <div className="bo-row">
        <button type="submit" className="bo-btn small" disabled={pending}>
          {pending ? 'Envoi…' : 'Prolonger'}
        </button>
      </div>
    </form>
  );
}

/** Moves a pass to another box, past the customer's own transfer limit. */
export function TransferForm({ accountId, boxes }: { accountId: string; boxes: string[] }) {
  const [state, action, pending] = useActionState<FormState, FormData>(reloadOnStale(transferPass), {});
  return (
    <form
      action={action}
      className="bo-form"
      onSubmit={(e) => {
        const f = new FormData(e.currentTarget);
        const ask = `Déplacer le pass de la box ${f.get('fromBoxId')} vers la box ${String(f.get('toBoxId')).toUpperCase()} ? L’ancienne box repasse en Gratuit.`;
        if (!confirm(ask)) e.preventDefault();
      }}
    >
      <input type="hidden" name="accountId" value={accountId} />
      <label>
        De la box
        <select name="fromBoxId" defaultValue={boxes[0]}>
          {boxes.map((b) => (
            <option key={b} value={b}>
              {b}
            </option>
          ))}
        </select>
      </label>
      <label>
        Vers la box (numéro de série)
        <input name="toBoxId" required maxLength={64} pattern="[A-Za-z0-9]+" placeholder="ZTEG1A2B3C4D" className="bo-mono" />
      </label>
      <Reason placeholder="ex. box remplacée par l’opérateur" />
      <Feedback state={state} />
      <div className="bo-row">
        <button type="submit" className="bo-btn small" disabled={pending}>
          {pending ? 'Envoi…' : 'Déplacer'}
        </button>
      </div>
    </form>
  );
}

/** Signs every phone of the account out: a lost or stolen phone. */
export function RevokeForm({ accountId, phone, active }: { accountId: string; phone: string; active: number }) {
  const [state, action, pending] = useActionState<FormState, FormData>(reloadOnStale(revokeSessions), {});
  return (
    <form
      action={action}
      className="bo-form"
      onSubmit={(e) => {
        const ask = `Déconnecter tous les téléphones de ${phone} (${active} session(s)) ? Le client devra se reconnecter avec un code WhatsApp, qu’il demandera lui-même.`;
        if (!confirm(ask)) e.preventDefault();
      }}
    >
      <input type="hidden" name="accountId" value={accountId} />
      <Reason placeholder="ex. téléphone volé, signalé par le client" />
      <Feedback state={state} />
      <div className="bo-row">
        <button type="submit" className="bo-btn small danger" disabled={pending}>
          {pending ? 'Envoi…' : 'Révoquer les sessions'}
        </button>
      </div>
    </form>
  );
}
