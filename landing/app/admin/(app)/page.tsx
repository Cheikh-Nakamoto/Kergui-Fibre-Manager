import Link from 'next/link';
import { hub, type Health } from '../../../lib/hub';
import { ago, dateTime } from '../../../lib/format';

export const metadata = { title: 'Santé du hub' };

function Stat({ label, value, tone }: { label: string; value: string | number; tone?: 'ok' | 'warn' | 'bad' }) {
  return (
    <div className={`bo-stat${tone ? ' ' + tone : ''}`}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

export default async function HealthPage() {
  const h = await hub<Health>('/api/v1/admin/health');
  const c = h.counts;
  const wa = h.whatsapp;
  return (
    <>
      <div className="bo-head">
        <h1>Santé du hub</h1>
        <p className="bo-muted">
          Version {h.version} · base de données {h.database === 'ok' ? 'OK' : 'injoignable'} · mis à jour {dateTime(h.now)}
        </p>
      </div>

      <section className="bo-stats">
        <Stat label="Foyers" value={c.households} />
        <Stat
          label="Relais en ligne"
          value={`${c.relaysOnline} / ${c.relays}`}
          tone={c.relays > 0 && c.relaysOnline === 0 ? 'warn' : undefined}
        />
        <Stat label="Passes actifs" value={c.activePasses} />
        <Stat label="Paiements en attente" value={c.pendingPayments} tone={c.pendingPayments > 0 ? 'warn' : undefined} />
        <Stat label="Alertes (24 h)" value={c.alerts24h} />
        <Stat label="Envois (24 h)" value={c.deliveries24h} />
        <Stat label="Envois en échec (24 h)" value={c.failed24h} tone={c.failed24h > 0 ? 'bad' : 'ok'} />
        <Stat
          label="WhatsApp"
          value={!wa ? 'Console' : wa.connected ? 'Connecté' : wa.linked ? 'Déconnecté' : 'Non lié'}
          tone={!wa ? undefined : wa.connected ? 'ok' : 'bad'}
        />
      </section>

      {wa && !wa.connected ? (
        <p className="bo-banner warn">
          WhatsApp n’envoie rien en ce moment : les foyers ne sont alertés que par notification.{' '}
          <Link href="/admin/whatsapp">Gérer la session</Link>
        </p>
      ) : null}

      <section className="bo-card">
        <h2>Derniers envois en échec</h2>
        {h.recentFailures.length === 0 ? (
          <p className="bo-muted">Aucun échec récent.</p>
        ) : (
          <div className="bo-table-wrap">
            <table className="bo-table">
              <thead>
                <tr>
                  <th>Quand</th>
                  <th>Canal</th>
                  <th>Fournisseur</th>
                  <th>Erreur</th>
                  <th>Foyer</th>
                </tr>
              </thead>
              <tbody>
                {h.recentFailures.map((f, i) => (
                  <tr key={i}>
                    <td title={dateTime(f.at)}>{ago(f.at)}</td>
                    <td>{f.channel}</td>
                    <td>{f.provider}</td>
                    <td className="bo-wrap-text">{f.error}</td>
                    <td>
                      <Link href={`/admin/foyers/${f.tenantId}`}>Voir</Link>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  );
}
