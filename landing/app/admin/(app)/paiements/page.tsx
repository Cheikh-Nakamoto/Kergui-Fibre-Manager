import Link from 'next/link';
import { hub, type Revenue, type Stuck, type TierFigures, type WatchedPayment } from '../../../../lib/hub';
import { ago, dateTime, fcfa, providerLabel, statusLabel, tierLabel } from '../../../../lib/format';
import { RecheckForm } from './recheck';

export const metadata = { title: 'Paiements' };

type View = 'day' | 'month';

function Stat({ label, value, sub, tone }: { label: string; value: string | number; sub?: string; tone?: 'ok' | 'warn' | 'bad' }) {
  return (
    <div className={`bo-stat${tone ? ' ' + tone : ''}`}>
      <span>{label}</span>
      <strong>{value}</strong>
      {sub ? <small className="bo-muted">{sub}</small> : null}
    </div>
  );
}

// Dakar is UTC all year: a UTC day is the household's day.
function today(): string {
  return new Date().toISOString().slice(0, 10);
}

function periodLabel(p: string): string {
  const [y, m, d] = p.split('-').map(Number);
  const at = new Date(Date.UTC(y, (m ?? 1) - 1, d ?? 1));
  return d
    ? at.toLocaleDateString('fr-FR', { timeZone: 'UTC', weekday: 'short', day: '2-digit', month: 'short' })
    : at.toLocaleDateString('fr-FR', { timeZone: 'UTC', month: 'long', year: 'numeric' });
}

function tierAmount(list: TierFigures[], tier: string): string {
  const t = list.find((x) => x.tier === tier);
  return t ? `${fcfa(t.amountXof)} (${t.count})` : '—';
}

function count(n: number): string {
  return `${n} paiement${n > 1 ? 's' : ''}`;
}

function WatchTable({ rows, recheck, empty }: { rows: WatchedPayment[]; recheck: boolean; empty: string }) {
  return (
    <div className="bo-table-wrap">
      <table className="bo-table">
        <thead>
          <tr>
            <th>Créé</th>
            <th>Numéro</th>
            <th>Pass</th>
            <th>Montant</th>
            <th>Statut</th>
            <th>Référence</th>
            <th>Action</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((p) => (
            <tr key={p.id}>
              <td title={dateTime(p.createdAt)}>{ago(p.createdAt)}</td>
              <td className="bo-mono">
                <Link href={`/admin/support/${p.accountId}`}>{p.phone}</Link>
                {p.boxId ? <small className="bo-muted"> · box {p.boxId}</small> : null}
              </td>
              <td>
                {p.passCode}
                {p.tier ? <small className="bo-muted"> · {tierLabel[p.tier] ?? p.tier}</small> : null}
              </td>
              <td>{fcfa(p.amountXof)}</td>
              <td>
                <span className={`bo-pill ${p.status === 'pending' ? 'warn' : 'off'}`}>{statusLabel[p.status] ?? p.status}</span>
                <br />
                <small className="bo-muted">{providerLabel[p.provider] ?? p.provider}</small>
              </td>
              <td className="bo-mono bo-wrap-text">{p.reference}</td>
              <td>
                {p.recheckable && recheck ? (
                  <RecheckForm paymentId={p.id} reference={p.reference} phone={p.phone} />
                ) : (
                  <span className="bo-muted">—</span>
                )}
              </td>
            </tr>
          ))}
          {rows.length === 0 ? (
            <tr>
              <td colSpan={7} className="bo-muted">
                {empty}
              </td>
            </tr>
          ) : null}
        </tbody>
      </table>
    </div>
  );
}

export default async function PaymentsPage({
  searchParams,
}: {
  searchParams: Promise<{ vue?: string; exportError?: string }>;
}) {
  const { vue, exportError } = await searchParams;
  const view: View = vue === 'mois' ? 'month' : 'day';
  const day = today();
  const monthStart = `${day.slice(0, 8)}01`;

  // This month day by day (the default range): the month's and today's
  // figures, and the daily table. The monthly view adds the last 12 months.
  const [month, stuck] = await Promise.all([
    hub<Revenue>('/api/v1/admin/payments/revenue?by=day'),
    hub<Stuck>('/api/v1/admin/payments/stuck?olderThan=10m'),
  ]);
  const table = view === 'month' ? await hub<Revenue>('/api/v1/admin/payments/revenue?by=month') : month;
  const todays = month.periods.find((p) => p.period === day);
  const periods = [...table.periods].reverse();

  return (
    <>
      <div className="bo-head">
        <h1>Paiements</h1>
        <p className="bo-muted">
          Recettes encaissées (payées), hors gestes commerciaux. Dakar = UTC. Les passes et les gestes commerciaux sont dans{' '}
          <Link href="/admin/passes">Passes</Link>.
        </p>
      </div>

      <section className="bo-stats">
        <Stat label="Ce mois-ci" value={fcfa(month.totals.amountXof)} sub={count(month.totals.count)} />
        <Stat label="Aujourd’hui" value={fcfa(todays?.amountXof ?? 0)} sub={count(todays?.count ?? 0)} />
        <Stat
          label={`En attente depuis plus de ${stuck.olderThanMinutes} min`}
          value={stuck.pending.length}
          tone={stuck.pending.length > 0 ? 'warn' : 'ok'}
        />
        <Stat label={`Échoués (${stuck.failedDays} j)`} value={stuck.failed.length} tone={stuck.failed.length > 0 ? 'bad' : undefined} />
      </section>

      <section className="bo-card flush">
        <div className="bo-pad bo-row">
          <h2>{view === 'month' ? 'Par mois (12 derniers)' : 'Par jour (ce mois-ci)'}</h2>
          <Link className={`bo-btn small${view === 'day' ? '' : ' ghost'}`} href="/admin/paiements">
            Par jour
          </Link>
          <Link className={`bo-btn small${view === 'month' ? '' : ' ghost'}`} href="/admin/paiements?vue=mois">
            Par mois
          </Link>
        </div>
        <div className="bo-table-wrap">
          <table className="bo-table">
            <thead>
              <tr>
                <th>{view === 'month' ? 'Mois' : 'Jour'}</th>
                <th>Montant</th>
                <th>Paiements</th>
                <th>Basic</th>
                <th>Max</th>
              </tr>
            </thead>
            <tbody>
              {periods.map((p) => (
                <tr key={p.period}>
                  <td>{periodLabel(p.period)}</td>
                  <td>
                    <strong>{fcfa(p.amountXof)}</strong>
                  </td>
                  <td>{p.count}</td>
                  <td>{tierAmount(p.byTier, 'basic')}</td>
                  <td>{tierAmount(p.byTier, 'max')}</td>
                </tr>
              ))}
              {periods.length === 0 ? (
                <tr>
                  <td colSpan={5} className="bo-muted">
                    Aucun paiement encaissé sur cette période.
                  </td>
                </tr>
              ) : (
                <tr>
                  <td>
                    <strong>Total</strong>
                  </td>
                  <td>
                    <strong>{fcfa(table.totals.amountXof)}</strong>
                  </td>
                  <td>{table.totals.count}</td>
                  <td>{tierAmount(table.byTier, 'basic')}</td>
                  <td>{tierAmount(table.byTier, 'max')}</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      <section className="bo-card flush">
        <h2 className="bo-pad">Répartition par pass {view === 'month' ? '(12 derniers mois)' : '(ce mois-ci)'}</h2>
        <div className="bo-table-wrap">
          <table className="bo-table">
            <thead>
              <tr>
                <th>Pass</th>
                <th>Niveau</th>
                <th>Paiements</th>
                <th>Montant</th>
                <th>Part</th>
              </tr>
            </thead>
            <tbody>
              {table.byPass.map((p) => (
                <tr key={p.pass}>
                  <td className="bo-mono">{p.pass}</td>
                  <td>
                    <span className={`bo-pill ${p.tier}`}>{tierLabel[p.tier] ?? (p.tier || '?')}</span>
                  </td>
                  <td>{p.count}</td>
                  <td>{fcfa(p.amountXof)}</td>
                  <td>{table.totals.amountXof ? `${Math.round((100 * p.amountXof) / table.totals.amountXof)} %` : '—'}</td>
                </tr>
              ))}
              {table.byTier.map((t) => (
                <tr key={`tier-${t.tier}`}>
                  <td>
                    <strong>Total {tierLabel[t.tier] ?? (t.tier || '?')}</strong>
                  </td>
                  <td />
                  <td>{t.count}</td>
                  <td>
                    <strong>{fcfa(t.amountXof)}</strong>
                  </td>
                  <td>{table.totals.amountXof ? `${Math.round((100 * t.amountXof) / table.totals.amountXof)} %` : '—'}</td>
                </tr>
              ))}
              {table.byPass.length === 0 ? (
                <tr>
                  <td colSpan={5} className="bo-muted">
                    Rien à répartir.
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
      </section>

      <section className="bo-card flush">
        <h2 className="bo-pad">À surveiller : en attente depuis plus de {stuck.olderThanMinutes} min</h2>
        {!stuck.recheck ? (
          <p className="bo-banner warn">NabooPay n’est pas configuré sur ce hub : impossible de revérifier une commande.</p>
        ) : null}
        <WatchTable rows={stuck.pending} recheck={stuck.recheck} empty="Aucun paiement bloqué." />
      </section>

      <section className="bo-card flush">
        <h2 className="bo-pad">Échoués ({stuck.failedDays} derniers jours)</h2>
        <WatchTable rows={stuck.failed} recheck={stuck.recheck} empty="Aucun échec récent." />
      </section>

      <section className="bo-card">
        <h2>Export pour la comptabilité</h2>
        <p className="bo-muted">
          Tous les paiements créés sur la période, quel que soit leur statut, au format CSV (Excel). Numéros complets : ne
          le partagez pas hors de l’équipe.
        </p>
        {exportError ? (
          <p className="bo-error" role="alert">
            {exportError}
          </p>
        ) : null}
        <form className="bo-filters" action="/admin/paiements/export" method="get">
          <label>
            Du
            <input type="date" name="from" defaultValue={monthStart} required />
          </label>
          <label>
            Au
            <input type="date" name="to" defaultValue={day} required />
          </label>
          <div className="bo-row">
            <button className="bo-btn" type="submit">
              Télécharger le CSV
            </button>
          </div>
        </form>
      </section>
    </>
  );
}
