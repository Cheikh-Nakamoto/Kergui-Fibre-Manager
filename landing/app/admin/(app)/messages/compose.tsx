'use client';

import { useActionState, useState } from 'react';
import { previewMessage, sendMessage, type MessageState } from '../../actions';
import { reloadOnStale } from '../../../../lib/fresh';
import { audienceLabel, reachLabel, tierLabel } from '../../../../lib/format';

/**
 * Writes an announcement, counts who it reaches (« Aperçu »), and sends it
 * after a confirmation that names that count. Sending waits for an
 * up-to-date preview of the audience chosen.
 */
export function ComposeForm({ limits, push }: { limits: { title: number; body: number }; push: boolean }) {
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [preview, previewAction, previewing] = useActionState<MessageState, FormData>(reloadOnStale(previewMessage), {});
  const [sent, sendAction, sending] = useActionState<MessageState, FormData>(
    reloadOnStale(async (prev: MessageState, form: FormData) => {
      const out = await sendMessage(prev, form);
      // A sent announcement clears the form for the next one.
      if (out.ok) {
        setTitle('');
        setBody('');
      }
      return out;
    }),
    {},
  );
  const [audience, setAudience] = useState<'all' | 'tier' | 'household'>('all');
  const [tier, setTier] = useState('max');
  const [household, setHousehold] = useState('');

  const value = audience === 'tier' ? tier : audience === 'household' ? household.trim() : '';
  const key = `${audience}|${value}`;
  const current = preview.preview && preview.key === key ? preview.preview : undefined;

  const target = current ? audienceLabel(current.audience, current.value, current.household) : '';
  return (
    <form
      action={sendAction}
      className="bo-form bo-compose"
      onSubmit={(e) => {
        const submitter = (e.nativeEvent as SubmitEvent).submitter as HTMLButtonElement | null;
        if (submitter?.name === 'preview') return;
        if (!current) {
          e.preventDefault();
          return;
        }
        const ask =
          `Envoyer « ${title.trim()} » par notification ?\n\n` +
          `${target} : ${reachLabel(current.households, current.phones)}.\n` +
          'Un message envoyé ne peut pas être retiré.';
        if (!confirm(ask)) e.preventDefault();
      }}
    >
      <label>
        Titre ({[...title].length}/{limits.title})
        <input
          name="title"
          required
          maxLength={limits.title}
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="ex. Coupure prévue ce soir"
        />
      </label>
      <label>
        Texte ({[...body].length}/{limits.body})
        <textarea
          name="body"
          required
          rows={4}
          maxLength={limits.body}
          value={body}
          onChange={(e) => setBody(e.target.value)}
          placeholder="ex. Le réseau sera en maintenance de 22 h à minuit. Rien à faire de votre côté."
        />
      </label>

      <fieldset className="bo-choices">
        <legend>Destinataires</legend>
        <label>
          <input type="radio" name="audience" value="all" checked={audience === 'all'} onChange={() => setAudience('all')} />
          Tous les foyers
        </label>
        <label>
          <input type="radio" name="audience" value="tier" checked={audience === 'tier'} onChange={() => setAudience('tier')} />
          Un niveau
        </label>
        {audience === 'tier' ? (
          <select name="tier" value={tier} onChange={(e) => setTier(e.target.value)} aria-label="Niveau">
            {['free', 'basic', 'max'].map((t) => (
              <option key={t} value={t}>
                {tierLabel[t]}
              </option>
            ))}
          </select>
        ) : null}
        <label>
          <input
            type="radio"
            name="audience"
            value="household"
            checked={audience === 'household'}
            onChange={() => setAudience('household')}
          />
          Un foyer
        </label>
        {audience === 'household' ? (
          <input
            name="household"
            value={household}
            onChange={(e) => setHousehold(e.target.value)}
            placeholder="Numéro (77…, +221…) ou identifiant du foyer"
            aria-label="Numéro ou identifiant du foyer"
          />
        ) : null}
      </fieldset>

      {preview.error ? (
        <p className="bo-error" role="alert">
          {preview.error}
        </p>
      ) : current ? (
        <p className="bo-ok">
          {audienceLabel(current.audience, current.value, current.household)} : {reachLabel(current.households, current.phones)}
          {current.householdsWithApp < current.households ? (
            <small className="bo-muted"> · {current.householdsWithApp} foyer(s) avec l’app</small>
          ) : null}
          {current.household?.name ? <small className="bo-muted"> · {current.household.name}</small> : null}
        </p>
      ) : (
        <p className="bo-muted">Faites l’aperçu pour savoir combien de téléphones recevront le message.</p>
      )}
      {sent.error ? (
        <p className="bo-error" role="alert">
          {sent.error}
        </p>
      ) : sent.ok ? (
        <p className="bo-ok">{sent.ok}</p>
      ) : null}

      <div className="bo-row">
        <button type="submit" name="preview" formAction={previewAction} formNoValidate className="bo-btn ghost" disabled={previewing}>
          {previewing ? 'Calcul…' : 'Aperçu'}
        </button>
        <button
          type="submit"
          className="bo-btn"
          disabled={!push || sending || !current || current.phones === 0 || !title.trim() || !body.trim()}
        >
          {sending ? 'Envoi…' : current ? `Envoyer à ${current.phones} téléphone${current.phones > 1 ? 's' : ''}` : 'Envoyer'}
        </button>
      </div>
    </form>
  );
}
