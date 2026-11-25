// The new box's rule (#93), the same as the core's ValidateBoxPassword
// (internal/domain/boxpassword.go): at least 8 printable ASCII characters,
// a lower-case letter, an upper-case letter and a digit. The gauge is only
// advisory; the rule is what blocks.
import { boxPasswordMissing, boxPasswordStrength } from '../lib/boxPassword';

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
