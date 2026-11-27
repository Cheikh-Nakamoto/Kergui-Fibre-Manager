import { fr } from '../i18n/fr';

// One vocabulary for one decision (#118, audit H2, RFC-007 D1, R14, R15),
// before the closed Wi-Fi (#108) lands on it: a device is « Autorisé » or
// « À vérifier », and the household « Autorise » or « Bloque » it. Never
// approuver, accepter, inconnu, nor the box's own jargon.
const entries = Object.entries(fr) as [keyof typeof fr, string][];
const matching = (re: RegExp, allowed: readonly string[] = []) =>
  entries.filter(([k, v]) => re.test(v) && !allowed.includes(k)).map(([k]) => k);

test('no French string approves a device (« approuver », « approbation »)', () => {
  expect(matching(/approuv|approb/i)).toEqual([]);
});

test('no French string accepts a device: « accepter » is for a text, an invite or the box', () => {
  expect(matching(/accepter cet appareil|acceptation/i)).toEqual([]);
  // The ones left say « accept » of something else than a device's access.
  const allowed = [
    'relayDisclosure.accept', // the relay's disclosure
    'household.inviteSent', // an invitation
    'hubError.COMPLAINT_INVALID', // a message to the team
    'error.listFull', // the box takes N devices
    'error.listFullUnknown',
    'remote.error.box', // the box did not take a command (#119)
  ];
  expect(matching(/accept/i, allowed)).toEqual([]);
});

test('no device is « inconnu »: Android’s own permission name is quoted as is', () => {
  // « Installer des applications inconnues » is Android's wording (R14).
  expect(matching(/inconnu/i, ['update.permissionBody'])).toEqual([]);
  expect(fr['update.permissionBody']).toContain('« Installer des applications inconnues »');
});

// Customer text is plain French (#112, RFC-007 D1): Autoriser / À vérifier /
// Bloquer / Débloquer, never the box's own jargon.
const BANNED_WORDS = [/filtrage\s+mac/i, /liste\s+blanche/i, /liste\s+noire/i, /\bACL\b/i, /white\s*list/i, /black\s*list/i];

test('no French string uses a banned word (#115)', () => {
  expect(entries.filter(([, v]) => BANNED_WORDS.some((w) => w.test(v))).map(([k]) => k)).toEqual([]);
});

test('« plainte » sounds like the courts: the customer makes a « demande » (audit M8)', () => {
  expect(matching(/plainte/i)).toEqual([]);
  expect(fr['report.sent']).toMatch(/^Demande n° \{number\} reçue/);
  expect(fr['inbox.replyTitle']).toBe('Réponse à votre demande n° {number}');
});

test('the device states and the one action', () => {
  expect(fr['device.approve']).toBe('Autoriser');
  expect(fr['device.approved']).toBe('Autorisé');
  expect(fr['device.notApproved']).toBe('À vérifier');
  expect(fr['device.pendingState']).toBe('· sera refusé à {time} si personne ne l’autorise');
  expect(fr['device.renamed']).toBe('Nommé et autorisé ✓');
  // The notification's button, the app's and the Android watch service's
  // (approveText), says the same as its text.
  expect(fr['alerts.approve']).toBe('Autoriser');
  expect(fr['arrivals.approve']).toBe('Autoriser');
  expect(fr['alerts.title']).toBe('Appareil à vérifier sur votre Wi-Fi');
  expect(fr['inbox.approved']).toBe('Autorisé');
});

test('Kergui’s own background permission does not say « autoriser » (R14)', () => {
  expect(fr['watch.battery']).toBe('Laisser Kergui surveiller quand l’app est fermée');
});

test('two « Changer de box » become two different words (audit M4)', () => {
  expect(fr['settings.signOut']).toBe('Oublier cette box');
  expect(fr['pro.transferTitle']).toBe('Ma box a été remplacée');
  expect(matching(/changer de box/i)).toEqual([]);
});

test('the first tab counts what it lists (audit M5)', () => {
  expect(fr['devices.filterConnected']).toBe('Appareils ({count})');
});

test('this phone is out of the list, and says why (audit L4)', () => {
  expect(fr['devices.hiddenSelf']).toBe('Ce téléphone n’est pas dans la liste : Kergui ne le bloque jamais.');
  expect(matching(/masqué/i, ['wifi.hidden'])).toEqual([]);
});

test('the support category is « Mon compte », and no key or text says « paiement » (audit L2)', () => {
  expect(fr['report.category.compte']).toBe('Mon compte');
  expect(Object.keys(fr).filter((k) => /paiement/i.test(k))).toEqual([]);
  expect(matching(/paiement/i)).toEqual([]);
});

// Relays, household and recovery in plain words (#124, audit M2): no
// cryptography. A word is matched whole (« commander », the verb, is fine).
const word = (w: string) => new RegExp(`(^|[^\\p{L}])${w}([^\\p{L}]|$)`, 'iu');

test('nobody « révoque »: the owner « retire l’accès » (audit M2)', () => {
  expect(matching(/révoqu/i)).toEqual([]);
  expect(fr['delegation.revoke']).toBe('Retirer l’accès');
});

test('no « clé » in what the household reads (audit M2)', () => {
  expect(matching(word('clés?'))).toEqual([]);
  expect(fr['delegation.you.other_owner']).toBe('Kergui ne reconnaît pas le propriétaire. Demandez-lui d’ouvrir Kergui.');
});

test('no « commande »: the household makes « demandes » and « actions » (audit M2)', () => {
  expect(matching(word('commandes?'))).toEqual([]);
});

// Every sentence that names a screen has a button that goes there (#124,
// audit M3). The written path stays for the other phone only.
test('no written path to a screen, but for the other phone', () => {
  const allowed = [
    'tabs.settings', // the tab's own name
    'features.title', // the screen's own title
    'relayPhone.introHow', // the steps on the owner's phone, read on the relay phone
    'hubError.COMPLAINT_CLOSED', // outside the relay, household and recovery screens
    'error.pin',
  ];
  expect(matching(/>|Plus de fonctionnalités|Réglages/, allowed)).toEqual([]);
});
