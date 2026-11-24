import Link from 'next/link';
import { hub, type ComplaintAdmins, type Complaints } from '../../../../lib/hub';
import {
  ago,
  complaintCategoryLabel,
  complaintSourceLabel,
  complaintStatusLabel,
  complaintStatusTone,
  dateTime,
} from '../../../../lib/format';
import { AdminsForm } from './forms';

export const metadata = { title: 'Plaintes' };

type Filters = { status?: string; category?: string; source?: string; age?: string; phone?: string };

/** The query string of the filters, without the empty ones. */
function query(f: Record<string, string | undefined>): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) if (v) p.set(k, v);
  const s = p.toString();
  return s ? `?${s}` : '';
}

/** Every complaint, newest first: from the app, the site and WhatsApp. */
export default async function ComplaintsPage({ searchParams }: { searchParams: Promise<Filters> }) {
  const { status = '', category = '', source = '', age = '', phone = '' } = await searchParams;
  const [data, admins] = await Promise.all([
    hub<Complaints>(
      `/api/v1/admin/complaints${query({ status, category, source, olderThanHours: age, phone: phone.trim(), limit: '200' })}`,
    ),
    hub<ComplaintAdmins>('/api/v1/admin/settings/complaint-admins'),
  ]);
  const c = data.counts;

  return (
    <>
      <div className="bo-head">
        <h1>Plaintes</h1>
        <p className="bo-muted">
          Ce que les foyers signalent depuis l’app, l’espace client ou WhatsApp. Une réponse part en notification sur les
          téléphones du client, dans l’écran Notifications de l’app, et par WhatsApp s’il l’a activé.
        </p>
      </div>

      <section className="bo-stats">
        <div className={`bo-stat${c.open ? ' warn' : ''}`}>
          <span>Ouvertes</span>
          <strong>{c.open}</strong>
          {c.openOverDay ? <small>dont {c.openOverDay} depuis plus d’un jour</small> : null}
        </div>
        <div className="bo-stat">
          <span>Répondues</span>
          <strong>{c.answered}</strong>
        </div>
        <div className="bo-stat">
          <span>Closes</span>
          <strong>{c.closed}</strong>
        </div>
      </section>

      <section className="bo-card">
        <form className="bo-filters" role="search">
          <label>
            Statut
            <select name="status" defaultValue={status}>
              <option value="">Tous</option>
              {Object.entries(complaintStatusLabel).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </select>
          </label>
          <label>
            Catégorie
            <select name="category" defaultValue={category}>
              <option value="">Toutes</option>
              {Object.entries(complaintCategoryLabel).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </select>
          </label>
          <label>
            Source
            <select name="source" defaultValue={source}>
              <option value="">Toutes</option>
              {['app', 'web', 'whatsapp'].map((k) => (
                <option key={k} value={k}>
                  {complaintSourceLabel[k]}
                </option>
              ))}
            </select>
          </label>
          <label>
            Ancienneté
            <select name="age" defaultValue={age}>
              <option value="">Toutes</option>
              <option value="24">Plus d’un jour</option>
              <option value="72">Plus de 3 jours</option>
              <option value="168">Plus d’une semaine</option>
            </select>
          </label>
          <label>
            Numéro
            <input name="phone" type="tel" defaultValue={phone} placeholder="77 000 00 00" />
          </label>
          <div className="bo-row">
            <button type="submit" className="bo-btn small">
              Filtrer
            </button>
            {status || category || source || age || phone ? (
              <Link href="/admin/plaintes" className="bo-btn small ghost">
                Tout voir
              </Link>
            ) : null}
          </div>
        </form>
      </section>

      <section className="bo-card flush">
        <h2 className="bo-pad">
          {data.complaints.length} plainte{data.complaints.length > 1 ? 's' : ''}
        </h2>
        {data.complaints.length === 0 ? (
          <p className="bo-pad bo-muted">Aucune plainte pour ces filtres.</p>
        ) : (
          <div className="bo-table-wrap">
            <table className="bo-table">
              <thead>
                <tr>
                  <th>N°</th>
                  <th>Reçue</th>
                  <th>Client</th>
                  <th>Catégorie</th>
                  <th>Message</th>
                  <th>Statut</th>
                </tr>
              </thead>
              <tbody>
                {data.complaints.map((p) => (
                  <tr key={p.id}>
                    <td>
                      <Link href={`/admin/plaintes/${p.number}`}>n° {p.number}</Link>
                    </td>
                    <td title={dateTime(p.createdAt)}>
                      {ago(p.createdAt)}
                      <br />
                      <small>{complaintSourceLabel[p.source] ?? p.source}</small>
                    </td>
                    <td className="bo-mono">
                      {p.accountId ? <Link href={`/admin/support/${p.accountId}`}>{p.phone}</Link> : p.phone}
                      {p.household ? <small className="bo-muted"> · {p.household.name}</small> : null}
                    </td>
                    <td>{complaintCategoryLabel[p.category] ?? p.category}</td>
                    <td className="bo-wrap-text">
                      <Link href={`/admin/plaintes/${p.number}`}>{p.preview || '—'}</Link>
                      <br />
                      <small>
                        {p.messageCount ?? 0} message{(p.messageCount ?? 0) > 1 ? 's' : ''}
                        {p.lastFrom === 'customer' && p.status !== 'closed' ? ' · attend une réponse' : ''}
                        {p.hasJournal ? ' · journal joint' : ''}
                      </small>
                    </td>
                    <td>
                      <span className={`bo-pill ${complaintStatusTone[p.status] ?? ''}`}>
                        {complaintStatusLabel[p.status] ?? p.status}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="bo-card">
        <h2>Admins sur WhatsApp</h2>
        <p className="bo-muted">
          Les numéros des admins des plaintes : le résumé WhatsApp des nouvelles plaintes et les commandes (LISTE, VOIR, R,
          OK) leur sont réservés.
        </p>
        <AdminsForm numbers={admins.numbers} max={admins.max} />
      </section>
    </>
  );
}
