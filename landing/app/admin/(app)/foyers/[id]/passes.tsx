'use client';

import { useActionState, useState } from 'react';
import { grantLifetimePass, renewHouseholdPass, revokeLifetimePass, type FormState } from '../../../actions';
import { reloadOnStale } from '../../../../../lib/fresh';

// The pass gestures of a household's page (issue #98). Each asks for a reason,
// which the action log keeps, and the household is told by push.

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
      Motif (enregistré dans le journal)
      <input name="reason" required minLength={3} maxLength={500} placeholder={placeholder} />
    </label>
  );
}

const OTHER = '__other';

/** A box of the account, or a serial typed by hand (a box not bound yet). */
function BoxField({ boxes }: { boxes: string[] }) {
  const [choice, setChoice] = useState(boxes[0] ?? OTHER);
  return (
    <>
      <label>
        Box
        <select value={choice} onChange={(e) => setChoice(e.target.value)} name={choice === OTHER ? undefined : 'boxId'}>
          {boxes.map((b) => (
            <option key={b} value={b}>
              {b}
            </option>
          ))}
          <option value={OTHER}>Une autre box (numéro de série)</option>
        </select>
      </label>
      {choice === OTHER ? (
        <label>
          Numéro de série
          <input name="boxId" required maxLength={64} pattern="[A-Za-z0-9]+" placeholder="ZTEG1A2B3C4D" className="bo-mono" />
        </label>
      ) : null}
    </>
  );
}

function TierField() {
  return (
    <label>
      Niveau
      <select name="tier" defaultValue="max">
        <option value="basic">Basic</option>
        <option value="max">Max</option>
      </select>
    </label>
  );
}

const tierName = (f: FormData) => (f.get('tier') === 'basic' ? 'Basic' : 'Max');

/**
 * Renews or extends a box's pass by hand: from the current end while it runs,
 * from today otherwise. An amount received counts in the revenue; none, it
 * is a gift at 0 F.
 */
export function RenewForm({ tenantId, boxes, maxDays }: { tenantId: string; boxes: string[]; maxDays: number }) {
  const [state, action, pending] = useActionState<FormState, FormData>(reloadOnStale(renewHouseholdPass), {});
  return (
    <form
      action={action}
      className="bo-form"
      onSubmit={(e) => {
        const f = new FormData(e.currentTarget);
        const amount = String(f.get('amountXof') ?? '').trim();
        const paid = amount && amount !== '0' ? `, ${amount} F reçus` : ', offert';
        const ask = `Renouveler le pass ${tierName(f)} de la box ${f.get('boxId')} de ${f.get('days')} jour(s)${paid} ?`;
        if (!confirm(ask)) e.preventDefault();
      }}
    >
      <input type="hidden" name="tenantId" value={tenantId} />
      <BoxField boxes={boxes} />
      <TierField />
      <label>
        Durée (jours)
        <input name="days" type="number" min={1} max={maxDays} defaultValue={30} required />
      </label>
      <label>
        Montant reçu (F CFA, facultatif)
        <input name="amountXof" type="number" min={0} step={1} inputMode="numeric" placeholder="ex. 2000 ; vide si offert" />
      </label>
      <Reason placeholder="ex. paiement Wave reçu à la main" />
      <Feedback state={state} />
      <div className="bo-row">
        <button type="submit" className="bo-btn small" disabled={pending}>
          {pending ? 'Envoi…' : 'Renouveler'}
        </button>
      </div>
    </form>
  );
}

/** Gives a box a pass with no end: a friend, a partner, a tester. */
export function LifetimeForm({ tenantId, boxes }: { tenantId: string; boxes: string[] }) {
  const [state, action, pending] = useActionState<FormState, FormData>(reloadOnStale(grantLifetimePass), {});
  return (
    <form
      action={action}
      className="bo-form"
      onSubmit={(e) => {
        const f = new FormData(e.currentTarget);
        const ask = `Accorder un pass ${tierName(f)} à vie sur la box ${f.get('boxId')} ? Il ne s’arrête que si vous le retirez.`;
        if (!confirm(ask)) e.preventDefault();
      }}
    >
      <input type="hidden" name="tenantId" value={tenantId} />
      <BoxField boxes={boxes} />
      <TierField />
      <Reason placeholder="ex. testeur de la bêta, partenaire" />
      <Feedback state={state} />
      <div className="bo-row">
        <button type="submit" className="bo-btn small" disabled={pending}>
          {pending ? 'Envoi…' : 'Accorder à vie'}
        </button>
      </div>
    </form>
  );
}

/** Takes a lifetime pass back. */
export function RevokeLifetimeForm({ tenantId, boxId, tier }: { tenantId: string; boxId: string; tier: string }) {
  const [state, action, pending] = useActionState<FormState, FormData>(reloadOnStale(revokeLifetimePass), {});
  return (
    <form
      action={action}
      className="bo-form compact"
      onSubmit={(e) => {
        const ask = `Retirer le pass ${tier} à vie de la box ${boxId} ? Le pass d’avant revient s’il court encore, sinon la box repasse en Gratuit.`;
        if (!confirm(ask)) e.preventDefault();
      }}
    >
      <input type="hidden" name="tenantId" value={tenantId} />
      <input type="hidden" name="boxId" value={boxId} />
      <Reason placeholder="ex. fin du partenariat" />
      <Feedback state={state} />
      <div className="bo-row">
        <button type="submit" className="bo-btn small danger" disabled={pending}>
          {pending ? 'Envoi…' : 'Retirer le pass à vie'}
        </button>
      </div>
    </form>
  );
}
