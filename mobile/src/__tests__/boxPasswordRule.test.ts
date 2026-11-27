/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The new box's rule (#93), the same as the core's ValidateBoxPassword
// (internal/domain/boxpassword.go): at least 8 printable ASCII characters,
// a lower-case letter, an upper-case letter and a digit. The gauge is only
// advisory; the rule is what blocks.
// The suggested password (#116) is words and digits, easy to read aloud and
// to write down, drawn from a secure source, and always meets the rule.
import { randomBytes } from 'crypto';

jest.mock('expo-crypto', () => ({
  getRandomBytes: (n: number) => new Uint8Array(jest.requireActual('crypto').randomBytes(n)),
}));

import {
  BOX_PASSWORD_MIN_LENGTH,
  SUGGESTION_DIGITS,
  SUGGESTION_WORDS,
  boxPasswordMissing,
  boxPasswordStrength,
  suggestBoxPassword,
} from '../lib/boxPassword';

test('a password that meets the box rule misses nothing', () => {
  expect(boxPasswordMissing('Kergui2026')).toEqual([]);
  expect(boxPasswordMissing('aB3defgh')).toEqual([]);
});

test('says each part of the rule a password misses', () => {
  expect(boxPasswordMissing('')).toEqual(['length', 'lower', 'upper', 'digit']);
  expect(boxPasswordMissing('abcdefgh')).toEqual(['upper', 'digit']);
  expect(boxPasswordMissing('ABCDEFG1')).toEqual(['lower']);
  expect(boxPasswordMissing('Abc1')).toEqual(['length']);
});

test('accents and other non-ASCII characters are refused, as the box does', () => {
  expect(boxPasswordMissing('Sénégal2026')).toEqual(['ascii']);
  expect(boxPasswordMissing('Kergui 2026')).toEqual([]); // a space is printable ASCII
});

test('the gauge: weak until the rule is met, then medium, then strong', () => {
  expect(boxPasswordStrength('')).toBe('weak');
  expect(boxPasswordStrength('abcdefgh')).toBe('weak');
  expect(boxPasswordStrength('Kergui26')).toBe('medium');
  expect(boxPasswordStrength('Kergui2026Dakar')).toBe('strong');
  expect(boxPasswordStrength('Kerg!2026x')).toBe('strong');
});

// --- The suggested password (#116) ------------------------------------------

/** Characters a reader can take for another: 0/O/o, 1/l/I/i. */
const LOOK_ALIKES = /[0Oo1lIi|]/;
const DRAWS = 10_000;

test('every suggested password meets the box rule, on many draws', () => {
  for (let i = 0; i < DRAWS; i++) {
    const p = suggestBoxPassword();
    expect(boxPasswordMissing(p)).toEqual([]);
    expect(p.length).toBeGreaterThanOrEqual(BOX_PASSWORD_MIN_LENGTH);
    expect(p.length).toBeLessThanOrEqual(32);
  }
});

test('a suggestion is three words and four digits, without look-alike characters', () => {
  const words = new Set<string>();
  for (let i = 0; i < DRAWS; i++) {
    const p = suggestBoxPassword();
    expect(p).toMatch(/^[A-Z][a-z]+-[A-Z][a-z]+-[A-Z][a-z]+-[2-9]{4}$/);
    expect(p).not.toMatch(LOOK_ALIKES);
    for (const w of p.split('-').slice(0, 3)) words.add(w.toLowerCase());
  }
  // The whole list is used: nothing narrows the draw.
  expect([...words].sort()).toEqual([...SUGGESTION_WORDS].sort());
});

test('the word list is plain lower-case ASCII, unique, and safe to read aloud', () => {
  expect(SUGGESTION_WORDS.length).toBeGreaterThanOrEqual(48);
  expect(new Set(SUGGESTION_WORDS).size).toBe(SUGGESTION_WORDS.length);
  for (const w of SUGGESTION_WORDS) {
    expect(w).toMatch(/^[a-z]{4,8}$/);
    expect(w).not.toMatch(LOOK_ALIKES);
  }
  expect(SUGGESTION_DIGITS).not.toMatch(LOOK_ALIKES);
  expect(SUGGESTION_DIGITS).not.toMatch(/[58]/); // 5/S, 8/B
});

test('the draw uses the given secure source, without modulo bias', () => {
  // All zeros: the first word, three times, and the first digit four times.
  const zeros = (n: number) => new Uint8Array(n);
  const first = SUGGESTION_WORDS[0];
  const cap = first[0].toUpperCase() + first.slice(1);
  expect(suggestBoxPassword(zeros)).toBe(`${cap}-${cap}-${cap}-${SUGGESTION_DIGITS[0].repeat(4)}`);

  // A byte at or above the last whole multiple of the list's size is drawn
  // again, not folded onto a word: 255 would otherwise pick word 255 % n.
  const n = SUGGESTION_WORDS.length;
  expect(256 % n).not.toBe(0);
  const queue = [255];
  const scripted = (k: number) => Uint8Array.from({ length: k }, () => queue.shift() ?? 0);
  expect(suggestBoxPassword(scripted).startsWith(`${cap}-`)).toBe(true);
});

test('two suggestions differ', () => {
  expect(suggestBoxPassword()).not.toBe(suggestBoxPassword());
  // Node's source, passed in, works the same.
  expect(boxPasswordMissing(suggestBoxPassword((n) => new Uint8Array(randomBytes(n))))).toEqual([]);
});
