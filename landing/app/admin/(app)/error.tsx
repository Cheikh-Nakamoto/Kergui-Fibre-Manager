'use client';

export default function AdminError({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return (
    <div className="bo-card">
      <h1>Impossible d’afficher cette page</h1>
      <p className="bo-muted">
        Le serveur Kergui n’a pas répondu comme prévu. Si ce compte n’est pas administrateur de la plateforme, l’accès est
        refusé.
      </p>
      {error.digest ? <p className="bo-muted bo-mono">Référence : {error.digest}</p> : null}
      <div className="bo-row">
        <button type="button" className="bo-btn" onClick={reset}>
          Réessayer
        </button>
        <a className="bo-btn ghost" href="/admin/login">
          Se reconnecter
        </a>
      </div>
    </div>
  );
}
