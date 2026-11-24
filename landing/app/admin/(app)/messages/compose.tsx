'use client';

import { useActionState, useState } from 'react';
import { previewMessage, sendMessage, type MessageState } from '../../actions';
import { reloadOnStale } from '../../../../lib/fresh';
import { audienceLabel, durationLabel, reachLabel, refusedLabel, tierLabel } from '../../../../lib/format';
import type { Messages } from '../../../../lib/hub';

type Channel = 'push' | 'whatsapp';
type Audience = 'all' | 'tier' | 'household' | 'numbers';

/** One number per line (commas and semicolons too), as the server action reads them. */
function numbersOf(text: string): string[] {
  return text
    .split(/[\n,;]+/)
    .map((n) => n.trim())
    .filter(Boolean);
}

/**
 * Writes a message, counts who it reaches (« Aperçu »), and sends it after a
 * confirmation that names that count. Push goes to everyone, a level or a
 * household; WhatsApp only to a household or a few numbers, queued one
 * every 40 s. Sending waits for an up-to-date preview of the audience chosen.
 */
export function ComposeForm({
  limits,
  push,
  whatsapp,
}: {
  limits: { title: number; body: number };
  push: boolean;
  whatsapp: Messages['whatsapp'];
}) {
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [preview, previewAction, previewing] = useActionState<MessageState, FormData>(reloadOnStale(previewMessage), {});
  const [sent, sendAction, sending] = useActionState<MessageState, FormData>(
    reloadOnStale(async (prev: MessageState, form: FormData) => {
      const out = await sendMessage(prev, form);
      // A sent message clears the form for the next one.
      if (out.ok) {
        setTitle('');
        setBody('');
      }
      return out;
    }),
    {},
  );
  const [channel, setChannel] = useState<Channel>('push');
  const [audience, setAudience] = useState<Audience>('all');
  const [tier, setTier] = useState('max');
  const [household, setHousehold] = useState('');
  const [numbers, setNumbers] = useState('');

  const pickChannel = (c: Channel) => {
    setChannel(c);
    // WhatsApp never goes to everyone or to a level.
    if (c === 'whatsapp' && (audience === 'all' || audience === 'tier')) setAudience('household');
    if (c === 'push' && audience === 'numbers') setAudience('all');
  };

  const value =
    audience === 'tier'
      ? tier
      : audience === 'household'
        ? household.trim()
        : audience === 'numbers'
          ? numbersOf(numbers).join('\n')
          : '';
  const key = `${channel}|${audience}|${value}`;
  const current = preview.preview && preview.key === key ? preview.preview : undefined;
  const interval = whatsapp.intervalSeconds;
  const typed = numbersOf(numbers).length;

  const target = current ? audienceLabel(current.audience, current.value, current.household) : '';
  const canSend = channel === 'push' ? push : whatsapp.enabled;
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
          channel === 'whatsapp'
            ? `Envoyer « ${title.trim()} » par WhatsApp ?\n\n` +
              `${target} : ${current.phones} numéro(s), un message toutes les ${interval} s ` +
              `(${durationLabel(current.estimatedSeconds ?? 0)}).\n` +
              'Un message envoyé ne peut pas être retiré.'
            : `Envoyer « ${title.trim()} » par notification ?\n\n` +
              `${target} : ${reachLabel(current.households, current.phones)}.\n` +
              'Un message envoyé ne peut pas être retiré.';
        if (!confirm(ask)) e.preventDefault();
      }}
    >
      <fieldset className="bo-choices">
        <legend>Canal</legend>
        <label>
          <input type="radio" name="channel" value="push" checked={channel === 'push'} onChange={() => pickChannel('push')} />
          Notification (app)
        </label>
        <label>
          <input
            type="radio"
            name="channel"
            value="whatsapp"
            checked={channel === 'whatsapp'}
            onChange={() => pickChannel('whatsapp')}
          />
          WhatsApp
        </label>
      </fieldset>
      {channel === 'whatsapp' ? (
        <p className="bo-banner warn">
          WhatsApp : {interval} s entre deux messages, pour ne pas faire bannir le numéro Kergui. Seulement un foyer ou{' '}
          {whatsapp.groupMax} numéros au plus, et seulement ceux qui ont activé WhatsApp (STOP respecté).
          {whatsapp.pending > 0 ? ` ${whatsapp.pending} message(s) déjà en file.` : ''}
          {!whatsapp.enabled ? ' WhatsApp n’est pas configuré sur ce hub : envoi impossible.' : ''}
        </p>
      ) : null}

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
        {channel === 'push' ? (
          <>
            <label>
              <input type="radio" name="audience" value="all" checked={audience === 'all'} onChange={() => setAudience('all')} />
              Tous les foyers
            </label>
            <label>
              <input
                type="radio"
                name="audience"
                value="tier"
                checked={audience === 'tier'}
                onChange={() => setAudience('tier')}
              />
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
          </>
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
        {channel === 'whatsapp' ? (
          <>
            <label>
              <input
                type="radio"
                name="audience"
                value="numbers"
                checked={audience === 'numbers'}
                onChange={() => setAudience('numbers')}
              />
              Des numéros
            </label>
            {audience === 'numbers' ? (
              <>
                <textarea
                  name="numbers"
                  rows={5}
                  value={numbers}
                  onChange={(e) => setNumbers(e.target.value)}
                  placeholder={'Un numéro par ligne\n77 123 45 67\n+221 78 765 43 21'}
                  aria-label="Numéros, un par ligne"
                />
                <small className={typed > whatsapp.groupMax ? 'bo-error' : 'bo-muted'}>
                  {typed}/{whatsapp.groupMax} numéro(s)
                </small>
              </>
            ) : null}
          </>
        ) : null}
      </fieldset>

      {preview.error ? (
        <p className="bo-error" role="alert">
          {preview.error}
        </p>
      ) : current && current.channel === 'whatsapp' ? (
        <div className="bo-ok">
          <p>
            {current.phones} numéro{current.phones > 1 ? 's' : ''} recevr{current.phones > 1 ? 'ont' : 'a'} le message
            {current.household?.name ? <small className="bo-muted"> · {current.household.name}</small> : null}
            {current.phones > 0 ? (
              <small className="bo-muted">
                {' '}
                · WhatsApp : {current.intervalSeconds ?? interval} s entre deux messages, {current.phones} message
                {current.phones > 1 ? 's' : ''} {durationLabel(current.estimatedSeconds ?? 0)}
                {current.ahead ? ` (${current.ahead} déjà en file avant)` : ''}
              </small>
            ) : null}
          </p>
          {current.recipients?.length ? <small className="bo-muted">{current.recipients.join(', ')}</small> : null}
          {current.refused?.length ? (
            <ul className="bo-refused">
              {current.refused.map((r) => (
                <li key={r.number}>
                  <span className="bo-pill warn">refusé</span> {r.number} — {refusedLabel[r.reason] ?? r.reason}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : current ? (
        <p className="bo-ok">
          {audienceLabel(current.audience, current.value, current.household)} : {reachLabel(current.households, current.phones)}
          {current.householdsWithApp < current.households ? (
            <small className="bo-muted"> · {current.householdsWithApp} foyer(s) avec l’app</small>
          ) : null}
          {current.household?.name ? <small className="bo-muted"> · {current.household.name}</small> : null}
        </p>
      ) : (
        <p className="bo-muted">
          {channel === 'whatsapp'
            ? 'Faites l’aperçu pour savoir quels numéros recevront le message et lesquels sont refusés.'
            : 'Faites l’aperçu pour savoir combien de téléphones recevront le message.'}
        </p>
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
          disabled={!canSend || sending || !current || current.phones === 0 || !title.trim() || !body.trim()}
        >
          {sending
            ? 'Envoi…'
            : current
              ? channel === 'whatsapp'
                ? `Mettre en file ${current.phones} message${current.phones > 1 ? 's' : ''} WhatsApp`
                : `Envoyer à ${current.phones} téléphone${current.phones > 1 ? 's' : ''}`
              : 'Envoyer'}
        </button>
      </div>
    </form>
  );
}
