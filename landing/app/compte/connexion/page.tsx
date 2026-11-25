import Link from 'next/link';
import type { Metadata } from 'next';
import { Brand, SiteFooter } from '../../_components/chrome';
import { SignInForm } from './form';

export const metadata: Metadata = { title: 'Connexion' };

export default async function SignInPage({ searchParams }: { searchParams: Promise<{ expire?: string }> }) {
  const { expire } = await searchParams;
  return (
    <>
      <main className="cl-login">
        <div className="cl-card cl-login-card hud">
          <Link href="/" className="cl-back">
            ← Retour à l’accueil
          </Link>
          <Brand sub="mon compte" />
          <h1>Connexion</h1>
          <p className="cl-muted">
            Avec le numéro WhatsApp de votre compte Kergui, le même que dans l’application. Nous vous envoyons un code à
            6 chiffres.
          </p>
          {expire ? <p className="cl-info">Votre session a expiré. Reconnectez-vous.</p> : null}
          <SignInForm />
        </div>
      </main>
      <SiteFooter />
    </>
  );
}
