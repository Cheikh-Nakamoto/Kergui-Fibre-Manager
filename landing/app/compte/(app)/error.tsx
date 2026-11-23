'use client';

export default function CompteError({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return (
    <div className="cl-card">
      <h1>Impossible d’afficher votre compte</h1>
      <p className="cl-muted">Le serveur Kergui n’a pas répondu comme prévu. Réessayez dans un instant.</p>
      {error.digest ? <p className="cl-muted cl-mono">Référence : {error.digest}</p> : null}
      <div className="cl-row">
        <button type="button" className="cl-btn" onClick={reset}>
          Réessayer
        </button>
        <a className="cl-btn ghost" href="/compte/connexion">
          Se reconnecter
        </a>
      </div>
    </div>
  );
}
