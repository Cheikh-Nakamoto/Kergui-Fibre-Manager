import Image from 'next/image';
import Link from 'next/link';
import type { Metadata } from 'next';
import { LoginForm } from './form';

export const metadata: Metadata = { title: 'Connexion' };

export default function LoginPage() {
  return (
    <main className="bo-login">
      <div className="bo-card bo-login-card">
        <Link href="/" className="bo-back">
          ← Retour au site
        </Link>
        <div className="bo-brand">
          <Image src="/logo.png" alt="" width={32} height={32} />
          <span>Kergui</span>
          <small>back-office</small>
        </div>
        <h1>Connexion</h1>
        <p className="bo-muted">Réservé à l’équipe Kergui (comptes administrateurs de la plateforme).</p>
        <LoginForm />
      </div>
    </main>
  );
}
