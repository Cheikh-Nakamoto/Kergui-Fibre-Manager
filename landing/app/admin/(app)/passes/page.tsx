import Link from 'next/link';
import { hub, type Expiring, type Passes, type Renewals, type Revenue } from '../../../../lib/hub';
import { date, dateTime, fcfa, statusLabel, tierLabel } from '../../../../lib/format';
import { GrantForm } from './grant';
import { ExpiringSection, RenewalsSection, type PassFilters } from './renewals';

export const metadata = { title: 'Passes et paiements' };

/** The query string for the hub, without the empty values. */
function query(f: Record<string, string>): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) if (v) p.set(k, v);
  const s = p.toString();
  return s ? `?${s}` : '';
}

export default async function PassesPage({ searchParams }: { searchParams: Promise<Partial<PassFilters>> }) {
  const params = await searchParams;
  // Only known values reach the hub: a hand-edited URL shows the default
  // view instead of the error page.
  const filters: PassFilters = {
    q: params.q ?? '',
    etat: params.etat === 'bientot' || params.etat === 'expires' ? params.etat : '',
    niveau: params.niveau === 'basic' || params.niveau === 'max' ? params.niveau : '',
    mois: /^\d{4}-(0[1-9]|1[0-2])$/.test(params.mois ?? '') ? (params.mois as string) : '',
  };
  const { q } = filters;
  const status = { bientot: 'soon', expires: 'expired' }[filters.etat] ?? '';
  const [data, expiring, renewals, revenue] = await Promise.all([
    hub<Passes>(`/api/v1/admin/passes${query({ limit: '100', q })}`),
    hub<Expiring>(`/api/v1/admin/passes/expiring${query({ status, tier: filters.niveau, q })}`),
    hub<Renewals>(`/api/v1/admin/passes/renewals${query({ month: filters.mois })}`),
    hub<Revenue>('/api/v1/admin/payments/revenue?by=month'),
  ]);
  return (
    <>
      <div className="bo-head">
        <h1>Passes et paiements</h1>
        <form className="bo-search" role="search">
          {filters.etat ? <input type="hidden" name="etat" value={filters.etat} /> : null}
          {filters.niveau ? <input type="hidden" name="niveau" value={filters.niveau} /> : null}
          {filters.mois ? <input type="hidden" name="mois" value={filters.mois} /> : null}
          <input name="q" defaultValue={q} placeholder="Numéro de téléphone" aria-label="Rechercher un numéro" />
          <button className="bo-btn" type="submit">
            Rechercher
          </button>
        </form>
      </div>

      <ExpiringSection data={expiring} filters={filters} />

      <RenewalsSection data={renewals} revenue={revenue} filters={filters} />

      <section className="bo-card flush">
        <h2 className="bo-pad">Comptes</h2>
        <div className="bo-table-wrap">
          <table className="bo-table">
            <thead>
              <tr>
                <th>Numéro</th>
                <th>Passes par box</th>
                <th>Essai Max</th>
                <th>Compte créé</th>
                <th>Geste commercial</th>
              </tr>
            </thead>
            <tbody>
              {data.accounts.map((a) => (
                <tr key={a.accountId}>
                  <td className="bo-mono">
                    {a.tenantId ? <Link href={`/admin/foyers/${a.tenantId}`}>{a.phone}</Link> : a.phone}
                  </td>
                  <td>
                    {a.subscriptions.length === 0 ? (
                      <span className="bo-muted">Aucune box</span>
                    ) : (
                      a.subscriptions.map((s) => (
                        <div key={s.boxId}>
                          <span className={`bo-pill ${s.active ? s.tier : 'off'}`}>{tierLabel[s.tier] ?? s.tier}</span>{' '}
                          <span className="bo-mono">{s.boxId}</span>
                          {s.tier !== 'free' ? <small> {s.active ? 'jusqu’au' : 'expiré le'} {date(s.expiresAt)}</small> : null}
                        </div>
                      ))
                    )}
                  </td>
                  <td>
                    {!a.trial ? (
                      <span className="bo-muted">non</span>
                    ) : a.trial.startedAt ? (
                      `démarré le ${date(a.trial.startedAt)}`
                    ) : (
                      <span className="bo-pill warn">demandé le {date(a.trial.requestedAt)}, en attente de box</span>
                    )}
                  </td>
                  <td>{date(a.createdAt)}</td>
                  <td>
                    <GrantForm
                      accountId={a.accountId}
                      boxes={a.subscriptions.map((s) => s.boxId)}
                      catalogue={data.catalogue}
                    />
                  </td>
                </tr>
              ))}
              {data.accounts.length === 0 ? (
                <tr>
                  <td colSpan={5} className="bo-muted">
                    Aucun compte.
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
      </section>

      <section className="bo-card flush">
        <h2 className="bo-pad">Derniers paiements</h2>
        <div className="bo-table-wrap">
          <table className="bo-table">
            <thead>
              <tr>
                <th>Date</th>
                <th>Numéro</th>
                <th>Pass</th>
                <th>Montant</th>
                <th>Moyen</th>
                <th>Statut</th>
                <th>Référence</th>
              </tr>
            </thead>
            <tbody>
              {data.payments.map((p) => (
                <tr key={p.id}>
                  <td>{dateTime(p.createdAt)}</td>
                  <td className="bo-mono">{p.phone}</td>
                  <td>{p.passCode}</td>
                  <td>{fcfa(p.amountXof)}</td>
                  <td>{p.provider === 'support' ? 'Geste commercial' : p.provider === 'naboopay' ? 'NabooPay' : p.provider}</td>
                  <td>
                    <span className={`bo-pill ${p.status === 'credited' ? 'ok' : p.status === 'pending' ? 'warn' : 'off'}`}>
                      {statusLabel[p.status] ?? p.status}
                    </span>
                  </td>
                  <td className="bo-mono bo-wrap-text">{p.reference}</td>
                </tr>
              ))}
              {data.payments.length === 0 ? (
                <tr>
                  <td colSpan={7} className="bo-muted">
                    Aucun paiement.
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
