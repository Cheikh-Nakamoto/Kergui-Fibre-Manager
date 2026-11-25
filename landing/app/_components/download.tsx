import { faq, offers, site } from '../../lib/site';
import { DownloadIcon, PlayIcon } from './icons';

// The download buttons. The app is not on Google Play yet: unless
// NEXT_PUBLIC_PLAY_URL is set, the one button downloads the newest APK
// (/telecharger, or NEXT_PUBLIC_APK_URL when it is a file).
export function Download() {
  return (
    <>
      {site.playUrl ? (
        <a className="k-store" href={site.playUrl}>
          <PlayIcon size={22} />
          <span>
            <small>Disponible sur</small>
            <strong>Google Play</strong>
          </span>
        </a>
      ) : null}
      <a className={site.playUrl ? 'k-btn ghost tall' : 'k-store'} href={site.apkUrl} rel="nofollow" download>
        {site.playUrl ? (
          'Ou l’APK Android'
        ) : (
          <>
            <DownloadIcon size={22} />
            <span>
              <small>Application Android</small>
              <strong>Télécharger</strong>
            </span>
          </>
        )}
      </a>
    </>
  );
}

// SoftwareApplication (with the offers) and FAQPage, as on the old page.
export function JsonLd() {
  const data = [
    {
      '@context': 'https://schema.org',
      '@type': 'SoftwareApplication',
      name: site.name,
      applicationCategory: 'UtilitiesApplication',
      operatingSystem: 'Android',
      description: site.description,
      url: site.url,
      inLanguage: 'fr',
      offers: offers.map((o) => ({
        '@type': 'Offer',
        name: o.name,
        price: o.price,
        priceCurrency: 'XOF',
        description: o.pitch,
        ...(o.price > 0 && site.buyEnabled ? { url: site.buyUrl } : {}),
      })),
    },
    {
      '@context': 'https://schema.org',
      '@type': 'FAQPage',
      mainEntity: faq.map((f) => ({
        '@type': 'Question',
        name: f.q,
        acceptedAnswer: { '@type': 'Answer', text: f.a },
      })),
    },
  ];
  return (
    <script
      type="application/ld+json"
      // Escaped so no answer text can close the script tag.
      dangerouslySetInnerHTML={{ __html: JSON.stringify(data).replace(/</g, '\\u003c') }}
    />
  );
}
