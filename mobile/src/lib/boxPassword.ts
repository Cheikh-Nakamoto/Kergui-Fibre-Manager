// The new box's admin password rule (#93), the same as the core's
// domain.ValidateBoxPassword (internal/domain/boxpassword.go), itself the
// ZTE F6600P's checkPassStrong: at least 8 printable ASCII characters, a
// lower-case letter, an upper-case letter and a digit. The app checks it
// before asking the core; the box stays the judge and may still refuse.
import * as Crypto from 'expo-crypto';

export const BOX_PASSWORD_MIN_LENGTH = 8;

/** A part of the rule a password misses; 'ascii' alone when it has a character the box refuses. */
export type BoxPasswordRule = 'length' | 'lower' | 'upper' | 'digit' | 'ascii';
export type BoxPasswordStrength = 'weak' | 'medium' | 'strong';

/** The parts of the rule the password misses, in the order the screen lists them; [] when it meets it. */
export function boxPasswordMissing(p: string): BoxPasswordRule[] {
  let lower = false;
  let upper = false;
  let digit = false;
  for (let i = 0; i < p.length; i++) {
    const c = p.charCodeAt(i);
    // An accent, a tab or an emoji: the box's form refuses it outright.
    if (c < 0x20 || c > 0x7e) return ['ascii'];
    if (c >= 0x61 && c <= 0x7a) lower = true;
    else if (c >= 0x41 && c <= 0x5a) upper = true;
    else if (c >= 0x30 && c <= 0x39) digit = true;
  }
  const missing: BoxPasswordRule[] = [];
  if (p.length < BOX_PASSWORD_MIN_LENGTH) missing.push('length');
  if (!lower) missing.push('lower');
  if (!upper) missing.push('upper');
  if (!digit) missing.push('digit');
  return missing;
}

/**
 * The advisory gauge: weak until the rule is met, then medium, and strong
 * with 12 characters or more, or a symbol. Only the rule blocks.
 */
export function boxPasswordStrength(p: string): BoxPasswordStrength {
  if (boxPasswordMissing(p).length > 0) return 'weak';
  const symbol = /[^A-Za-z0-9]/.test(p);
  return p.length >= 12 || symbol ? 'strong' : 'medium';
}

// --- The suggested password (#116) ------------------------------------------
// The default on a new box: three simple French words and four digits, such as
// « Mangue-Vache-Harpe-4729 », easy to read aloud and to write down, 31
// characters at most. The words have no i, l or o and the digits no 0, 1, 5
// or 8, so no character can be taken for another (0/O, 1/l/I, 5/S, 8/B).
// About 28 bits from a secure source: the box is reached only from the
// house's network, but anyone on its Wi-Fi, a visitor included, can try.

/** Plain lower-case ASCII words, no accent, nothing a reader could find rude. */
export const SUGGESTION_WORDS: readonly string[] = [
  'mangue', 'banane', 'sucre', 'arbre', 'pagne', 'tasse', 'vache', 'chameau', 'herbe',
  'verre', 'carte', 'crabe', 'ruche', 'nuage', 'barque', 'harpe', 'cabane', 'avenue',
  'dune', 'gare', 'menthe', 'ananas', 'papaye', 'datte', 'natte', 'canard', 'renard',
  'autruche', 'cactus', 'tente', 'ferme', 'vague', 'prune', 'hamac', 'parc', 'trace',
  'panda', 'chance', 'danse', 'samba', 'puma', 'jaguar', 'kayak', 'cygne', 'rame',
  'nappe', 'vent', 'sauce', 'caravane', 'safran', 'muscade', 'savane', 'marche', 'case',
];

export const SUGGESTION_DIGITS = '234679';

/** n bytes from a cryptographically secure source. */
export type RandomBytes = (n: number) => Uint8Array;

/** A uniform index below n (n <= 256), by rejection: no modulo bias. */
function uniform(n: number, random: RandomBytes): number {
  const limit = 256 - (256 % n);
  for (;;) {
    const b = random(1)[0];
    if (b < limit) return b % n;
  }
}

const capitalised = (w: string) => w[0].toUpperCase() + w.slice(1);

/**
 * A new suggestion that always meets the box rule: capitals and lower case
 * from the words, a digit from the end. Never logged: only the screen shows it.
 */
export function suggestBoxPassword(random: RandomBytes = Crypto.getRandomBytes): string {
  const words = [0, 1, 2].map(() => capitalised(SUGGESTION_WORDS[uniform(SUGGESTION_WORDS.length, random)]));
  const digits = [0, 1, 2, 3].map(() => SUGGESTION_DIGITS[uniform(SUGGESTION_DIGITS.length, random)]).join('');
  return `${words.join('-')}-${digits}`;
}
