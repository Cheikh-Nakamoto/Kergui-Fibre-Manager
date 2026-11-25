import Link from 'next/link';
import { site } from '../../lib/site';
import { WifiIcon } from './icons';

// The site's shared chrome: the KERGUI wordmark, the sticky header and the
// footer, used by the homepage and the customer area (/compte).

export function Brand({ href = '/', sub }: { href?: string; sub?: string }) {
  return (
    <Link href={href} className="k-brand" aria-label={sub ? undefined : 'Kergui, accueil'}>
      <WifiIcon size={26} color="var(--green)" dot />
      <span className="k-brand-name">KERGUI</span>
      {sub ? <small>{sub}</small> : null}
    </Link>
  );
}

export function SiteHeader({
  brandHref,
  sub,
  nav,
  progress,
  children,
}: {
  brandHref?: string;
  sub?: string;
  /** In-page section links, hidden on phones. */
  nav?: { href: string; label: string }[];
  /** The scroll progress bar under the header (homepage only). */
  progress?: boolean;
  children?: React.ReactNode;
}) {
  return (
    <header className="k-top">
      <div className="k-top-inner">
        <Brand href={brandHref} sub={sub} />
        {nav ? (
          <nav className="k-nav" aria-label="Sections">
            {nav.map((n) => (
              <a key={n.href} href={n.href}>
                {n.label}
              </a>
            ))}
          </nav>
        ) : null}
        <div className="k-top-actions">{children}</div>
      </div>
      {progress ? <div className="k-prog" aria-hidden="true" /> : null}
    </header>
  );
}

export function SiteFooter() {
  return (
    <footer className="k-foot">
      <div className="k-foot-inner">
        <span className="k-brand-name">KERGUI</span>
        <p>
          © {new Date().getFullYear()} Kergui. Projet indépendant, sans lien avec votre fournisseur d’accès. Les noms des
          box appartiennent à leurs propriétaires.
        </p>
        <a href={site.sourceUrl}>Code source</a>
      </div>
    </footer>
  );
}
