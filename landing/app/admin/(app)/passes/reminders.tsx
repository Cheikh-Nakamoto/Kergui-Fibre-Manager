'use client';

import { useActionState } from 'react';
import { setRenewalReminders, type FormState } from '../../actions';
import { reloadOnStale } from '../../../../lib/fresh';
import { dateTime } from '../../../../lib/format';
import type { RemindersSetting } from '../../../../lib/hub';

/** The renewal reminders' on/off switch (issue #99), for every household at once. */
export function RemindersSwitch({ setting }: { setting: RemindersSetting }) {
  const [state, action, pending] = useActionState<FormState, FormData>(reloadOnStale(setRenewalReminders), {});
  const on = setting.enabled;
  return (
    <form action={action} className="bo-form compact">
      <input type="hidden" name="enabled" value={on ? 'off' : 'on'} />
      <p>
        <strong>Rappels automatiques : </strong>
        <span className={`bo-pill ${on ? 'ok' : 'off'}`}>{on ? 'activés' : 'désactivés'}</span>
        <br />
        <small className="bo-muted">
          Un message WhatsApp avec le lien pour renouveler, et une notification dans l’application, 3 jours avant la fin
          du pass puis le jour même. Ils partent entre {setting.fromHour}h et {setting.toHour}h (heure de Dakar), une
          seule fois par pass et par étape. Rien pour les passes à vie ni les abonnements Google Play.
          {setting.updatedAt ? ` Modifié le ${dateTime(setting.updatedAt)}.` : ''}
        </small>
      </p>
      {state.error ? (
        <p className="bo-error" role="alert">
          {state.error}
        </p>
      ) : state.ok ? (
        <p className="bo-ok">{state.ok}</p>
      ) : null}
      <div className="bo-row">
        <button type="submit" className={`bo-btn small${on ? ' ghost' : ''}`} disabled={pending}>
          {pending ? 'Enregistrement…' : on ? 'Désactiver les rappels' : 'Activer les rappels'}
        </button>
      </div>
    </form>
  );
}
