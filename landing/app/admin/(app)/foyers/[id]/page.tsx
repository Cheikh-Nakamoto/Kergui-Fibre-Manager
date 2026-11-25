import Link from 'next/link';
import { notFound } from 'next/navigation';
import { hub, HubError, type HouseholdDetail } from '../../../../../lib/hub';
import { ago, date, dateTime, passEnd, roleLabel, tierLabel } from '../../../../../lib/format';
import { LifetimeForm, RenewForm, RevokeLifetimeForm } from './passes';

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
  const passes = d.passes ?? [];
  // A lifetime pass has no end to renew: only a revocation applies to it.
  const renewable = passes.filter((p) => !p.lifetime).map((p) => p.boxId);
  const lifetime = passes.filter((p) => p.lifetime);
  return (
    <>
      <p>
        <Link href="/admin/foyers">← Foyers</Link>
      </p>
      <div className="bo-head">
        <h1>{h.name}</h1>
        <p className="bo-muted bo-mono">{h.tenantId}</p>
      </div>

      <section className="bo-stats">
        <div className="bo-stat">
          <span>Numéro</span>
          <strong className="bo-mono">{h.phone || '—'}</strong>
        </div>
        <div className="bo-stat">
          <span>Pass</span>
          <strong>{tierLabel[h.tier] ?? h.tier}</strong>
          {h.passUntil ? <small>{passEnd({ expiresAt: h.passUntil, lifetime: h.lifetime })}</small> : null}
        </div>
        <div className="bo-stat">
          <span>Appareils connus</span>
          <strong>{h.devices}</strong>
        </div>
        <div className="bo-stat">
          <span>Dernier relevé de la box</span>
          <strong>{ago(h.lastSnapshot)}</strong>
        </div>
      </section>

      {h.phone ? (
        <p className="bo-row">
          <Link href={`/admin/support?phone=${encodeURIComponent(h.phone)}`}>Fiche client (support) →</Link>
          <Link href={`/admin/passes?q=${encodeURIComponent(h.phone)}`}>Voir ses passes et paiements →</Link>
        </p>
      ) : null}

      {d.accountId ? (
        <>
          <section className="bo-card">
            <h2>Passes des boxes</h2>
            {passes.length === 0 ? (
              <p className="bo-muted">Aucune box liée à ce numéro pour l’instant.</p>
            ) : (
              <div className="bo-table-wrap">
                <table className="bo-table">
                  <thead>
                    <tr>
                      <th>Box</th>
                      <th>Niveau</th>
                      <th>Fin</th>
                      <th>Paiement</th>
                    </tr>
                  </thead>
                  <tbody>
                    {passes.map((p) => (
                      <tr key={p.boxId}>
                        <td className="bo-mono">{p.boxId}</td>
                        <td>
                          <span className={`bo-pill ${p.active ? p.tier : 'off'}`}>{tierLabel[p.tier] ?? p.tier}</span>
                        </td>
                        <td>{p.tier === 'free' ? '—' : passEnd(p)}</td>
                        <td>{p.lifetime ? 'Pass à vie' : p.channel === 'play' ? 'Google Play' : 'Prépayé'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          <section className="bo-card">
            <h2>Renouveler ou prolonger un pass</h2>
            <p className="bo-muted">
              Pour un paiement reçu hors de l’application (Wave, espèces) ou un geste : les jours s’ajoutent à la fin du
              pass en cours, ou partent d’aujourd’hui s’il est terminé. Choisir Max sur un Basic en cours convertit le
              temps Basic restant, comme un achat. Le montant reçu compte dans les revenus ; sans montant, c’est un pass
              offert (0 F), comme le geste commercial.
            </p>
            <RenewForm tenantId={h.tenantId} boxes={renewable} maxDays={d.maxDays ?? 365} />
          </section>

          <section className="bo-card">
            <h2>Pass à vie</h2>
            <p className="bo-muted">
              Un pass sans date de fin pour un ami, un partenaire ou un testeur. Aucun rappel d’échéance ne part et il
              n’apparaît pas dans les passes qui expirent. Le pass en cours est mis de côté : il revient si le pass à
              vie est retiré.
            </p>
            {lifetime.map((p) => (
              <div key={p.boxId} className="bo-card">
                <p>
                  <strong>
                    Box <span className="bo-mono">{p.boxId}</span> : {tierLabel[p.tier] ?? p.tier} à vie
                  </strong>
                </p>
                <RevokeLifetimeForm tenantId={h.tenantId} boxId={p.boxId} tier={tierLabel[p.tier] ?? p.tier} />
              </div>
            ))}
            <LifetimeForm tenantId={h.tenantId} boxes={renewable} />
          </section>
        </>
      ) : (
        <p className="bo-muted">Ce foyer s’est inscrit par e-mail, sans numéro : il n’a pas de pass à gérer.</p>
      )}

      <section className="bo-card">
        <h2>Membres</h2>
        <div className="bo-table-wrap">
          <table className="bo-table">
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
                  <td className="bo-wrap-text">
                    {m.email.endsWith('@phone.kergui.invalid') ? (
                      <span title={m.email}>Compte par téléphone</span>
                    ) : (
                      m.email
                    )}
                  </td>
                  <td>{roleLabel[m.role] ?? m.role}</td>
                  <td>{m.status === 'ACTIVE' ? 'Actif' : m.status === 'PENDING' ? 'Invité' : m.status}</td>
                  <td className="bo-mono">{m.whatsapp || '—'}</td>
                  <td>{date(m.joinedAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className="bo-card">
        <h2>Relais</h2>
        {d.relays.length === 0 ? (
          <p className="bo-muted">Aucun relais : ce foyer ne gère sa box que depuis la maison.</p>
        ) : (
          <div className="bo-table-wrap">
            <table className="bo-table">
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
                        <span className="bo-pill off">Révoqué</span>
                      ) : r.online ? (
                        <span className="bo-pill ok">En ligne</span>
                      ) : (
                        <span className="bo-pill warn">Hors ligne</span>
                      )}
                      {!r.revoked && !r.eligible && r.reason ? <small> {r.reason}</small> : null}
                    </td>
                    <td title={dateTime(r.seenAt)}>{ago(r.seenAt)}</td>
                    <td className="bo-mono">{r.version || '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="bo-card">
        <h2>Dernières alertes</h2>
        {d.alerts.length === 0 ? (
          <p className="bo-muted">Aucune alerte.</p>
        ) : (
          <ul className="bo-list">
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
