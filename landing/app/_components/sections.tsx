import Image from 'next/image';
import Link from 'next/link';
import { faq, offers, site } from '../../lib/site';
import { Download } from './download';
import { Fan } from './Fan';
import { BellIcon } from './icons';
import { DeviceScreen, DevicesScreen, MoreScreen, PhoneFrame, UsageScreen, WhatsAppScreen, WifiScreen } from './phones';

const fcfa = (n: number) => (n === 0 ? '0 F' : `${n.toLocaleString('fr-FR').replace(/\s/g, '\u00a0')} F`);

export function Hero() {
  return (
    <section id="top" className="grid-bg hero">
      <div className="glow hero-glow">
        <div className="wrap hero-grid">
          <div className="hero-copy" data-px="-0.05">
            <p className="eyebrow rv rv-up">[ BOX FIBRE · SÉNÉGAL ]</p>
            <h1 className="hero-title">
              <span className="rv rv-up d1">Voyez.</span>
              <br />
              <span className="rv rv-up d2 em">Décidez.</span>
            </h1>
            <p className="hero-lead rv rv-up d3">
              Chaque appareil connecté à votre box, une alerte WhatsApp quand un nouvel appareil arrive, et un geste pour le bloquer.
              Même quand vous n’êtes pas à la maison.
            </p>
            <div className="k-actions rv rv-up d4">
              <Download />
              <a className="k-btn ghost tall" href="#offres">
                Voir les offres
              </a>
            </div>
            <p className="hero-small rv rv-up d5">
              Gratuit pour commencer. Testé sur la box ZTE ZXHN F6600P.
              <br />
              Autres modèles et répéteurs Wi-Fi en cours de développement.
            </p>
          </div>

          <div className="hero-phone" data-px="0.1">
            <span className="ring" aria-hidden="true" />
            <span className="ring r2" aria-hidden="true" />
            <span className="ring r3" aria-hidden="true" />
            <PhoneFrame
              className="ph-hero hud rv rv-sc d2 float"
              label="Aperçu de Kergui : la liste des appareils connectés, avec un nouvel appareil signalé"
            >
              <DevicesScreen />
            </PhoneFrame>
            <div className="notif" data-px="-0.07" aria-hidden="true">
              <span className="notif-head">KERGUI · maintenant</span>
              <strong>Appareil à vérifier sur votre Wi-Fi</strong>
              <span>Une tablette Samsung, arrivée à 21 h 05, vient de rejoindre votre Wi-Fi. Ouvrez pour l’autoriser ou la bloquer.</span>
              <div className="notif-actions">
                <span>Autoriser</span>
              </div>
            </div>
          </div>
        </div>
        <div className="scroll-hint" aria-hidden="true">
          <span className="bob">DÉFILER ↓</span>
        </div>
      </div>
    </section>
  );
}

export function Problems() {
  return (
    <section className="problems" aria-label="Ce que Kergui résout">
      <div className="wrap">
        <div className="prob-row">
          <div className="prob-visual rv rv-l">
            <div className="receipt" data-px="0.08" aria-hidden="true">
              <div className="receipt-head">ÉTAT DE LA BOX · CLIENTS</div>
              <div>
                android-3f2a ……… <span className="blink">?</span>
              </div>
              <div className="struck">HUAWEI-P30-7c1e ……</div>
              <div>
                ESP_4F2A1B ……… <span className="blink b2">??</span>
              </div>
              <div>
                192.168.1.77 ……… <span className="blink b3">???</span>
              </div>
            </div>
          </div>
          <h2 className="prob-title rv rv-r d1">
            Des appareils sur votre box… mais vous ne savez pas <em>qui c’est</em> ?
          </h2>
        </div>

        <div className="prob-row reverse">
          <h2 className="prob-title rv rv-l">
            Votre connexion rame… sans savoir <em>qui consomme</em> ?
          </h2>
          <div className="prob-visual rv rv-r d1">
            <div className="usage-card hud" data-px="0.06" aria-hidden="true">
              <span className="mono-label">CONSOMMATION · 30 JOURS</span>
              {[
                { n: 'TV du salon', v: '21,4 Go', w: 88, hot: true },
                { n: 'Téléphone de Awa', v: '12,9 Go', w: 54 },
                { n: 'Ordinateur du bureau', v: '8,6 Go', w: 36 },
              ].map((r) => (
                <div key={r.n} className="usage-row">
                  <div className="usage-line">
                    <span>{r.n}</span>
                    <span className={r.hot ? 'hot' : undefined}>{r.v}</span>
                  </div>
                  <div className="meter">
                    <div className={`bar${r.hot ? ' hot' : ''}`} style={{ width: `${r.w}%` }} />
                  </div>
                </div>
              ))}
            </div>
          </div>
        </div>

        <div className="prob-row">
          <div className="prob-visual rv rv-l">
            <div className="curfew" aria-hidden="true">
              <div className="curfew-time">20:00</div>
              <div className="curfew-row">
                <span>Tablette des enfants</span>
                <span className="switch track">
                  <span className="knob" />
                </span>
              </div>
              <div className="curfew-row">
                <span>TV du salon</span>
                <span className="switch on">
                  <span />
                </span>
              </div>
            </div>
          </div>
          <h2 className="prob-title rv rv-r d1">
            Couper le Wi-Fi des enfants… sans <em>éteindre toute la maison</em> ?
          </h2>
        </div>
      </div>
    </section>
  );
}

export function Discover() {
  return (
    <section className="grid-bg discover" aria-labelledby="discover-title">
      <div className="wrap discover-inner">
        <h2 id="discover-title" className="big-title center rv rv-up">
          Découvrez <em>Kergui</em>
        </h2>
        <Fan>
          <PhoneFrame
            className="fan-item fan-o1 rv rv-sc"
            data-px="0.16"
            data-sx="150"
            label="L’écran Wi-Fi : allumer, renommer un réseau, changer son mot de passe"
          >
            <WifiScreen />
          </PhoneFrame>
          <PhoneFrame
            className="fan-item fan-i1 rv rv-sc d1"
            data-px="0.08"
            data-sx="75"
            label="L’écran Conso : la consommation du mois par appareil"
          >
            <UsageScreen />
          </PhoneFrame>
          <PhoneFrame className="fan-item fan-c hud rv rv-sc" label="Une alerte WhatsApp de Kergui, la réponse « 1 », puis l’appareil bloqué">
            <WhatsAppScreen />
          </PhoneFrame>
          <PhoneFrame
            className="fan-item fan-i2 rv rv-sc d1"
            data-px="0.08"
            data-sx="-75"
            label="La fiche d’un appareil : autorisé, sa consommation sur 6 mois, Bloquer et Couper l’accès"
          >
            <DeviceScreen />
          </PhoneFrame>
          <PhoneFrame
            className="fan-item fan-o2 rv rv-sc"
            data-px="0.16"
            data-sx="-150"
            label="Plus de fonctionnalités : relais à la maison, foyer et membres, alertes WhatsApp, rejet automatique"
          >
            <MoreScreen />
          </PhoneFrame>
        </Fan>
        <div className="discover-copy rv rv-up">
          <strong>Répondez « 1 », c’est bloqué</strong>
          <p>Pas besoin d’ouvrir l’app : répondez directement au message WhatsApp. OK l’autorise, STOP arrête les messages.</p>
        </div>
      </div>
    </section>
  );
}

const features: { title: string; text: string; demo: React.ReactNode }[] = [
  {
    title: 'Voyez qui est connecté',
    text: 'Chaque téléphone, TV ou ordinateur, avec un nom que vous choisissez.',
    demo: (
      <div className="demo mono col">
        <s>android-3f2a</s>
        <span className="accent">→ Téléphone de Awa</span>
      </div>
    ),
  },
  {
    title: 'Alerté à chaque nouvel appareil',
    text: 'Par notification et sur WhatsApp, dès qu’un appareil à vérifier arrive.',
    demo: (
      <div className="demo warn">
        <BellIcon size={18} />
        À vérifier · à chaque retour
      </div>
    ),
  },
  {
    title: 'Bloquez en un geste',
    text: 'Répondez « 1 » sur WhatsApp ou touchez Bloquer : l’appareil ne peut plus se connecter.',
    demo: (
      <div className="demo-pair">
        <span className="on">1 · Bloquer</span>
        <span>OK · Autoriser</span>
      </div>
    ),
  },
  {
    title: 'Coupez à l’heure du dîner',
    text: 'Coupez puis rétablissez l’accès d’un appareil, sans toucher au reste de la maison.',
    demo: (
      <div className="demo split">
        <span>Accès · Tablette des enfants</span>
        <span className="tag">COUPÉ</span>
      </div>
    ),
  },
  {
    title: 'Suivez la consommation',
    text: 'Qui utilise votre connexion, et combien. L’historique montre où part votre forfait.',
    demo: (
      <div className="demo col">
        <div className="usage-line">
          <span className="muted">Ce mois-ci</span>
          <strong className="display">48,2 Go</strong>
        </div>
        <div className="meter thin">
          <div className="bar" style={{ width: '68%' }} />
        </div>
      </div>
    ),
  },
  {
    title: 'Même loin de chez vous',
    text: 'Un téléphone ou un PC resté à la maison sert de relais. Personne d’autre que votre foyer ne peut agir sur votre box.',
    demo: (
      <div className="demo">
        <span className="live-dot" aria-hidden="true" />
        Relais actif · PC de la maison
      </div>
    ),
  },
  {
    title: 'Foyer et membres',
    text: 'Invitez les membres du foyer et choisissez qui peut voir ou contrôler la box.',
    demo: (
      <div className="demo-chips">
        <span>Awa · contrôle</span>
        <span className="muted">Moussa · lecture</span>
      </div>
    ),
  },
  {
    title: 'Rejet automatique',
    text: 'Les appareils que vous n’avez pas autorisés sont refusés tout seuls.',
    demo: (
      <div className="demo split">
        <span>Non autorisés refusés</span>
        <span className="tag">AUTO</span>
      </div>
    ),
  },
];

export function Features() {
  return (
    <section id="fonctions" className="sec sec-alt" aria-labelledby="features-title">
      <div className="wrap stack">
        <div className="sec-head rv rv-up">
          <p className="eyebrow">[ DANS L’APP ]</p>
          <h2 id="features-title" className="big-title">
            Votre box, <em>enfin simple</em>
          </h2>
          <p className="sec-lead">L’interface de votre box est faite pour les techniciens. Kergui la rend lisible, depuis votre téléphone.</p>
        </div>
        <div className="feature-grid" data-stagger="0.08">
          {features.map((f) => (
            <article key={f.title} className="k-card hud rv rv-up card">
              <div aria-hidden="true">{f.demo}</div>
              <h3>{f.title}</h3>
              <p>{f.text}</p>
            </article>
          ))}
        </div>
      </div>
    </section>
  );
}

const steps = [
  { title: 'Installez l’app', text: 'Sur votre téléphone Android, connecté au Wi-Fi de la maison.' },
  { title: 'Connectez votre box', text: 'Avec le mot de passe administrateur imprimé sous la box. Il reste chiffré sur votre téléphone.' },
  { title: 'Autorisez vos appareils', text: 'Nommez ceux que vous connaissez. Les autres vous seront signalés.' },
];

export function Steps() {
  return (
    <section id="etapes" className="sec sec-paper" aria-labelledby="steps-title">
      <div className="wrap stack">
        <div className="sec-head rv rv-up">
          <p className="eyebrow brand">[ EN 3 ÉTAPES ]</p>
          <h2 id="steps-title" className="big-title">
            Prêt en <em className="hl">trois minutes</em>
          </h2>
        </div>
        <div className="steps-grid">
          <ol className="steps" data-stagger="0.18">
            {steps.map((s, i) => (
              <li key={s.title} className={`rv rv-l${i === steps.length - 1 ? ' last' : ''}`}>
                <span className="step-n" aria-hidden="true">
                  {i === steps.length - 1 ? i + 1 : '✓'}
                </span>
                <div>
                  <h3>
                    <span className="sr-only">Étape {i + 1} : </span>
                    {s.title}
                  </h3>
                  <p>{s.text}</p>
                </div>
              </li>
            ))}
          </ol>
          <div className="steps-phone" data-px="0.08">
            <div className="steps-count" aria-hidden="true">
              <strong>03</strong> / 03
            </div>
            <PhoneFrame className="ph-steps hud rv rv-sc" label="La fiche d’un appareil autorisé dans Kergui">
              <DeviceScreen />
            </PhoneFrame>
          </div>
        </div>
      </div>
    </section>
  );
}

export function Offers() {
  return (
    <section id="offres" className="grid-bg sec" aria-labelledby="offers-title">
      <div className="wrap stack">
        <div className="sec-head center rv rv-up">
          <p className="eyebrow">[ SANS ENGAGEMENT ]</p>
          <h2 id="offers-title" className="big-title">
            Des passes de <em>30 jours</em>
          </h2>
          <p className="sec-lead">
            {site.buyEnabled
              ? 'Essayez Max gratuitement pendant 7 jours. Ensuite, prenez un pass quand vous en avez besoin.'
              : 'Bientôt : un essai Max gratuit de 7 jours, puis des passes quand vous en avez besoin.'}
          </p>
        </div>
        <div className="offer-grid" data-stagger="0.14">
          {offers.map((o) => (
            <article key={o.name} className={`offer rv rv-up card${o.highlight ? ' hud maxcard' : ''}`}>
              <div className="offer-top">
                <h3 className="mono-label">{o.name.toUpperCase()}</h3>
                {o.highlight ? <span className="offer-badge">LE PLUS COMPLET</span> : null}
              </div>
              <p className="offer-price">
                {fcfa(o.price)}
                {o.price > 0 ? <span> / 30 jours</span> : null}
              </p>
              <p className="offer-pitch">{o.pitch}</p>
              <ul>
                {o.features.map((f) => (
                  <li key={f}>— {f}</li>
                ))}
              </ul>
              {o.highlight && site.buyEnabled ? (
                <Link className="k-btn wide" href="/compte">
                  Essayer Max 7 jours
                </Link>
              ) : null}
            </article>
          ))}
        </div>
        {site.buyEnabled ? (
          <p className="center">
            <Link className="k-btn ghost" href="/compte">
              Prendre un pass
            </Link>
          </p>
        ) : (
          <p className="center muted small-text">Les passes seront bientôt disponibles ici. En attendant, Kergui est gratuit.</p>
        )}
      </div>
    </section>
  );
}

const trust = [
  { t: 'Mot de passe chiffré', d: 'Le mot de passe de la box reste chiffré sur votre téléphone. Il ne s’affiche qu’après déverrouillage.' },
  { t: 'Vous seul décidez', d: 'Vos actions à distance sont vérifiées avant d’arriver à la box : personne d’autre que votre foyer ne peut agir dessus.' },
  { t: 'Vos données, exportables', d: 'Une sauvegarde .zip (appareils.csv, conso.csv) à garder sur Drive, WhatsApp ou un PC.' },
  { t: 'Téléphone perdu ?', d: 'Un co-propriétaire, ou le mot de passe de la box à la maison, rend le foyer à un nouveau téléphone.' },
  { t: 'Rien ne se débloque seul', d: 'À l’échéance d’un pass, les blocages déjà en place restent en place.' },
  { t: 'Compte supprimable', d: 'Effacez votre compte et ce que Kergui garde, directement depuis l’app.' },
];

export function Trust() {
  return (
    <section className="sec sec-alt" aria-labelledby="trust-title">
      <div className="wrap stack">
        <h2 id="trust-title" className="big-title rv rv-up">
          Votre box reste <em>à vous</em>
        </h2>
        <div className="trust-grid" data-stagger="0.07">
          {trust.map((x, i) => (
            <div key={x.t} className="trust rv rv-up card">
              <span className="num">{String(i + 1).padStart(2, '0')}</span>
              <strong>{x.t}</strong>
              <span>{x.d}</span>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

export function Faq() {
  return (
    <section id="questions" className="sec" aria-labelledby="faq-title">
      <div className="wrap narrow stack">
        <h2 id="faq-title" className="big-title rv rv-up">
          Questions <em>fréquentes</em>
        </h2>
        <div className="faq-list" data-stagger="0.07">
          {faq.map((f, i) => (
            <details key={f.q} className="faq rv rv-l">
              <summary>
                <span className="num">{String(i + 1).padStart(2, '0')}</span>
                <span className="faq-q">{f.q}</span>
                <span className="faq-plus" aria-hidden="true">
                  +
                </span>
              </summary>
              <p>{f.a}</p>
            </details>
          ))}
        </div>
      </div>
    </section>
  );
}

export function FinalCta() {
  return (
    <section id="telecharger" className="grid-bg sec sec-alt" aria-labelledby="cta-title">
      <div className="wrap glow cta-grid">
        <div className="cta-copy rv rv-l">
          <h2 id="cta-title" className="huge-title">
            Votre box.
            <br />
            <em>Vos règles.</em>
          </h2>
          <p>Reprenez la main sur votre Wi-Fi, gratuitement pour commencer.</p>
          <div className="k-actions">
            <Download />
          </div>
        </div>
        <div className="qr hud rv rv-sc d2 float">
          <Image src="/qr-telecharger.svg" alt="QR code : kergui.click/telecharger" width={160} height={160} />
          <span>SCANNEZ POUR INSTALLER</span>
        </div>
      </div>
    </section>
  );
}
