import Link from 'next/link';
import type { Expiring, ExpiringPass, Renewals, Revenue, TierFigures } from '../../../../lib/hub';
import { date, daysFrom, fcfa, percent, providerLabel, tierLabel, whatsappLink } from '../../../../lib/format';

export type PassFilters = { q: string; etat: string; niveau: string; mois: string };

/** The page's query string with one filter changed, the empty ones left out. */
function href(f: PassFilters, change: Partial<PassFilters>): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries({ ...f, ...change })) if (v) p.set(k, v);
  const s = p.toString();
  return `/admin/passes${s ? `?${s}` : ''}`;
}

function Stat({ label, value, sub, tone }: { label: string; value: string | number; sub?: string; tone?: 'ok' | 'warn' | 'bad' }) {
  return (
    <div className={`bo-stat${tone ? ' ' + tone : ''}`}>
      <span>{label}</span>
      <strong>{value}</strong>
      {sub ? <small className="bo-muted">{sub}</small> : null}
    </div>
  );
}

/** The rows of the expiring list and of the lost passes: one pass per row. */
function PassTable({ rows, now, empty }: { rows: ExpiringPass[]; now: string; empty: string }) {
  return (
    <div className="bo-table-wrap">
      <table className="bo-table">
        <thead>
          <tr>
            <th>Fin</th>
            <th>Foyer</th>
            <th>Box</th>
            <th>Niveau</th>
            <th>Téléphone</th>
            <th>Dernier paiement</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((p) => (
            <tr key={`${p.accountId}-${p.boxId}`}>
              <td>
                {date(p.expiresAt)}
                <br />
                <small className={p.expired ? 'bo-warn-text' : undefined}>{daysFrom(p.expiresAt, now)}</small>
              </td>
              <td>
                {p.tenantId ? (
                  <Link href={`/admin/foyers/${p.tenantId}`}>{p.household || 'Foyer'}</Link>
                ) : (
                  <span className="bo-muted">Pas encore de foyer</span>
                )}
                <br />
                <small>
                  <Link href={`/admin/support/${p.accountId}`}>Fiche support</Link>
                </small>
              </td>
              <td className="bo-mono">{p.boxId}</td>
              <td>
                <span className={`bo-pill ${p.expired ? 'off' : p.tier}`}>{tierLabel[p.tier] ?? p.tier}</span>
                {p.trial ? <small> · essai</small> : null}
                {p.channel === 'play' ? <small> · Google Play (renouvellement automatique)</small> : null}
              </td>
              <td className="bo-mono">
                <a href={whatsappLink(p.phone)} target="_blank" rel="noopener noreferrer" title="Écrire sur WhatsApp">
                  {p.phone}
                </a>
              </td>
              <td>
                {p.lastPayment ? (
                  <>
                    {date(p.lastPayment.at)}
                    <br />
                    <small>
                      {p.lastPayment.passCode} · {fcfa(p.lastPayment.amountXof)} ·{' '}
                      {providerLabel[p.lastPayment.provider] ?? p.lastPayment.provider}
                    </small>
                  </>
                ) : (
                  <span className="bo-muted">Aucun</span>
                )}
              </td>
            </tr>
          ))}
          {rows.length === 0 ? (
            <tr>
              <td colSpan={6} className="bo-muted">
                {empty}
              </td>
            </tr>
          ) : null}
        </tbody>
      </table>
    </div>
  );
}

/** Passes ending within 7 days and ended less than 30 days ago, soonest end first. */
export function ExpiringSection({ data, filters }: { data: Expiring; filters: PassFilters }) {
  const { etat, niveau } = filters;
  return (
    <section className="bo-card flush">
      <div className="bo-pad">
        <h2>Passes qui expirent</h2>
        <p className="bo-muted">
          Les passes qui se terminent dans les {data.aheadDays} prochains jours et ceux terminés depuis moins de{' '}
          {data.behindDays} jours, la fin la plus proche en premier. Le numéro ouvre WhatsApp.
        </p>
        <form className="bo-filters" role="search">
          {filters.q ? <input type="hidden" name="q" value={filters.q} /> : null}
          {filters.mois ? <input type="hidden" name="mois" value={filters.mois} /> : null}
          <label>
            État
            <select name="etat" defaultValue={etat}>
              <option value="">Tous</option>
              <option value="bientot">Bientôt ({data.aheadDays} j)</option>
              <option value="expires">Expirés (moins de {data.behindDays} j)</option>
            </select>
          </label>
          <label>
            Niveau
            <select name="niveau" defaultValue={niveau}>
              <option value="">Tous</option>
              <option value="basic">Basic</option>
              <option value="max">Max</option>
            </select>
          </label>
          <div className="bo-row">
            <button type="submit" className="bo-btn small">
              Filtrer
            </button>
            {etat || niveau ? (
              <Link href={href(filters, { etat: '', niveau: '' })} className="bo-btn small ghost">
                Tout voir
              </Link>
            ) : null}
          </div>
        </form>
      </div>
      <PassTable rows={data.passes} now={data.now} empty="Aucun pass ne se termine dans cette fenêtre." />
    </section>
  );
}

function monthLabel(ym: string): string {
  const [y, m] = ym.split('-').map(Number);
  return new Date(Date.UTC(y, m - 1, 1)).toLocaleDateString('fr-FR', { timeZone: 'UTC', month: 'long', year: 'numeric' });
}

function tierAmount(list: TierFigures[], tier: string): string {
  const t = list.find((x) => x.tier === tier);
  return t ? fcfa(t.amountXof) : '—';
}

/** The month's renewal rate, the passes running, the trials, the revenue of 12 months and the lost passes. */
export function RenewalsSection({ data, revenue, filters }: { data: Renewals; revenue: Revenue; filters: PassFilters }) {
  const r = data.renewal;
  const label = monthLabel(data.month);
  const incomplete = data.trackedSince && new Date(data.from) < new Date(data.trackedSince);
  const months = [...revenue.periods].reverse();
  return (
    <>
      <section className="bo-card">
        <div className="bo-row">
          <h2>Renouvellement : {label}</h2>
        </div>
        <p className="bo-muted">
          Les passes dont la date de fin tombe dans le mois. Renouvelé : il a reçu des jours en plus, avant ou après sa fin
          (paiement, Google Play ou geste commercial). Perdu : terminé et pas renouvelé depuis. La fin de l’essai de 7 jours
          ne compte pas : elle est dans « Essais convertis ».
        </p>
        <form className="bo-filters">
          {filters.q ? <input type="hidden" name="q" value={filters.q} /> : null}
          {filters.etat ? <input type="hidden" name="etat" value={filters.etat} /> : null}
          {filters.niveau ? <input type="hidden" name="niveau" value={filters.niveau} /> : null}
          <label>
            Mois
            <input type="month" name="mois" defaultValue={data.month} />
          </label>
          <div className="bo-row">
            <button type="submit" className="bo-btn small">
              Voir
            </button>
            {filters.mois ? (
              <Link href={href(filters, { mois: '' })} className="bo-btn small ghost">
                Ce mois-ci
              </Link>
            ) : null}
          </div>
        </form>
        {incomplete ? (
          <p className="bo-banner warn">
            Le hub enregistre les renouvellements depuis le {date(data.trackedSince)} : avant, seuls ceux qui avaient reçu le
            rappel WhatsApp sont connus, et le taux de ce mois est sous-estimé.
          </p>
        ) : null}
      </section>

      <section className="bo-stats">
        <Stat
          label="Taux de renouvellement"
          value={percent(r.rate)}
          sub={`${r.renewed} renouvelé${r.renewed > 1 ? 's' : ''} sur ${r.due} arrivé${r.due > 1 ? 's' : ''} à échéance`}
          tone={r.rate === undefined ? undefined : r.rate >= 0.6 ? 'ok' : r.rate >= 0.3 ? 'warn' : 'bad'}
        />
        <Stat label="Perdus" value={r.lost} tone={r.lost > 0 ? 'bad' : undefined} sub="terminés, pas renouvelés" />
        <Stat label="À venir ce mois" value={r.upcoming} sub="fin pas encore atteinte" />
        <Stat label="Actifs Basic" value={data.active.basic} sub="en ce moment" />
        <Stat
          label="Actifs Max"
          value={data.active.max}
          sub={data.active.maxTrial ? `dont ${data.active.maxTrial} en essai` : 'en ce moment'}
        />
        <Stat
          label="Essais convertis"
          value={`${data.trials.converted} / ${data.trials.ended}`}
          sub={`essais finis ce mois · depuis le début : ${data.trialsAllTime.converted} / ${data.trialsAllTime.started}`}
        />
      </section>

      <section className="bo-card flush">
        <div className="bo-pad bo-row">
          <h2>Revenu par mois (12 derniers)</h2>
          <Link className="bo-btn small ghost" href="/admin/paiements?vue=mois">
            Détail des paiements
          </Link>
        </div>
        <div className="bo-table-wrap">
          <table className="bo-table">
            <thead>
              <tr>
                <th>Mois</th>
                <th>Montant</th>
                <th>Paiements</th>
                <th>Basic</th>
                <th>Max</th>
              </tr>
            </thead>
            <tbody>
              {months.map((p) => (
                <tr key={p.period}>
                  <td>{monthLabel(p.period)}</td>
                  <td>
                    <strong>{fcfa(p.amountXof)}</strong>
                  </td>
                  <td>{p.count}</td>
                  <td>{tierAmount(p.byTier, 'basic')}</td>
                  <td>{tierAmount(p.byTier, 'max')}</td>
                </tr>
              ))}
              {months.length === 0 ? (
                <tr>
                  <td colSpan={5} className="bo-muted">
                    Aucun paiement encaissé sur les 12 derniers mois.
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
      </section>

      <section className="bo-card flush">
        <h2 className="bo-pad">Foyers perdus : {label}</h2>
        <PassTable rows={data.lost} now={data.now} empty="Aucun foyer perdu ce mois-ci." />
      </section>
    </>
  );
}
