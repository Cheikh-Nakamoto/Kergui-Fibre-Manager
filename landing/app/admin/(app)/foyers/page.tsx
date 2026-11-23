import Link from 'next/link';
import { hub, type Household } from '../../../../lib/hub';
import { ago, date, tierLabel } from '../../../../lib/format';

export const metadata = { title: 'Foyers' };

export default async function HouseholdsPage({ searchParams }: { searchParams: Promise<{ q?: string }> }) {
  const { q = '' } = await searchParams;
  const { households } = await hub<{ households: Household[] }>(
    `/api/v1/admin/households?limit=100${q ? `&q=${encodeURIComponent(q)}` : ''}`,
  );
  return (
    <>
      <div className="bo-head">
        <h1>Foyers</h1>
        <form className="bo-search" role="search">
          <input name="q" defaultValue={q} placeholder="Numéro, nom ou identifiant" aria-label="Rechercher un foyer" />
          <button className="bo-btn" type="submit">
            Rechercher
          </button>
        </form>
      </div>
      <p className="bo-muted">
        {households.length} foyer{households.length > 1 ? 's' : ''}
        {q ? ` pour « ${q} »` : ', les plus récents d’abord'}.
      </p>
      <div className="bo-table-wrap bo-card flush">
        <table className="bo-table">
          <thead>
            <tr>
              <th>Foyer</th>
              <th>Numéro</th>
              <th>Pass</th>
              <th>Membres</th>
              <th>Relais</th>
              <th>Appareils</th>
              <th>Dernier relevé</th>
              <th>Créé le</th>
            </tr>
          </thead>
          <tbody>
            {households.map((h) => (
              <tr key={h.tenantId}>
                <td>
                  <Link href={`/admin/foyers/${h.tenantId}`}>{h.name}</Link>
                </td>
                <td className="bo-mono">{h.phone || '—'}</td>
                <td>
                  <span className={`bo-pill ${h.tier}`}>{tierLabel[h.tier] ?? h.tier}</span>
                  {h.passUntil ? <small> jusqu’au {date(h.passUntil)}</small> : null}
                </td>
                <td>{h.members}</td>
                <td>
                  <span className={h.relays > 0 && h.relaysOnline === 0 ? 'bo-warn-text' : ''}>
                    {h.relaysOnline} / {h.relays}
                  </span>
                </td>
                <td>{h.devices}</td>
                <td>{ago(h.lastSnapshot)}</td>
                <td>{date(h.createdAt)}</td>
              </tr>
            ))}
            {households.length === 0 ? (
              <tr>
                <td colSpan={8} className="bo-muted">
                  Aucun foyer.
                </td>
              </tr>
            ) : null}
          </tbody>
        </table>
      </div>
    </>
  );
}
