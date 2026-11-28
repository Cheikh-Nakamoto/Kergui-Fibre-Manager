// The site speaks the app's words (#130, after #118 and #124): a device is
// « Autorisé » or « À vérifier », the household « Autorise » or « Bloque »
// it, and the customer makes a « demande ». The list mirrors
// mobile/src/__tests__/vocabulary.test.ts. The back-office (/admin) keeps the
// team's own words (« plainte », « révoquer », « commande » NabooPay).
// Run: node --test lib/vocabulary.test.ts
import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { join, relative, sep } from 'node:path';
import { test } from 'node:test';

const root = join(import.meta.dirname, '..');

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return ['node_modules', '.next', 'out'].includes(e.name) ? [] : sources(p);
    return /\.(ts|tsx)$/.test(e.name) && !e.name.endsWith('.test.ts') ? [p] : [];
  });
}

const files = ['app', 'lib'].flatMap((d) => sources(join(root, d))).map((p) => ({
  path: relative(root, p).split(sep).join('/'),
  lines: readFileSync(p, 'utf8').split('\n'),
}));
const household = files.filter((f) => !f.path.startsWith('app/admin/'));

type Allowed = { path: string; line: string; why: string };

/** Every line that matches, but for the closed list of exceptions; an exception no line uses fails too. */
function offending(scope: typeof files, re: RegExp, allowed: readonly Allowed[] = []): string[] {
  const used = new Set<Allowed>();
  const out: string[] = [];
  for (const f of scope) {
    f.lines.forEach((l, i) => {
      if (!re.test(l)) return;
      const ok = allowed.find((a) => a.path === f.path && l.includes(a.line));
      if (ok) used.add(ok);
      else out.push(`${f.path}:${i + 1}: ${l.trim()}`);
    });
  }
  for (const a of allowed) if (!used.has(a)) out.push(`unused exception: ${a.path} « ${a.line} »`);
  return out;
}

// A word is matched whole (« commander », « clef de voûte » are not « commande », « clé »).
const word = (w: string) => new RegExp(`(^|[^\\p{L}])${w}([^\\p{L}]|$)`, 'iu');

test('nobody approves a device on the site, back-office included (« approuver », « approbation »)', () => {
  assert.deepEqual(offending(files, /approuv|approb/i), []);
  assert.deepEqual(offending(files, /non approuvé/i), []);
});

test('no device is « inconnu »: it is « à vérifier »', () => {
  assert.deepEqual(offending(files, /appareil inconnu/i), []);
  assert.deepEqual(
    offending(files, /inconnu/i, [
      { path: 'app/admin/actions.ts', line: "'Action inconnue.'", why: 'a back-office form sent a wrong action' },
    ]),
    [],
  );
});

test('no device is « accepté »: « accept » is for a message or an HTTP header', () => {
  assert.deepEqual(
    offending(household, /accept/i, [
      { path: 'app/compte/actions.ts', line: 'Le message n’a pas été accepté', why: 'a message to the team, as in the app' },
      { path: 'app/_components/download.tsx', line: 'acceptedAnswer', why: 'schema.org FAQ markup' },
      { path: 'lib/download.ts', line: "Accept: 'application/json'", why: 'HTTP header' },
    ]),
    [],
  );
});

test('no box jargon in what the household reads (#115)', () => {
  assert.deepEqual(offending(household, /filtrage\s+mac|liste\s+blanche|liste\s+noire|\bACL\b|white\s*list|black\s*list/i), []);
});

test('« plainte » sounds like the courts: the customer makes a « demande » (audit M8)', () => {
  assert.deepEqual(
    offending(household, /plainte/i, [
      { path: 'lib/format.ts', line: "'Réponse à une plainte'", why: 'back-office journal label' },
      { path: 'lib/format.ts', line: "'Plainte close'", why: 'back-office journal label' },
      { path: 'lib/format.ts', line: "'Numéros admin (plaintes) changés'", why: 'back-office journal label' },
      { path: 'lib/format.ts', line: "complaint: 'Plainte'", why: 'back-office journal target' },
      { path: 'lib/hub.ts', line: '« plainte n° 12 »', why: 'a comment on the hub type' },
    ]),
    [],
  );
});

test('no « révoquer », « clé » nor « commande » for the household (audit M2)', () => {
  assert.deepEqual(
    offending(household, /révoqu/i, [
      { path: 'lib/format.ts', line: "'Sessions révoquées'", why: 'back-office journal label' },
    ]),
    [],
  );
  assert.deepEqual(offending(household, word('clés?')), []);
  assert.deepEqual(offending(household, word('commandes?')), []);
});

test('the first tab counts what it lists: « Appareils (N) », not « Connectés (N) » (audit M5)', () => {
  assert.deepEqual(offending(household, /Connectés \(/), []);
});

const phones = files.find((f) => f.path === 'app/_components/phones.tsx')!.lines.join('\n');
const sections = files.find((f) => f.path === 'app/_components/sections.tsx')!.lines.join('\n');

test('the drawn app screens show the app’s words', () => {
  const missing = (text: string, wanted: string[]) => wanted.filter((w) => !text.includes(w));
  assert.deepEqual(
    missing(phones, [
      '>Appareils (6)<', // devices.filterConnected
      '>Autorisé<', // device.approved
      'un appareil à vérifier vient de rejoindre votre Wi-Fi', // the hub's WhatsApp alert
      'que vous n’avez pas autorisés', // features.autoRejectHint
    ]),
    [],
  );
  assert.deepEqual(
    missing(sections, [
      'Appareil à vérifier sur votre Wi-Fi', // alerts.title
      '<span>Autoriser</span>', // alerts.approve, the notification's one button
      'OK l’autorise', // one answer, one meaning on WhatsApp (audit C2): a number blocks, « OK » authorises
    ]),
    [],
  );
});

test('the drawn app screens show no price and no payment (V1 rule: the app shows none)', () => {
  const re = /paiement|payer|prix|tarif|FCFA|abonnement|\bpass\b|\d[\s ]?F\b/i;
  assert.deepEqual(phones.split('\n').filter((l) => re.test(l)), []);
});
