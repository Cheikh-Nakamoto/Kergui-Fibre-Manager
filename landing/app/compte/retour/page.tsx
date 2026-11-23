import Image from 'next/image';
import Link from 'next/link';
import type { Metadata } from 'next';

export const metadata: Metadata = { title: 'Paiement' };

// Where NabooPay sends the payer back, when the hub's
// KERGUI_HUB_LICENSING_RETURN_URL points here. The pass is credited by the
// webhook, never by this page, which anyone can open.
export default async function ReturnPage({ searchParams }: { searchParams: Promise<{ status?: string }> }) {
  const { status } = await searchParams;
  const ok = status !== 'error';
  return (
    <main className="cl-login">
      <div className="cl-card cl-login-card">
        <Link href="/" className="cl-brand">
          <Image src="/logo.png" alt="" width={32} height={32} />
          <span>Kergui</span>
          <small>mon compte</small>
        </Link>
        <h1>{ok ? 'Paiement reçu' : 'Paiement non abouti'}</h1>
        <p className="cl-muted">
          {ok
            ? 'Votre pass sera actif dans quelques instants, dès que le paiement est confirmé. Ouvrez l’application Kergui pour en profiter.'
            : 'Le paiement a été annulé ou n’a pas abouti. Vous pouvez réessayer depuis votre compte.'}
        </p>
        <Link className="cl-btn" href="/compte">
          {ok ? 'Voir mes passes' : 'Retour à mon compte'}
        </Link>
      </div>
    </main>
  );
}
