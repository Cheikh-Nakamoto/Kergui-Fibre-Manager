import Link from 'next/link';
import { notFound } from 'next/navigation';
import { hub, HubError, type HouseholdDetail } from '../../../../../lib/hub';
import { ago, date, dateTime, roleLabel, tierLabel } from '../../../../../lib/format';

export const metadata = { title: 'Foyer' };

export default async function HouseholdPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  let d: HouseholdDetail;
  try {
    d = await hub<HouseholdDetail>(`/api/v1/admin/households/${encodeURIComponent(id)}`);
  } catch (e) {
    if (e instanceof HubError && (e.status === 404 || e.status === 400)) notFound();
    throw e;
  }
  const h = d.household;
  return (
    <>
      <p>
        <Link href="/admin/foyers">← Foyers</Link>
      </p>
      <div className="ad-head">
        <h1>{h.name}</h1>
        <p className="ad-muted ad-mono">{h.tenantId}</p>
      </div>

      <section className="ad-stats">
        <div className="ad-stat">
          <span>Numéro</span>
          <strong className="ad-mono">{h.phone || '—'}</strong>
        </div>
        <div className="ad-stat">
          <span>Pass</span>
          <strong>{tierLabel[h.tier] ?? h.tier}</strong>
          {h.passUntil ? <small>jusqu’au {date(h.passUntil)}</small> : null}
        </div>
        <div className="ad-stat">
          <span>Appareils connus</span>
          <strong>{h.devices}</strong>
        </div>
        <div className="ad-stat">
          <span>Dernier relevé de la box</span>
          <strong>{ago(h.lastSnapshot)}</strong>
        </div>
      </section>

      {h.phone ? (
        <p>
          <Link href={`/admin/passes?q=${encodeURIComponent(h.phone)}`}>Voir ses passes et paiements →</Link>
        </p>
      ) : null}

      <section className="ad-card">
        <h2>Membres</h2>
        <div className="ad-table-wrap">
          <table className="ad-table">
            <thead>
              <tr>
                <th>Compte</th>
                <th>Rôle</th>
                <th>Statut</th>
                <th>WhatsApp</th>
                <th>Depuis</th>
              </tr>
            </thead>
            <tbody>
              {d.members.map((m) => (
                <tr key={m.userId}>
                  <td className="ad-wrap-text">
                    {m.email.endsWith('@phone.kergui.invalid') ? (
                      <span title={m.email}>Compte par téléphone</span>
                    ) : (
                      m.email
                    )}
                  </td>
                  <td>{roleLabel[m.role] ?? m.role}</td>
                  <td>{m.status === 'ACTIVE' ? 'Actif' : m.status === 'PENDING' ? 'Invité' : m.status}</td>
                  <td className="ad-mono">{m.whatsapp || '—'}</td>
                  <td>{date(m.joinedAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className="ad-card">
        <h2>Relais</h2>
        {d.relays.length === 0 ? (
          <p className="ad-muted">Aucun relais : ce foyer ne gère sa box que depuis la maison.</p>
        ) : (
          <div className="ad-table-wrap">
            <table className="ad-table">
              <thead>
                <tr>
                  <th>Nom</th>
                  <th>Type</th>
                  <th>État</th>
                  <th>Vu</th>
                  <th>Version</th>
                </tr>
              </thead>
              <tbody>
                {d.relays.map((r) => (
                  <tr key={r.id}>
                    <td>{r.name}</td>
                    <td>{r.kind === 'pc' ? 'PC' : r.kind === 'phone' ? 'Téléphone' : r.kind}</td>
                    <td>
                      {r.revoked ? (
                        <span className="ad-pill off">Révoqué</span>
                      ) : r.online ? (
                        <span className="ad-pill ok">En ligne</span>
                      ) : (
                        <span className="ad-pill warn">Hors ligne</span>
                      )}
                      {!r.revoked && !r.eligible && r.reason ? <small> {r.reason}</small> : null}
                    </td>
                    <td title={dateTime(r.seenAt)}>{ago(r.seenAt)}</td>
                    <td className="ad-mono">{r.version || '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="ad-card">
        <h2>Dernières alertes</h2>
        {d.alerts.length === 0 ? (
          <p className="ad-muted">Aucune alerte.</p>
        ) : (
          <ul className="ad-list">
            {d.alerts.map((a, i) => (
              <li key={i}>
                <strong>{dateTime(a.at)}</strong> ·{' '}
                {a.devices.map((x) => x.hostname || x.mac).join(', ')}
              </li>
            ))}
          </ul>
        )}
      </section>
    </>
  );
}
