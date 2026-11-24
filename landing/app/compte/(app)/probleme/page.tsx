import Link from 'next/link';
import type { Metadata } from 'next';
import { licensing, type CustomerComplaint } from '../../../../lib/account';
import { complaintCategoryLabel, complaintSourceLabel, dateTime } from '../../../../lib/format';
import { ReplyForm, ReportForm } from './forms';

export const metadata: Metadata = { title: 'Signaler un problème' };

// The customer's words for a complaint's status (the back-office's are the team's).
const statusLabel: Record<string, string> = { open: 'En attente', answered: 'Répondu', closed: 'Clos' };
const statusTone: Record<string, string> = { open: 'warn', answered: 'ok', closed: '' };

function Thread({ c }: { c: CustomerComplaint }) {
  return (
    <section className="cl-card" id={`n${c.number}`}>
      <div className="cl-row" style={{ justifyContent: 'space-between', marginBottom: 12 }}>
        <h2 style={{ margin: 0 }}>
          N° {c.number} · {complaintCategoryLabel[c.category] ?? c.category}
        </h2>
        <span className={`cl-pill ${statusTone[c.status] ?? ''}`}>{statusLabel[c.status] ?? c.status}</span>
      </div>
      <p className="cl-muted" style={{ marginTop: 0 }}>
        Envoyé le {dateTime(c.createdAt)} · {complaintSourceLabel[c.source] ?? c.source}
      </p>
      <ol className="cl-thread">
        {(c.messages ?? []).map((m) => (
          <li key={m.id} className={`cl-msg ${m.from}`}>
            <header>
              <strong>{m.from === 'admin' ? 'Équipe Kergui' : 'Vous'}</strong>
              <span>· {dateTime(m.at)}</span>
            </header>
            <p>{m.body}</p>
          </li>
        ))}
      </ol>
      {c.status === 'closed' ? (
        <p className="cl-muted">Ce signalement est clos. Pour un nouveau problème, utilisez le formulaire ci-dessus.</p>
      ) : (
        <ReplyForm number={c.number} />
      )}
    </section>
  );
}

export default async function ProblemPage() {
  const { complaints } = await licensing<{ complaints: CustomerComplaint[] }>('complaints');
  return (
    <>
      <p>
        <Link href="/compte">← Mes passes</Link>
      </p>
      <h1>Signaler un problème</h1>
      <section className="cl-card">
        <p style={{ marginTop: 0 }}>
          Un paiement non crédité, une box qui ne répond pas, l’application qui se comporte mal : dites-le à l’équipe
          Kergui. La réponse arrive ici, et dans l’application Kergui si vous l’avez.
        </p>
        <ReportForm />
      </section>

      <h2>Mes signalements</h2>
      {complaints.length === 0 ? (
        <p className="cl-muted">Aucun signalement pour l’instant.</p>
      ) : (
        complaints.map((c) => <Thread key={c.id} c={c} />)
      )}
    </>
  );
}
