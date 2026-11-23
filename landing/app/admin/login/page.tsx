import Image from 'next/image';
import type { Metadata } from 'next';
import { LoginForm } from './form';

export const metadata: Metadata = { title: 'Connexion' };

export default function LoginPage() {
  return (
    <main className="ad-login">
      <div className="ad-card ad-login-card">
        <div className="ad-brand">
          <Image src="/logo.png" alt="" width={32} height={32} />
          <span>Kergui</span>
          <small>back-office</small>
        </div>
        <h1>Connexion</h1>
        <p className="ad-muted">Réservé à l’équipe Kergui (comptes administrateurs de la plateforme).</p>
        <LoginForm />
      </div>
    </main>
  );
}
