// Base64 decoding without atob or Buffer, which Hermes does not promise.
// The token is RFC 4648 base64url without padding, decoded strictly as the
// hub's Go code does (base64.RawURLEncoding.Strict()): any '=', foreign
// character or non-zero trailing bit is refused. The public key is standard
// base64 with padding, as GET /licensing/v1/public-key serves it.

const STD = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
const URL = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';

function table(alphabet: string): Int16Array {
  const t = new Int16Array(128).fill(-1);
  for (let i = 0; i < alphabet.length; i++) t[alphabet.charCodeAt(i)] = i;
  return t;
}
const STD_T = table(STD);
const URL_T = table(URL);

function decode(s: string, t: Int16Array): Uint8Array | null {
  if (s.length % 4 === 1) return null;
  const out = new Uint8Array(Math.floor((s.length * 3) / 4));
  let acc = 0;
  let bits = 0;
  let n = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    const v = c < 128 ? t[c] : -1;
    if (v < 0) return null;
    acc = (acc << 6) | v;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      out[n++] = (acc >> bits) & 0xff;
    }
  }
  // Strict: the bits left over must be zero.
  if (bits > 0 && (acc & ((1 << bits) - 1)) !== 0) return null;
  return out.subarray(0, n);
}

/** Unpadded base64url, strictly; null when it is not. */
export function decodeBase64Url(s: string): Uint8Array | null {
  return decode(s, URL_T);
}

/** Standard base64, padded or not; null when it is not. */
export function decodeBase64(s: string): Uint8Array | null {
  const trimmed = s.trim();
  const body = trimmed.replace(/={1,2}$/, '');
  if (trimmed.length !== body.length && trimmed.length % 4 !== 0) return null;
  return decode(body, STD_T);
}

/** UTF-8 bytes to a string; null on an invalid sequence. */
export function utf8(bytes: Uint8Array): string | null {
  let out = '';
  for (let i = 0; i < bytes.length; ) {
    const b = bytes[i++];
    let cp: number;
    let extra: number;
    if (b < 0x80) {
      cp = b;
      extra = 0;
    } else if (b >= 0xc2 && b < 0xe0) {
      cp = b & 0x1f;
      extra = 1;
    } else if (b >= 0xe0 && b < 0xf0) {
      cp = b & 0x0f;
      extra = 2;
    } else if (b >= 0xf0 && b < 0xf5) {
      cp = b & 0x07;
      extra = 3;
    } else return null;
    for (let k = 0; k < extra; k++) {
      const c = bytes[i++];
      if (c === undefined || (c & 0xc0) !== 0x80) return null;
      cp = (cp << 6) | (c & 0x3f);
    }
    if ((extra === 2 && cp < 0x800) || (extra === 3 && (cp < 0x10000 || cp > 0x10ffff)) || (cp >= 0xd800 && cp < 0xe000)) {
      return null;
    }
    out += String.fromCodePoint(cp);
  }
  return out;
}
