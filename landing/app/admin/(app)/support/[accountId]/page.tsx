import Link from 'next/link';
import { notFound } from 'next/navigation';
import { hub, HubError, type Customer, type History } from '../../../../../lib/hub';
import {
  ago,
  date,
  dateTime,
  fcfa,
  historyKindLabel,
  roleLabel,
  statusLabel,
  tierLabel,
  trialStateLabel,
} from '../../../../../lib/format';
import { ExtendForm, RevokeForm, TransferForm } from './forms';

export const metadata = { title: 'Client' };

/** Everything support needs while a customer is on the phone, and the three gestures. */
export default async function CustomerPage({
  params,
  searchParams,
}: {
  params: Promise<{ accountId: string }>;
  searchParams: Promise<{ before?: string }>;
}) {
  const { accountId } = await params;
  const { before = '' } = await searchParams;
  let c: Customer;
  try {
    c = await hub<Customer>(`/api/v1/admin/support/accounts/${encodeURIComponent(accountId)}`);
  } catch (e) {
    if (e instanceof HubError && (e.status === 404 || e.status === 400)) notFound();
    throw e;
  }
  const h = c.household;
  const history = h
    ? await hub<History>(
        `/api/v1/admin/households/${h.tenantId}/history?limit=50${before ? `&before=${encodeURIComponent(before)}` : ''}`,
      )
    : null;
  const paid = c.subscriptions.filter((s) => s.tier !== 'free');
  const movable = c.subscriptions.filter((s) => s.active).map((s) => s.boxId);
  const self = `/admin/support/${c.account.id}`;

  return (
    <>
      <p>
        <Link href="/admin/support">← Support</Link>
      </p>
      <div className="bo-head">
        <h1 className="bo-mono">{c.account.phone}</h1>
        <p className="bo-muted">
          Compte créé le {date(c.account.createdAt)}
          {h ? (
            <>
              {' · '}
              <Link href={`/admin/foyers/${h.tenantId}`}>{h.name}</Link>
            </>
          ) : (
            ' · pas encore de foyer (jamais connecté dans l’application)'
          )}
        </p>
      </div>

      <section className="bo-stats">
        <div className="bo-stat">
          <span>Pass</span>
          <strong>{h ? (tierLabel[h.tier] ?? h.tier) : '—'}</strong>
          {h?.passUntil ? <small>jusqu’au {date(h.passUntil)}</small> : null}
        </div>
        <div className="bo-stat">
          <span>Essai Max</span>
          <strong>{trialStateLabel[c.trial.state] ?? c.trial.state}</strong>
          {c.trial.endsAt ? <small>fin le {date(c.trial.endsAt)}</small> : null}
        </div>
        <div className="bo-stat">
          <span>Téléphones connectés</span>
          <strong>{c.sessions.active}</strong>
        </div>
        <div className="bo-stat">
          <span>Transferts du client</span>
          <strong>
            {c.transfers.used} / {c.transfers.max}
          </strong>
          <small>sur {c.transfers.windowDays} jours</small>
        </div>
        {h ? (
          <div className="bo-stat">
            <span>Relais en ligne</span>
            <strong>
              {h.relaysOnline} / {h.relays}
            </strong>
            <small>dernier relevé {ago(h.lastSnapshot)}</small>
          </div>
        ) : null}
      </section>

      <section className="bo-card">
        <h2>Boxes et passes</h2>
        {c.subscriptions.length === 0 ? (
          <p className="bo-muted">Aucune box liée à ce numéro.</p>
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
                {c.subscriptions.map((s) => (
                  <tr key={s.boxId}>
                    <td className="bo-mono">{s.boxId}</td>
                    <td>
                      <span className={`bo-pill ${s.active ? s.tier : 'off'}`}>{tierLabel[s.tier] ?? s.tier}</span>
                    </td>
                    <td>{s.tier === 'free' ? '—' : `${s.active ? 'jusqu’au' : 'expiré le'} ${date(s.expiresAt)}`}</td>
                    <td>{s.channel === 'play' ? 'Google Play' : 'Prépayé'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {c.waitingPasses.length > 0 ? (
          <>
            <h3>Passes en attente d’une box</h3>
            <ul className="bo-list">
              {c.waitingPasses.map((p) => (
                <li key={p.paymentId}>
                  <strong>{p.passCode}</strong> · {fcfa(p.amountXof)} · payé le {date(p.creditedAt)} — démarre à la
                  prochaine connexion de l’application sur une box.
                </li>
              ))}
            </ul>
          </>
        ) : null}
        {c.trial.state === 'pending' ? (
          <p className="bo-muted">Essai Max demandé le {date(c.trial.requestedAt)}, en attente de box.</p>
        ) : c.trial.boxId ? (
          <p className="bo-muted">
            Essai Max sur la box <span className="bo-mono">{c.trial.boxId}</span>, démarré le {date(c.trial.startedAt)}.
          </p>
        ) : null}
      </section>

      <section className="bo-card">
        <h2>Prolonger un pass</h2>
        {paid.length === 0 ? (
          <p className="bo-muted">
            Aucun pass payé à prolonger. Pour offrir un pass :{' '}
            <Link href={`/admin/passes?q=${encodeURIComponent(c.account.phone)}`}>Passes</Link>.
          </p>
        ) : (
          <ExtendForm accountId={c.account.id} boxes={paid.map((s) => s.boxId)} maxDays={c.maxExtendDays} />
        )}
      </section>

      <section className="bo-card">
        <h2>Déplacer un pass vers une autre box</h2>
        <p className="bo-muted">
          Box remplacée ou cassée : passe outre la limite du client ({c.transfers.max} transferts par{' '}
          {c.transfers.windowDays} jours) et ne la consomme pas.
        </p>
        {movable.length === 0 ? (
          <p className="bo-muted">Aucun pass en cours à déplacer.</p>
        ) : (
          <TransferForm accountId={c.account.id} boxes={movable} />
        )}
      </section>

      <section className="bo-card">
        <h2>Sessions</h2>
        {c.sessions.devices.length === 0 ? (
          <p className="bo-muted">Aucun téléphone connecté.</p>
        ) : (
          <ul className="bo-list">
            {c.sessions.devices.map((d, i) => (
              <li key={i}>
                <strong>{d.device || 'Téléphone'}</strong> · connecté le {dateTime(d.createdAt)}
              </li>
            ))}
          </ul>
        )}
        <p className="bo-muted">
          Téléphone perdu ou volé : déconnecte l’application et le site de ce numéro. Aucun code n’est envoyé ; le client
          en demandera un lui-même.
        </p>
        <RevokeForm accountId={c.account.id} phone={c.account.phone} active={c.sessions.active} />
      </section>

      {h ? (
        <section className="bo-card">
          <h2>Membres du foyer</h2>
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
                {c.members.map((m) => (
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
      ) : null}

      <section className="bo-card flush">
        <h2 className="bo-pad">Paiements</h2>
        <div className="bo-table-wrap">
          <table className="bo-table">
            <thead>
              <tr>
                <th>Date</th>
                <th>Pass</th>
                <th>Box</th>
                <th>Montant</th>
                <th>Moyen</th>
                <th>Statut</th>
              </tr>
            </thead>
            <tbody>
              {c.payments.map((p) => (
                <tr key={p.id}>
                  <td>{dateTime(p.createdAt)}</td>
                  <td>{p.passCode}</td>
                  <td className="bo-mono">{p.boxId || '—'}</td>
                  <td>{fcfa(p.amountXof)}</td>
                  <td>
                    {p.provider === 'support'
                      ? 'Geste commercial'
                      : p.provider === 'naboopay'
                        ? 'NabooPay'
                        : p.provider === 'play'
                          ? 'Google Play'
                          : p.provider}
                  </td>
                  <td>
                    <span className={`bo-pill ${p.status === 'credited' ? 'ok' : p.status === 'pending' ? 'warn' : 'off'}`}>
                      {statusLabel[p.status] ?? p.status}
                    </span>
                  </td>
                </tr>
              ))}
              {c.payments.length === 0 ? (
                <tr>
                  <td colSpan={6} className="bo-muted">
                    Aucun paiement.
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
      </section>

      {history ? (
        <>
          <section className="bo-card flush" id="historique">
            <h2 className="bo-pad">Historique du foyer</h2>
            <div className="bo-table-wrap">
              <table className="bo-table">
                <thead>
                  <tr>
                    <th>Date</th>
                    <th>Source</th>
                    <th>Événement</th>
                    <th>Détail</th>
                  </tr>
                </thead>
                <tbody>
                  {history.events.map((e) => (
                    <tr key={e.id}>
                      <td>{dateTime(e.at)}</td>
                      <td>
                        <span className={`bo-pill ${e.failed ? 'warn' : 'off'}`}>{historyKindLabel[e.kind] ?? e.kind}</span>
                      </td>
                      <td className="bo-wrap-text">{e.summary}</td>
                      <td className="bo-wrap-text">{e.detail || <span className="bo-muted">—</span>}</td>
                    </tr>
                  ))}
                  {history.events.length === 0 ? (
                    <tr>
                      <td colSpan={4} className="bo-muted">
                        Rien côté hub pour ce foyer.
                      </td>
                    </tr>
                  ) : null}
                </tbody>
              </table>
            </div>
          </section>
          <div className="bo-row">
            {before ? (
              <Link className="bo-btn ghost" href={`${self}#historique`}>
                Plus récent
              </Link>
            ) : null}
            {history.next ? (
              <Link className="bo-btn ghost" href={`${self}?before=${encodeURIComponent(history.next)}#historique`}>
                Plus ancien
              </Link>
            ) : null}
          </div>
        </>
      ) : null}
    </>
  );
}
