/**
 * The address the connection screen is filled with: the box's own private
 * address (the ZTE F6600P's, and the core's default router).
 */
export const DEFAULT_BOX_ADDRESS = '192.168.1.1';

/** An RFC 1918 IPv4 address: 10/8, 172.16/12, 192.168/16. */
export function isPrivateIPv4(host: string): boolean {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(host);
  if (!m) return false;
  const [a, b, c, d] = m.slice(1).map(Number);
  if ([a, b, c, d].some((n) => n > 255)) return false;
  return a === 10 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168);
}

/**
 * The box's default address over https, on its own port, and nothing else:
 * no other port, no user part, no path. Parsed by hand: React Native's URL
 * is not the browser's.
 */
export function isDefaultBoxUrl(url: string): boolean {
  const m = /^https:\/\/([^/:@?#\s]+)(?::(\d+))?\/?$/i.exec(url.trim());
  if (!m) return false;
  const [, host, port] = m;
  return host === DEFAULT_BOX_ADDRESS && isPrivateIPv4(host) && (port === undefined || port === '443');
}

/**
 * What the connection screen does with the certificate a box presents
 * (#122, audit H6):
 * - `known`: the one Kergui remembers for this address. Go on.
 * - `changed`: not the one it remembers. Ask, never go on in silence.
 * - `firstDefault`: nothing remembered yet, on the default private
 *   address. Keep it without a question (trust on first use); the journal
 *   says its fingerprint.
 * - `ask`: nothing remembered yet, on any other address (typed by hand),
 *   or what Kergui remembers could not be read. Ask.
 *
 * `known` is the remembered fingerprint, null when there is none, and
 * undefined when it could not be read.
 */
export type TrustDecision = 'known' | 'changed' | 'firstDefault' | 'ask';

export function trustDecision(url: string, fingerprint: string, known: string | null | undefined): TrustDecision {
  if (known === undefined) return 'ask';
  if (known) return known.toLowerCase() === fingerprint.toLowerCase() ? 'known' : 'changed';
  return isDefaultBoxUrl(url) ? 'firstDefault' : 'ask';
}
