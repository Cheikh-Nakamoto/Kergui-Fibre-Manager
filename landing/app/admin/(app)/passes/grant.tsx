'use client';

import { useActionState, useState } from 'react';
import { grantPass, type FormState } from '../../actions';

type Pass = { code: string; tier: string; days: number; priceXof: number };

export function GrantForm({ accountId, boxes, catalogue }: { accountId: string; boxes: string[]; catalogue: Pass[] }) {
  const [open, setOpen] = useState(false);
  const [state, action, pending] = useActionState<FormState, FormData>(grantPass, {});
  if (!open) {
    return (
      <button type="button" className="ad-btn ghost small" onClick={() => setOpen(true)}>
        Offrir un pass
      </button>
    );
  }
  return (
    <form action={action} className="ad-form compact">
      <input type="hidden" name="accountId" value={accountId} />
      <label>
        Pass
        <select name="passCode" defaultValue="max_1m">
          {catalogue.map((p) => (
            <option key={p.code} value={p.code}>
              {p.tier === 'max' ? 'Max' : 'Basic'} {p.days} jours
            </option>
          ))}
        </select>
      </label>
      <label>
        Box
        <select name="boxId" defaultValue={boxes[0] ?? ''}>
          {boxes.map((b) => (
            <option key={b} value={b}>
              {b}
            </option>
          ))}
          <option value="">À la prochaine connexion</option>
        </select>
      </label>
      <label>
        Raison (enregistrée)
        <input name="reason" required minLength={3} maxLength={500} placeholder="ex. panne réseau du 12 nov." />
      </label>
      {state.error ? (
        <p className="ad-error" role="alert">
          {state.error}
        </p>
      ) : null}
      {state.ok ? <p className="ad-ok">{state.ok}</p> : null}
      <div className="ad-row">
        <button type="submit" className="ad-btn small" disabled={pending}>
          {pending ? 'Envoi…' : 'Accorder'}
        </button>
        <button type="button" className="ad-btn ghost small" onClick={() => setOpen(false)}>
          Fermer
        </button>
      </div>
    </form>
  );
}
