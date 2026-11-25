import Link from 'next/link';
import type { Metadata } from 'next';
import { licensing, passName, type CustomerPayment, type Subscription, type Trial } from '../../../lib/account';
import { date, dateTime, fcfa, statusLabel, tierLabel } from '../../../lib/format';
import { site } from '../../../lib/site';
import { TrialButton } from './trial';

export const metadata: Metadata = { title: 'Mes passes' };

function buyHref(box: string, tier: string): string {
  return `/compte/acheter?${new URLSearchParams({ box, tier })}`;
}

function daysLeft(iso: string): number {
  return Math.max(0, Math.ceil((new Date(iso).getTime() - Date.now()) / 86_400_000));
}

function BoxCard({ s }: { s: Subscription }) {
  const tier = s.active ? s.effectiveTier : 'free';
  const play = s.channel === 'play';
  return (
    <div className="cl-box">
      <div className="cl-box-info">
        <strong className="cl-mono">Box {s.boxId}</strong>
        <span className="cl-muted">
          {s.active && s.lifetime
            ? `${tierLabel[tier] ?? tier} à vie`
            : s.active
              ? `${tierLabel[tier] ?? tier} jusqu’au ${date(s.expiresAt)} (${daysLeft(s.expiresAt)} j)`
              : s.tier !== 'free'
                ? `Pass ${tierLabel[s.tier] ?? s.tier} terminé le ${date(s.expiresAt)}`
                : 'Aucun pass'}
          {play ? ' · géré dans le Play Store' : ''}
        </span>
      </div>
      <span className={`cl-pill ${tier}`}>{tierLabel[tier] ?? tier}</span>
      {site.buyEnabled && !play && !(s.active && s.lifetime) ? (
        <div className="cl-row">
          {tier === 'basic' ? (
            <>
              <Link className="cl-btn small" href={buyHref(s.boxId, 'max')}>
                Passer à Max
              </Link>
              <Link className="cl-btn ghost small" href={buyHref(s.boxId, 'basic')}>
                Renouveler
              </Link>
            </>
          ) : tier === 'max' ? (
            <Link className="cl-btn ghost small" href={buyHref(s.boxId, 'max')}>
              Renouveler
            </Link>
          ) : (
            <Link className="cl-btn small" href={buyHref(s.boxId, 'max')}>
              Prendre un pass
            </Link>
          )}
        </div>
      ) : null}
    </div>
  );
}

function TrialCard({ t }: { t: Trial }) {
  switch (t.state) {
    case 'available':
      return (
        <section className="cl-card">
          <h2>Essai Max gratuit</h2>
          <p>
            Essayez Max pendant {t.days} jours, sans payer. Une fois par numéro et par box : les jours démarrent à la
            première connexion de l’application Kergui à votre box.
          </p>
          <TrialButton />
        </section>
      );
    case 'pending':
      return (
        <section className="cl-card">
          <h2>Essai Max gratuit</h2>
          <p>
            <span className="cl-pill warn">En attente</span> Demandé le {date(t.requestedAt)}. Ouvrez l’application Kergui
            chez vous, connectée à votre box : les {t.days} jours démarrent à ce moment-là.
          </p>
        </section>
      );
    case 'running':
      return (
        <section className="cl-card">
          <h2>Essai Max gratuit</h2>
          <p>
            <span className="cl-pill max">En cours</span> Sur la box <span className="cl-mono">{t.boxId}</span>, jusqu’au{' '}
            {dateTime(t.endsAt)}.
          </p>
        </section>
      );
    default:
      return null;
  }
}

export default async function ComptePage() {
  const [{ subscriptions }, { payments }, trial] = await Promise.all([
    licensing<{ subscriptions: Subscription[] }>('subscriptions'),
    licensing<{ payments: CustomerPayment[] }>('payments'),
    licensing<Trial>('trial'),
  ]);
  const waiting = payments.filter((p) => p.waiting);

  return (
    <>
      <h1>Mes passes</h1>

      <section className="cl-card">
        <div className="cl-row" style={{ justifyContent: 'space-between', marginBottom: 12 }}>
          <h2 style={{ margin: 0 }}>Mes box</h2>
          {site.buyEnabled ? (
            <Link className="cl-btn small" href="/compte/acheter">
              Prendre un pass
            </Link>
          ) : null}
        </div>
        {subscriptions.length === 0 && waiting.length === 0 ? (
          <p className="cl-muted">
            Aucun pass pour l’instant : Kergui reste gratuit. Un pass s’applique à la box sur laquelle l’application Kergui
            est connectée avec ce numéro.
          </p>
        ) : (
          <div className="cl-boxes">
            {subscriptions.map((s) => (
              <BoxCard key={s.boxId} s={s} />
            ))}
            {waiting.map((p) => (
              <div className="cl-box" key={p.id}>
                <div className="cl-box-info">
                  <strong>{passName(p.pass)}</strong>
                  <span className="cl-muted">
                    Payé le {date(p.creditedAt ?? p.createdAt)}. Il s’appliquera à la box sur laquelle l’application
                    Kergui se connectera avec ce numéro.
                  </span>
                </div>
                <span className="cl-pill warn">En attente d’une box</span>
              </div>
            ))}
          </div>
        )}
        {!site.buyEnabled ? <p className="cl-muted">Les passes payants arrivent bientôt.</p> : null}
      </section>

      <TrialCard t={trial} />

      <section className="cl-card">
        <h2>Historique des paiements</h2>
        {payments.length === 0 ? (
          <p className="cl-muted">Aucun paiement pour l’instant.</p>
        ) : (
          <div className="cl-table-wrap">
            <table className="cl-table">
              <thead>
                <tr>
                  <th>Date</th>
                  <th>Pass</th>
                  <th>Box</th>
                  <th className="num">Montant</th>
                  <th>Statut</th>
                </tr>
              </thead>
              <tbody>
                {payments.map((p) => (
                  <tr key={p.id}>
                    <td>{dateTime(p.createdAt)}</td>
                    <td>{passName(p.pass)}</td>
                    <td className="cl-mono">{p.boxId ?? '—'}</td>
                    <td className="num">{fcfa(p.amountXof)}</td>
                    <td>
                      <span className={`cl-pill ${p.status === 'credited' ? 'ok' : p.status === 'pending' ? 'warn' : ''}`}>
                        {statusLabel[p.status] ?? p.status}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="cl-card">
        <h2>Un problème ?</h2>
        <p>
          Un paiement non crédité, une box qui ne répond pas : écrivez à l’équipe Kergui et suivez sa réponse ici.
        </p>
        <Link className="cl-btn ghost small" href="/compte/probleme">
          Signaler un problème
        </Link>
      </section>
    </>
  );
}
