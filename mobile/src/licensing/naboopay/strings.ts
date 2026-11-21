// The NabooPay flavor's own words. They live here, not in src/i18n/fr.ts,
// because this module is only bundled into the naboopay build (see
// babel.config.js): the play build must carry no NabooPay wording or price
// (RFC-006, Two channels).

const terms: Record<string, string> = { '1m': '1 mois', '3m': '3 mois', '12m': '12 mois' };

export const naboo = {
  trial: (days: number) => `Essayer Max gratuitement pendant ${days} jours`,
  trialHint: 'Une fois par numéro et une fois par box. Sans paiement.',
  passesTitle: 'Acheter un pass',
  passesHint: 'Paiement par Wave ou Orange Money. Pas de prélèvement automatique : le pass s’arrête tout seul à la fin.',
  term: (term: string, days: number) => terms[term] ?? `${days} jours`,
  pass: (tier: string, term: string, days: number) => `${tier} · ${naboo.term(term, days)}`,
  price: (xof: number) => `${String(Math.round(xof)).replace(/\B(?=(\d{3})+(?!\d))/g, ' ')} F CFA`,
  pay: 'Payer',
  payConfirm: (pass: string, price: string) => `Acheter le pass ${pass} pour ${price} ? La page de paiement s’ouvre dans le navigateur.`,
  waiting:
    'Paiement terminé ? Votre pass s’active dans quelques instants. Touchez « Actualiser » s’il n’apparaît pas.',
  maxActive: 'Max est actif sur cette box : les passes Basic seront disponibles à sa fin.',
  upgradeHint: 'Passer de Basic à Max convertit les jours Basic restants en jours Max.',
  loadFailed: 'Impossible de charger les offres.',
};
