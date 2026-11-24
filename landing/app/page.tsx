import Image from 'next/image';
import { faq, offers, site } from '../lib/site';

const fcfa = (n: number) => (n === 0 ? '0 F' : `${n.toLocaleString('fr-FR').replace(/ /g, ' ')} F`);

function JsonLd() {
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

function Download({ variant = 'primary' }: { variant?: 'primary' | 'light' }) {
  return (
    <div className="actions">
      {site.playUrl ? (
        <a className={`btn ${variant}`} href={site.playUrl}>
          Télécharger sur Google Play
        </a>
      ) : null}
      <a className={`btn ${site.playUrl ? 'ghost' : variant}`} href={site.apkUrl} rel="nofollow" download>
        {site.playUrl ? 'Ou l’APK Android' : 'Télécharger l’app Android'}
      </a>
    </div>
  );
}

function Phone() {
  return (
    <div className="phone" role="img" aria-label="Aperçu de Kergui : une alerte pour un appareil non approuvé, avec les boutons Approuver et Bloquer">
      <div className="phone-screen">
        <div className="notif">
          <div className="notif-head">
            <Image src="/logo.png" alt="" width={20} height={20} />
            <span>Kergui · maintenant</span>
          </div>
          <strong>Appareil non approuvé sur votre Wi-Fi</strong>
          <p>Tablette-Visiteur vient de rejoindre votre Wi-Fi. Approuvez-le ou bloquez-le.</p>
          <div className="notif-actions">
            <span>Approuver</span>
            <span>Bloquer</span>
          </div>
        </div>
        <ul className="devices" aria-hidden="true">
          <li>
            <span className="dot ok" />
            Téléphone de Awa <em>Approuvé</em>
          </li>
          <li>
            <span className="dot ok" />
            TV du salon <em>Approuvé</em>
          </li>
          <li>
            <span className="dot warn" />
            Tablette-Visiteur <em className="warn">Non approuvé</em>
          </li>
          <li>
            <span className="dot ok" />
            Ordinateur du bureau <em>Approuvé</em>
          </li>
          <li>
            <span className="dot off" />
            PC de Moussa <em>Bloqué</em>
          </li>
        </ul>
        <div className="usage" aria-hidden="true">
          <span>Ce mois-ci</span>
          <strong>48,2 Go</strong>
          <div className="bar">
            <i style={{ width: '62%' }} />
          </div>
        </div>
      </div>
    </div>
  );
}

const features = [
  {
    title: 'Voyez qui est connecté',
    text: 'Chaque téléphone, TV ou ordinateur sur votre Wi-Fi, avec un nom que vous choisissez. Fini les « android-3f2a » incompréhensibles.',
  },
  {
    title: 'Alerté à chaque inconnu',
    text: 'Un appareil que vous n’avez pas approuvé arrive ? Vous le savez tout de suite, par notification et sur WhatsApp, à chaque fois qu’il revient.',
  },
  {
    title: 'Bloquez en un geste',
    text: 'Répondez « 1 » sur WhatsApp ou touchez Bloquer : l’appareil ne peut plus se connecter. Répondez « 2 » pour l’approuver.',
  },
  {
    title: 'Coupez Internet à l’heure du dîner',
    text: 'Coupez puis rétablissez l’accès d’un appareil, par exemple celui des enfants, sans toucher au reste de la maison.',
  },
  {
    title: 'Suivez la consommation',
    text: 'Qui utilise votre connexion, et combien. L’historique par appareil vous montre où part votre forfait.',
  },
  {
    title: 'Même loin de chez vous',
    text: 'Un téléphone ou un PC resté à la maison sert de relais. Vos ordres sont signés par votre clé : personne d’autre ne peut agir sur votre box.',
  },
];

const steps = [
  { n: '1', title: 'Installez l’app', text: 'Sur votre téléphone Android, connecté au Wi-Fi de la maison.' },
  { n: '2', title: 'Connectez votre box', text: 'Avec le mot de passe administrateur imprimé sous la box. Il reste chiffré sur votre téléphone.' },
  { n: '3', title: 'Approuvez vos appareils', text: 'Nommez ceux que vous connaissez. Les autres vous seront signalés.' },
];

export default function Home() {
  return (
    <>
      <JsonLd />
      <header className="top">
        <div className="wrap top-inner">
          <a href="#" className="brand" aria-label="Kergui, accueil">
            <Image src="/logo.png" alt="" width={36} height={36} priority />
            <span>Kergui</span>
          </a>
          <nav aria-label="Sections">
            <a href="#fonctionnalites">Fonctionnalités</a>
            <a href="#offres">Offres</a>
            <a href="#faq">Questions</a>
          </nav>
          <a className="btn primary top-cta" href="/compte">
            Se connecter
          </a>
        </div>
      </header>

      <main>
        <section className="hero">
          <div className="wrap hero-grid">
            <div>
              <p className="eyebrow">Pour les box fibre au Sénégal</p>
              <h1>Qui est sur votre Wi‑Fi&nbsp;? Kergui vous le dit, et vous laisse décider.</h1>
              <p className="lead">
                Voyez chaque appareil connecté à votre box, soyez alerté sur WhatsApp quand un inconnu arrive, et bloquez-le ou
                coupez Internet en un geste. Même quand vous n’êtes pas à la maison.
              </p>
              <Download />
              <p className="small">Gratuit pour commencer. Testé sur la box ZTE ZXHN F6600P. Les autres modèles et les répéteurs Wi-Fi sont en cours de développement.</p>
            </div>
            <Phone />
          </div>
        </section>

        <section id="fonctionnalites" className="section">
          <div className="wrap">
            <h2>Votre box, enfin simple</h2>
            <p className="section-lead">
              L’interface de votre box est faite pour les techniciens. Kergui la rend lisible, depuis votre téléphone.
            </p>
            <div className="grid3">
              {features.map((f) => (
                <article key={f.title} className="card">
                  <h3>{f.title}</h3>
                  <p>{f.text}</p>
                </article>
              ))}
            </div>
          </div>
        </section>

        <section className="section alt" aria-labelledby="whatsapp-title">
          <div className="wrap split">
            <div>
              <h2 id="whatsapp-title">Répondez « 1 », c’est bloqué</h2>
              <p>
                Pas besoin d’ouvrir l’app. Quand Kergui vous signale un appareil, répondez simplement au message WhatsApp :
              </p>
              <ul className="replies">
                <li>
                  <kbd>1</kbd> bloque l’appareil
                </li>
                <li>
                  <kbd>2</kbd> l’approuve : il ne vous sera plus signalé
                </li>
                <li>
                  <kbd>STOP</kbd> arrête les messages
                </li>
              </ul>
            </div>
            <div className="chat" aria-label="Exemple de conversation WhatsApp avec Kergui">
              <p className="bubble in">
                Kergui : un appareil non approuvé vient de rejoindre votre Wi-Fi.
                <br />
                1. Tablette-Visiteur (192.168.1.77)
                <br />
                Répondez 1 pour bloquer cet appareil, ou 2 pour l’approuver : il ne vous sera plus signalé.
              </p>
              <p className="bubble out">1</p>
              <p className="bubble in">✅ Tablette-Visiteur (192.168.1.77) est bloqué.</p>
            </div>
          </div>
        </section>

        <section className="section" aria-labelledby="steps-title">
          <div className="wrap">
            <h2 id="steps-title">Prêt en trois minutes</h2>
            <ol className="steps">
              {steps.map((s) => (
                <li key={s.n}>
                  <span className="step-n" aria-hidden="true">
                    {s.n}
                  </span>
                  <h3>{s.title}</h3>
                  <p>{s.text}</p>
                </li>
              ))}
            </ol>
          </div>
        </section>

        <section id="offres" className="section alt" aria-labelledby="offers-title">
          <div className="wrap">
            <h2 id="offers-title">Des passes de 30 jours, sans engagement</h2>
            <p className="section-lead">
              {site.buyEnabled
                ? 'Essayez Max gratuitement pendant 7 jours. Ensuite, prenez un pass quand vous en avez besoin.'
                : 'Bientôt : un essai Max gratuit de 7 jours, puis des passes quand vous en avez besoin.'}
            </p>
            <div className="grid3 offers">
              {offers.map((o) => (
                <article key={o.name} className={`card offer${o.highlight ? ' highlight' : ''}`}>
                  {o.highlight ? <p className="badge">Le plus complet</p> : null}
                  <h3>{o.name}</h3>
                  <p className="price">
                    {fcfa(o.price)}
                    {o.price > 0 ? <span> / 30 jours</span> : null}
                  </p>
                  <p className="pitch">{o.pitch}</p>
                  <ul>
                    {o.features.map((f) => (
                      <li key={f}>{f}</li>
                    ))}
                  </ul>
                </article>
              ))}
            </div>
            <div className="center">
              {site.buyEnabled ? (
                <a className="btn primary" href="/compte">
                  Prendre un pass ou essayer Max 7 jours
                </a>
              ) : (
                <p className="small">Les passes seront bientôt disponibles ici. En attendant, Kergui est gratuit.</p>
              )}
            </div>
          </div>
        </section>

        <section id="faq" className="section" aria-labelledby="faq-title">
          <div className="wrap narrow">
            <h2 id="faq-title">Questions fréquentes</h2>
            {faq.map((f) => (
              <details key={f.q}>
                <summary>{f.q}</summary>
                <p>{f.a}</p>
              </details>
            ))}
          </div>
        </section>

        <section className="cta">
          <div className="wrap center">
            <h2>Reprenez la main sur votre Wi-Fi</h2>
            <Download variant="light" />
          </div>
        </section>
      </main>

      <footer className="foot">
        <div className="wrap foot-inner">
          <p>
            © {new Date().getFullYear()} Kergui. Projet indépendant, sans lien avec votre fournisseur d’accès. Les noms des box
            appartiennent à leurs propriétaires.
          </p>
          <a href={site.sourceUrl}>Code source</a>
        </div>
      </footer>
    </>
  );
}
