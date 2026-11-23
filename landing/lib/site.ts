// Everything that changes between deployments comes from NEXT_PUBLIC_* at
// build time (Dokploy build args). The defaults keep a local build working.

function url(value: string | undefined, fallback: string): string {
  return (value && value.trim() ? value.trim() : fallback).replace(/\/+$/, '');
}

export const site = {
  name: 'Kergui',
  title: 'Kergui : qui est sur votre Wi-Fi ? Gérez votre box fibre depuis votre téléphone',
  description:
    'Kergui vous montre chaque appareil connecté à votre box fibre, vous alerte sur WhatsApp quand un inconnu se connecte, et vous permet de le bloquer ou de couper Internet en un geste, même à distance.',
  /** The public address of this site, for canonical links and the sitemap. */
  url: url(process.env.NEXT_PUBLIC_SITE_URL, 'https://kergui.sn'),
  /** The hub's purchase page (passes and the 7-day trial). */
  buyUrl: url(process.env.NEXT_PUBLIC_HUB_URL, 'https://hub.kergui.sn') + '/licensing/v1/buy',
  /** Where to get the app. Empty hides the button. */
  playUrl: process.env.NEXT_PUBLIC_PLAY_URL?.trim() ?? '',
  /**
   * The download button: /telecharger starts the newest APK directly. A
   * NEXT_PUBLIC_APK_URL is used only when it is a file (.apk), never a page
   * to land on.
   */
  apkUrl: process.env.NEXT_PUBLIC_APK_URL?.trim().endsWith('.apk')
    ? process.env.NEXT_PUBLIC_APK_URL.trim()
    : '/telecharger',
  sourceUrl: 'https://github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager',
  locale: 'fr_SN',
} as const;

export type Offer = {
  name: string;
  price: number; // FCFA per 30 days
  pitch: string;
  features: string[];
  highlight?: boolean;
};

// The offers of RFC-006. Prices are shown here, on the web; the Play app
// itself shows none (V1 rule).
export const offers: Offer[] = [
  {
    name: 'Gratuit',
    price: 0,
    pitch: 'Pour savoir qui est chez vous.',
    features: [
      'La liste des appareils connectés, en direct',
      'Alerte à chaque appareil non approuvé (notification et WhatsApp)',
      'Approuver ou renommer vos appareils',
      'La consommation du mois en cours',
    ],
  },
  {
    name: 'Basic',
    price: 650,
    pitch: 'Pour reprendre la main.',
    features: ['Tout le Gratuit', 'Bloquer un appareil en un geste', 'Couper puis rétablir l’accès Wi-Fi'],
  },
  {
    name: 'Max',
    price: 2000,
    pitch: 'Pour tout gérer, même à distance.',
    highlight: true,
    features: [
      'Tout le Basic',
      'Historique de consommation par appareil',
      'Gérer vos réseaux Wi-Fi : éteindre, renommer, changer le mot de passe',
      'Rejet automatique des appareils non acceptés après 15 minutes',
    ],
  },
];

export const faq: { q: string; a: string }[] = [
  {
    q: 'Quelles box sont compatibles ?',
    a: 'Les box fibre ZTE distribuées au Sénégal : ZXHN F660, F680, F6600P et Funbox. Kergui se connecte à l’interface de votre box (192.168.1.1) avec le mot de passe administrateur imprimé sous la box. Il ne remplace pas son logiciel.',
  },
  {
    q: 'Est-ce que Kergui est lié à mon opérateur ?',
    a: 'Non. Kergui est un projet indépendant, sans lien avec votre fournisseur d’accès. Vos réglages restent sur votre box, et vous pouvez arrêter d’utiliser Kergui à tout moment.',
  },
  {
    q: 'Comment fonctionnent les alertes WhatsApp ?',
    a: 'Vous écrivez d’abord au numéro Kergui pour les activer. Ensuite, dès qu’un appareil que vous n’avez pas approuvé rejoint votre Wi-Fi, vous recevez un message. Répondez « 1 » pour le bloquer, « 2 » pour l’approuver, ou STOP pour ne plus rien recevoir.',
  },
  {
    q: 'Puis-je gérer ma box quand je ne suis pas à la maison ?',
    a: 'Oui. Un téléphone ou un PC resté à la maison sert de relais : vos ordres passent par lui, signés par votre propre clé. Le serveur Kergui transporte les commandes, il ne peut pas en inventer.',
  },
  {
    q: 'Comment payer un pass ?',
    a: 'Les passes se prennent sur notre page web, pour 30 jours, sans abonnement caché. Vous pouvez aussi essayer Max gratuitement pendant 7 jours, une fois par numéro et par box.',
  },
  {
    q: 'Mes données sont-elles envoyées ailleurs ?',
    a: 'Sans compte, tout reste sur votre téléphone. Avec un compte, le serveur garde la liste de vos appareils pour vous alerter, jamais le mot de passe de votre box en clair : il est chiffré pour vos seuls appareils.',
  },
];
