import Link from 'next/link';
import { hub, type Messages } from '../../../../lib/hub';
import { audienceLabel, dateTime, durationLabel } from '../../../../lib/format';
import { ComposeForm } from './compose';

export const metadata = { title: 'Messages' };

export default async function MessagesPage() {
  const data = await hub<Messages>('/api/v1/admin/messages?limit=100');
  return (
    <>
      <div className="bo-head">
        <h1>Messages</h1>
        <p className="bo-muted">
          Une annonce part en notification sur les téléphones des foyers et reste dans l’écran Notifications de l’app. Le
          niveau d’un foyer est le plus haut pass en cours parmi ses box (Gratuit sinon). Par WhatsApp : un foyer ou
          quelques numéros seulement, envoyés un par un ({data.whatsapp.intervalSeconds} s entre deux messages).
        </p>
      </div>

      <section className="bo-card">
        <h2>Nouvelle annonce</h2>
        {!data.push ? (
          <p className="bo-banner warn">
            Les notifications de l’app ne sont pas configurées sur ce hub : envoi par notification impossible.
          </p>
        ) : null}
        <ComposeForm limits={data.limits} push={data.push} whatsapp={data.whatsapp} />
      </section>

      <section className="bo-card flush">
        <h2 className="bo-pad">Historique</h2>
        {data.whatsapp.pending > 0 ? (
          <p className="bo-pad bo-muted">
            File WhatsApp : {data.whatsapp.pending} message(s) en attente,{' '}
            {durationLabel(data.whatsapp.pending * data.whatsapp.intervalSeconds)} pour tout envoyer.
          </p>
        ) : null}
        <div className="bo-table-wrap">
          <table className="bo-table">
            <thead>
              <tr>
                <th>Envoyé</th>
                <th>Message</th>
                <th>Destinataires</th>
                <th>Envoi</th>
                <th>Par</th>
              </tr>
            </thead>
            <tbody>
              {data.messages.map((m) => (
                <tr key={m.id}>
                  <td>{dateTime(m.createdAt)}</td>
                  <td className="bo-wrap-text">
                    <strong>{m.title}</strong>
                    <br />
                    <small className="bo-muted">{m.body}</small>
                  </td>
                  <td>
                    {m.audience === 'household' && m.household ? (
                      <Link href={`/admin/foyers/${m.household.tenantId}`}>
                        {audienceLabel(m.audience, m.value, m.household)}
                      </Link>
                    ) : (
                      audienceLabel(m.audience, m.value)
                    )}
                    <br />
                    <small className="bo-muted">
                      {m.households} foyer{m.households > 1 ? 's' : ''} ·{' '}
                      {m.channel === 'push' ? 'notification' : m.channel === 'whatsapp' ? 'WhatsApp' : m.channel}
                    </small>
                  </td>
                  <td>
                    <span className={`bo-pill ${m.failed > 0 ? 'warn' : m.queued > 0 ? '' : 'ok'}`}>
                      {m.sent}/{m.phones} envoyé{m.sent > 1 ? 's' : ''}
                    </span>
                    {m.queued > 0 ? <small className="bo-muted"> · {m.queued} en attente</small> : null}
                    {m.failed > 0 ? <small className="bo-muted"> · {m.failed} échec(s)</small> : null}
                  </td>
                  <td className="bo-muted">{m.author.email || '—'}</td>
                </tr>
              ))}
              {data.messages.length === 0 ? (
                <tr>
                  <td colSpan={5} className="bo-muted">
                    Aucun message envoyé pour l’instant.
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
      </section>
    </>
  );
}
