import Link from 'next/link';
import { notFound } from 'next/navigation';
import { hub, HubError, type ComplaintDetail } from '../../../../../lib/hub';
import {
  complaintCategoryLabel,
  complaintSourceLabel,
  complaintStatusLabel,
  complaintStatusTone,
  complaintWhatsAppLabel,
  dateTime,
  ko,
} from '../../../../../lib/format';
import { AnswerForm, CloseForm } from '../forms';

export const metadata = { title: 'Plainte' };

/** One complaint: the thread, the app's journal, the answer and the close. */
export default async function ComplaintPage({ params }: { params: Promise<{ ref: string }> }) {
  const { ref } = await params;
  let data: ComplaintDetail;
  try {
    data = await hub<ComplaintDetail>(`/api/v1/admin/complaints/${encodeURIComponent(ref)}`);
  } catch (e) {
    if (e instanceof HubError && (e.status === 404 || e.status === 400)) notFound();
    throw e;
  }
  const c = data.complaint;
  const closed = c.status === 'closed';

  return (
    <>
      <p>
        <Link href="/admin/plaintes">← Plaintes</Link>
      </p>
      <div className="bo-head">
        <h1>
          Plainte n° {c.number}{' '}
          <span className={`bo-pill ${complaintStatusTone[c.status] ?? ''}`}>{complaintStatusLabel[c.status] ?? c.status}</span>
        </h1>
        <p className="bo-muted">
          {complaintCategoryLabel[c.category] ?? c.category} · {complaintSourceLabel[c.source] ?? c.source} · reçue le{' '}
          {dateTime(c.createdAt)}
          {c.closedAt ? ` · close le ${dateTime(c.closedAt)}${c.closedBy?.email ? ` par ${c.closedBy.email}` : ''}` : ''}
        </p>
        <p>
          <span className="bo-mono">
            {c.accountId ? <Link href={`/admin/support/${c.accountId}`}>{c.phone}</Link> : c.phone}
          </span>
          {c.household ? (
            <>
              {' · '}
              <Link href={`/admin/foyers/${c.household.tenantId}`}>{c.household.name}</Link>
            </>
          ) : null}
        </p>
      </div>

      <section className="bo-card">
        <h2>Échanges</h2>
        <ol className="bo-thread">
          {(c.messages ?? []).map((m) => (
            <li key={m.id} className={`bo-msg ${m.from}`}>
              <header>
                <strong>{m.from === 'admin' ? (m.author?.email ?? 'Kergui') : 'Client'}</strong>
                <span>· {dateTime(m.at)}</span>
                <span>· {complaintSourceLabel[m.channel] ?? m.channel}</span>
                {m.from === 'admin' ? (
                  <span>
                    · notifié sur {m.pushed ?? 0} téléphone(s)
                    {m.pushFailed ? `, ${m.pushFailed} échec(s)` : ''}
                    {m.whatsapp ? `, ${complaintWhatsAppLabel[m.whatsapp] ?? m.whatsapp}` : ''}
                  </span>
                ) : null}
              </header>
              <p>{m.body}</p>
            </li>
          ))}
        </ol>
      </section>

      {c.hasJournal ? (
        <section className="bo-card">
          <details className="bo-journal">
            <summary>Journal de l’app ({ko(c.journalBytes ?? 0)})</summary>
            <pre>{c.journal}</pre>
          </details>
        </section>
      ) : null}

      {closed ? (
        <section className="bo-card">
          <p className="bo-muted">Cette plainte est close : elle ne prend plus de réponse.</p>
        </section>
      ) : (
        <section className="bo-card">
          <h2>Répondre</h2>
          <AnswerForm id={c.id} max={data.limits.message} />
          <h2>Clore</h2>
          <p className="bo-muted">Le client ne pourra plus répondre sur cette plainte ; le geste est enregistré.</p>
          <CloseForm id={c.id} number={c.number} />
        </section>
      )}
    </>
  );
}
