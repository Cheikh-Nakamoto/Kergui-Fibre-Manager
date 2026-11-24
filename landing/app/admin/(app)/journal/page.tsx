import Link from 'next/link';
import { hub, type ActionLog } from '../../../../lib/hub';
import { actionLabel, dateTime, targetLabel } from '../../../../lib/format';

export const metadata = { title: 'Journal' };

type Filters = { admin?: string; kind?: string; from?: string; to?: string; before?: string };

/** The query string of the filters, without the empty ones. */
function query(f: Record<string, string | undefined>): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) if (v) p.set(k, v);
  const s = p.toString();
  return s ? `?${s}` : '';
}

export default async function JournalPage({ searchParams }: { searchParams: Promise<Filters> }) {
  const { admin = '', kind = '', from = '', to = '', before = '' } = await searchParams;
  const filters = { admin, kind, from, to };
  const data = await hub<ActionLog>(`/api/v1/admin/audit${query({ ...filters, before, limit: '50' })}`);
  const filtered = Boolean(admin || kind || from || to);

  return (
    <>
      <div className="bo-head">
        <h1>Journal des actions</h1>
        <p className="bo-muted">
          Chaque geste sensible du back-office : qui, quand, quoi et pourquoi. Les plus récents d’abord.
        </p>
      </div>

      <section className="bo-card">
        <form className="bo-filters" role="search">
          <label>
            Administrateur
            <select name="admin" defaultValue={admin}>
              <option value="">Tous</option>
              {data.admins.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.email}
                </option>
              ))}
            </select>
          </label>
          <label>
            Action
            <select name="kind" defaultValue={kind}>
              <option value="">Toutes</option>
              {data.kinds.map((k) => (
                <option key={k} value={k}>
                  {actionLabel[k] ?? k}
                </option>
              ))}
            </select>
          </label>
          <label>
            Du
            <input type="date" name="from" defaultValue={from} />
          </label>
          <label>
            Au
            <input type="date" name="to" defaultValue={to} />
          </label>
          <div className="bo-row">
            <button className="bo-btn" type="submit">
              Filtrer
            </button>
            {filtered ? (
              <Link className="bo-btn ghost" href="/admin/journal">
                Effacer
              </Link>
            ) : null}
          </div>
        </form>
      </section>

      <div className="bo-table-wrap bo-card flush">
        <table className="bo-table">
          <thead>
            <tr>
              <th>Date</th>
              <th>Administrateur</th>
              <th>Action</th>
              <th>Cible</th>
              <th>Raison</th>
              <th>Résumé</th>
            </tr>
          </thead>
          <tbody>
            {data.entries.map((e) => (
              <tr key={e.id}>
                <td>{dateTime(e.at)}</td>
                <td>{e.admin.email || <span className="bo-muted">compte supprimé</span>}</td>
                <td>{actionLabel[e.kind] ?? e.kind}</td>
                <td>
                  {targetLabel[e.target.type] ?? (e.target.type || '—')}
                  {e.target.id ? (
                    <>
                      <br />
                      <small className="bo-mono" title={e.target.id}>
                        {e.target.id.slice(0, 8)}
                      </small>
                    </>
                  ) : null}
                  {e.tenantId ? (
                    <>
                      <br />
                      <Link href={`/admin/foyers/${e.tenantId}`}>Voir le foyer</Link>
                    </>
                  ) : null}
                </td>
                <td className="bo-wrap-text">{e.reason || <span className="bo-muted">—</span>}</td>
                <td className="bo-wrap-text">{e.summary || <span className="bo-muted">—</span>}</td>
              </tr>
            ))}
            {data.entries.length === 0 ? (
              <tr>
                <td colSpan={6} className="bo-muted">
                  {filtered ? 'Aucune action pour ces filtres.' : 'Aucune action enregistrée.'}
                </td>
              </tr>
            ) : null}
          </tbody>
        </table>
      </div>

      <div className="bo-row">
        {before ? (
          <Link className="bo-btn ghost" href={`/admin/journal${query(filters)}`}>
            Plus récent
          </Link>
        ) : null}
        {data.next ? (
          <Link className="bo-btn ghost" href={`/admin/journal${query({ ...filters, before: data.next })}`}>
            Plus ancien
          </Link>
        ) : null}
      </div>
    </>
  );
}
