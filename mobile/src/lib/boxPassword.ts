// The new box's admin password rule (#93), the same as the core's
// domain.ValidateBoxPassword (internal/domain/boxpassword.go), itself the
// ZTE F6600P's checkPassStrong: at least 8 printable ASCII characters, a
// lower-case letter, an upper-case letter and a digit. The app checks it
// before asking the core; the box stays the judge and may still refuse.

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
