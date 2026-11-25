import { DownloadIcon } from './_components/icons';
import { SiteFooter, SiteHeader } from './_components/chrome';
import { JsonLd } from './_components/download';
import { Motion } from './_components/Motion';
import { Discover, Faq, Features, FinalCta, Hero, Offers, Problems, Steps, Trust } from './_components/sections';

const nav = [
  { href: '#fonctions', label: 'Fonctionnalités' },
  { href: '#etapes', label: 'Comment ça marche' },
  { href: '#offres', label: 'Offres' },
  { href: '#questions', label: 'Questions' },
];

export default function Home() {
  return (
    <div data-kroot="" className="k-root">
      <JsonLd />
      <Motion />
      <SiteHeader brandHref="#top" nav={nav} progress>
        <a className="k-btn ghost" href="/compte">
          Se connecter
        </a>
        <a className="k-btn mono" href="#telecharger">
          <DownloadIcon size={16} width={2.4} />
          <span className="k-btn-label">Télécharger</span>
        </a>
      </SiteHeader>
      <main>
        <Hero />
        <Problems />
        <Discover />
        <Features />
        <Steps />
        <Offers />
        <Trust />
        <Faq />
        <FinalCta />
      </main>
      <SiteFooter />
    </div>
  );
}
