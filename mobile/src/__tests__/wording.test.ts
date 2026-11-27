import { fr } from '../i18n/fr';

// The Play app has no payment surface (RFC-006, decision of 2026-11-21): no
// string in the app may talk about paying, prices, offers or a website to
// pay on. scripts/check-play-bundle.sh checks the built bundle the same way.
const PAYMENT_WORDING = /NabooPay|Wave|Orange Money|FCFA|F CFA|XOF|prix|abonne|payer|acheter|achat|s’abonner|site web|https?:\/\/[^"]*buy/i;

test('no French string talks about paying', () => {
  const bad = Object.entries(fr).filter(([, v]) => PAYMENT_WORDING.test(v));
  expect(bad).toEqual([]);
});

test('no string sells Pro as an offer', () => {
  const removed = Object.keys(fr).filter((k) => /^play\.|^pro\.(pitch|basicFeatures|maxFeatures|needs)$/.test(k));
  expect(removed).toEqual([]);
  const pro = Object.entries(fr).filter(([, v]) => /\bPro\b|offre|essai|formule/i.test(v));
  expect(pro).toEqual([]);
});

test('the neutral lock message', () => {
  expect(fr['pro.locked']).toBe('Cette fonctionnalité n’est pas disponible pour votre compte.');
});

test('no string names the operator or the box maker (#37, Play impersonation policy)', () => {
  const brands = Object.entries(fr).filter(([, v]) => /\b(ZTE|Orange|Livebox|Fiberbox|Sonatel|Huawei)\b/i.test(v));
  expect(brands).toEqual([]);
  expect(fr['welcome.subtitle']).toContain('votre box fibre');
});

test('account deletion speaks of no purchase or refund (#37)', () => {
  const deletion = Object.entries(fr).filter(([k]) => k.startsWith('account.delete'));
  expect(deletion.length).toBeGreaterThan(0);
  for (const [, v] of deletion) expect(v).not.toMatch(/rembours|achat|acheté|prix|payé|abonne/i);
});

// Customer text is plain French (#112, RFC-007 D1): Autoriser / À vérifier /
// Bloquer / Débloquer, never the box's own jargon.
const BANNED_WORDS = [/filtrage\s+mac/i, /liste\s+blanche/i, /liste\s+noire/i, /\bACL\b/i, /white\s*list/i, /black\s*list/i];

test('no French string uses a banned word (#115)', () => {
  const bad = Object.entries(fr).filter(([, v]) => BANNED_WORDS.some((w) => w.test(v)));
  expect(bad).toEqual([]);
});

test('away, the line says blocks are managed at home (#115)', () => {
  expect(fr['remote.unblockAllLocal']).toBe('Les blocages se règlent à la maison, sur le Wi-Fi de la box.');
});
