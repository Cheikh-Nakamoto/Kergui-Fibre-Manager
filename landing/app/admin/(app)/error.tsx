'use client';

export default function AdminError({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return (
    <div className="ad-card">
      <h1>Impossible d’afficher cette page</h1>
      <p className="ad-muted">
        Le serveur Kergui n’a pas répondu comme prévu. Si ce compte n’est pas administrateur de la plateforme, l’accès est
        refusé.
      </p>
      {error.digest ? <p className="ad-muted ad-mono">Référence : {error.digest}</p> : null}
      <div className="ad-row">
        <button type="button" className="ad-btn" onClick={reset}>
          Réessayer
        </button>
        <a className="ad-btn ghost" href="/admin/login">
          Se reconnecter
        </a>
      </div>
    </div>
  );
}
