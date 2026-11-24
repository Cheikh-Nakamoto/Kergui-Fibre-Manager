import Link from 'next/link';
import { hub, type Messages } from '../../../../lib/hub';
import { audienceLabel, dateTime } from '../../../../lib/format';
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
          niveau d’un foyer est le plus haut pass en cours parmi ses box (Gratuit sinon).
        </p>
      </div>

      <section className="bo-card">
        <h2>Nouvelle annonce</h2>
        {!data.push ? (
          <p className="bo-banner warn">Les notifications de l’app ne sont pas configurées sur ce hub : envoi impossible.</p>
        ) : null}
        <ComposeForm limits={data.limits} push={data.push} />
      </section>

      <section className="bo-card flush">
        <h2 className="bo-pad">Historique</h2>
        <div className="bo-table-wrap">
          <table className="bo-table">
            <thead>
              <tr>
                <th>Envoyé</th>
                <th>Message</th>
                <th>Destinataires</th>
                <th>Téléphones</th>
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
                      {m.households} foyer{m.households > 1 ? 's' : ''} · {m.channel === 'push' ? 'notification' : m.channel}
                    </small>
                  </td>
                  <td>
                    <span className={`bo-pill ${m.failed > 0 ? 'warn' : 'ok'}`}>
                      {m.sent}/{m.phones} envoyé{m.sent > 1 ? 's' : ''}
                    </span>
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
