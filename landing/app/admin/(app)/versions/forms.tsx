'use client';

import { useActionState, useState } from 'react';
import { releaseAction, saveReleaseNotes, setMinimumBuild, type FormState } from '../../actions';
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

/** "Publier" for a pending or withdrawn release, "Retirer" (confirmed) for a published one. */
export function PublishButton({ id, published, label }: { id: string; published: boolean; label: string }) {
  const [state, action, pending] = useActionState<FormState, FormData>(reloadOnStale(releaseAction), {});
  const op = published ? 'withdraw' : 'publish';
  return (
    <form
      action={action}
      className="bo-form"
      onSubmit={(e) => {
        const ask = published
          ? `Retirer la version ${label} ? Elle ne sera plus proposée aux téléphones.`
          : `Publier la version ${label} ? Elle sera proposée à tous les téléphones.`;
        if (!confirm(ask)) e.preventDefault();
      }}
    >
      <input type="hidden" name="id" value={id} />
      <input type="hidden" name="op" value={op} />
      <button type="submit" className={`bo-btn small${published ? ' danger' : ''}`} disabled={pending}>
        {pending ? 'Envoi…' : published ? 'Retirer' : 'Publier'}
      </button>
      <Feedback state={state} />
    </form>
  );
}

/** Makes a build the minimum (build > 0), or clears the minimum (build 0). */
export function MinimumButton({
  platform,
  channel,
  build,
  label,
}: {
  platform: string;
  channel: string;
  build: number;
  label?: string;
}) {
  const [state, action, pending] = useActionState<FormState, FormData>(reloadOnStale(setMinimumBuild), {});
  const clear = build === 0;
  return (
    <form
      action={action}
      className="bo-form"
      onSubmit={(e) => {
        const ask = clear
          ? 'Effacer la version minimale ? Plus aucun téléphone ne sera obligé de se mettre à jour.'
          : `Faire de ${label ?? `build ${build}`} la version minimale ? Les téléphones plus anciens devront se mettre à jour avant de continuer.`;
        if (!confirm(ask)) e.preventDefault();
      }}
    >
      <input type="hidden" name="platform" value={platform} />
      <input type="hidden" name="channel" value={channel} />
      <input type="hidden" name="build" value={build} />
      <button type="submit" className="bo-btn ghost small" disabled={pending}>
        {pending ? 'Envoi…' : clear ? 'Effacer la version minimale' : 'Version minimale'}
      </button>
      <Feedback state={state} />
    </form>
  );
}

/** The release notes shown in the update prompt, edited in place. */
export function NotesForm({ id, notes }: { id: string; notes: string }) {
  const [open, setOpen] = useState(false);
  const [state, action, pending] = useActionState<FormState, FormData>(reloadOnStale(saveReleaseNotes), {});
  if (!open) {
    return (
      <div className="bo-notes">
        {notes ? <p className="bo-notes-text">{notes}</p> : <p className="bo-muted">Aucune note.</p>}
        <button type="button" className="bo-btn ghost small" onClick={() => setOpen(true)}>
          Modifier les notes
        </button>
        {state.ok ? <p className="bo-ok">{state.ok}</p> : null}
      </div>
    );
  }
  return (
    <form action={action} className="bo-form compact">
      <input type="hidden" name="id" value={id} />
      <label>
        Notes de version
        <textarea name="notes" defaultValue={notes} maxLength={4000} rows={4} />
      </label>
      <Feedback state={state} />
      <div className="bo-row">
        <button type="submit" className="bo-btn small" disabled={pending}>
          {pending ? 'Envoi…' : 'Enregistrer'}
        </button>
        <button type="button" className="bo-btn ghost small" onClick={() => setOpen(false)}>
          Fermer
        </button>
      </div>
    </form>
  );
}
