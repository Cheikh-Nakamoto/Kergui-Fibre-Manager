import { fr } from '../i18n/fr';

// A device in words (#121, audit H5): what it probably is, its maker, and a
// title the household can read. Never its MAC nor its IP: those go to the
// sheet's « Détails techniques ».
//
// The guess is the hub's (internal/hub/alerts/usecase/name.go, #114): the
// same words, the same order, the same matching, so the app's list and the
// household's WhatsApp alerts say the same « téléphone Samsung ». Both tests
// read the same cases (internal/hub/alerts/usecase/testdata/device_names.json).
// Change a word in one, change it in the other.
//
// There is no maker table in the app: the core's OUI table is empty, and
// phones mostly use private addresses anyway. The guess reads the name the box
// reported; the vendor helps when the core one day fills it in.
//
// This file imports fr only, not the i18n module: format.ts names devices
// with it, and i18n imports format.ts.

/** What a device probably is. « device » is « we cannot tell ». */
export type DeviceKind = 'phone' | 'tablet' | 'computer' | 'tv' | 'printer' | 'console' | 'watch' | 'device';

export type DeviceGuess = {
  kind: DeviceKind;
  /** The brand, « Samsung »; '' when the name does not give it away. */
  maker: string;
  /** The French noun without its article: « téléphone Samsung », « iPhone », « appareil ». */
  noun: string;
  /** Its gender, for « Une télévision ». */
  fem: boolean;
};

const FEMININE: ReadonlySet<DeviceKind> = new Set(['tablet', 'tv', 'printer', 'console', 'watch']);

// Words that name a kind outright; they win over a maker's usual kind, so a
// « Samsung-TV » is a television. The order matters: « androidtv » is a TV
// before it is an Android phone.
const KIND_WORDS: { words: string[]; kind: DeviceKind }[] = [
  { words: ['tv', 'smarttv', 'androidtv', 'appletv', 'firetv', 'webos', 'lgwebostv', 'bravia', 'roku', 'tizen'], kind: 'tv' },
  { words: ['playstation', 'ps4', 'ps5', 'xbox', 'nintendo'], kind: 'console' },
  { words: ['printer', 'epson', 'canon', 'brother'], kind: 'printer' },
  { words: ['watch'], kind: 'watch' },
  { words: ['tab', 'tablet', 'galaxytab', 'ipad', 'mediapad', 'matepad'], kind: 'tablet' },
  {
    words: ['desktop', 'laptop', 'pc', 'macbook', 'imac', 'mac', 'thinkpad', 'ideapad', 'vivobook', 'zenbook', 'windows'],
    kind: 'computer',
  },
  { words: ['phone', 'iphone', 'android', 'mobile'], kind: 'phone' },
];

// The brands a reported name gives away, with the kind they usually are. The
// phones sold in Senegal come first.
const MAKERS: { words: string[]; name: string; kind: DeviceKind }[] = [
  { words: ['tecno'], name: 'Tecno', kind: 'phone' },
  { words: ['infinix'], name: 'Infinix', kind: 'phone' },
  { words: ['itel'], name: 'itel', kind: 'phone' },
  { words: ['samsung', 'galaxy', 'sm'], name: 'Samsung', kind: 'phone' },
  { words: ['iphone', 'ipad', 'macbook', 'imac', 'mac', 'apple', 'appletv'], name: 'Apple', kind: 'device' },
  { words: ['xiaomi', 'redmi', 'poco', 'mi'], name: 'Xiaomi', kind: 'phone' },
  { words: ['huawei', 'mediapad', 'matepad'], name: 'Huawei', kind: 'phone' },
  { words: ['honor'], name: 'Honor', kind: 'phone' },
  { words: ['oppo'], name: 'Oppo', kind: 'phone' },
  { words: ['realme'], name: 'Realme', kind: 'phone' },
  { words: ['oneplus'], name: 'OnePlus', kind: 'phone' },
  { words: ['vivobook', 'zenbook', 'asus'], name: 'Asus', kind: 'computer' },
  { words: ['vivo'], name: 'Vivo', kind: 'phone' },
  { words: ['nokia'], name: 'Nokia', kind: 'phone' },
  { words: ['motorola', 'moto'], name: 'Motorola', kind: 'phone' },
  { words: ['pixel'], name: 'Google', kind: 'phone' },
  { words: ['lenovo', 'thinkpad', 'ideapad'], name: 'Lenovo', kind: 'computer' },
  { words: ['hp'], name: 'HP', kind: 'computer' },
  { words: ['dell'], name: 'Dell', kind: 'computer' },
  { words: ['acer'], name: 'Acer', kind: 'computer' },
  { words: ['lg', 'lgwebostv', 'webos'], name: 'LG', kind: 'device' },
  { words: ['playstation', 'ps4', 'ps5', 'sony', 'bravia'], name: 'Sony', kind: 'device' },
  { words: ['xbox'], name: 'Microsoft', kind: 'console' },
  { words: ['nintendo'], name: 'Nintendo', kind: 'console' },
  { words: ['epson'], name: 'Epson', kind: 'printer' },
  { words: ['canon'], name: 'Canon', kind: 'printer' },
  { words: ['brother'], name: 'Brother', kind: 'printer' },
  { words: ['android'], name: 'Android', kind: 'phone' },
];

/** A reported name in lower-case words: « TECNO-POP-10 » is tecno, pop, 10. */
function tokens(name: string | undefined): string[] {
  return (name ?? '').toLowerCase().split(/[^\p{L}\p{N}]+/u).filter(Boolean);
}

// A short word must be the whole token (« mi », « hp », « tv »); a longer one
// may start it (« galaxys21 », « epson3c2af1 »), and a long one may be
// anywhere in it (« awasiphone »).
function matchesAny(toks: string[], words: string[]): boolean {
  return toks.some((t) =>
    words.some((w) => t === w || (w.length >= 4 && t.startsWith(w)) || (w.length >= 6 && t.includes(w))),
  );
}

const noun = (kind: DeviceKind) => fr[`deviceKind.${kind}`];

/**
 * What a device probably is, from the name the box reported and, for the
 * maker only, the vendor of its address (empty today).
 */
export function guessDevice(hostname?: string, vendor?: string): DeviceGuess {
  const host = tokens(hostname);
  let kind: DeviceKind = 'device';
  let known = false;
  const named = KIND_WORDS.find((k) => matchesAny(host, k.words));
  if (named) {
    kind = named.kind;
    known = true;
  }
  const brand = MAKERS.find((m) => matchesAny(host, m.words)) ?? MAKERS.find((m) => matchesAny(tokens(vendor), m.words));
  const maker = brand?.name ?? '';
  if (brand && !known) kind = brand.kind;

  if (matchesAny(host, ['chromecast'])) return { kind: 'tv', maker: 'Google', noun: 'Chromecast', fem: false };
  if (maker === 'Apple' && kind === 'phone') return { kind, maker, noun: 'iPhone', fem: false };
  if (maker === 'Apple' && kind === 'tablet') return { kind, maker, noun: 'iPad', fem: false };
  return { kind, maker, noun: maker ? `${noun(kind)} ${maker}` : noun(kind), fem: FEMININE.has(kind) };
}

const MAC = /^([0-9a-f]{2}[:-]){5}[0-9a-f]{2}$/i;
const IPV4 = /^\d{1,3}(\.\d{1,3}){3}$/;
const NOT_A_NAME = new Set(['localhost', 'unknown', 'null', '(null)', 'none', '*']);

// A word made by a machine: a serial or an address (« 3f2a9c01d4 »,
// « 4f2k9qx », « a125f », a MAC without its colons), not a model (« a12 »,
// « pop », « 10 »).
const isCode = (t: string) =>
  (t.length >= 5 && /\d/.test(t) && /\p{L}/u.test(t)) || /^\d{6,}$/.test(t) || /^[0-9a-f]{12}$/.test(t);

/**
 * Whether the name the box reported reads as a name (« Laptop-Bureau »,
 * « TECNO-POP-10 », « Awa-iPhone ») and not as a machine's (« android-3f2a… »,
 * « DESKTOP-4F2K9QX », a MAC, an IP).
 */
export function isReadableHostname(hostname?: string): boolean {
  const h = (hostname ?? '').trim();
  if (!h || NOT_A_NAME.has(h.toLowerCase()) || MAC.test(h) || IPV4.test(h) || h.includes(':')) return false;
  const toks = tokens(h);
  return toks.length > 0 && !toks.some(isCode);
}

type Named = { CustomName?: string; Hostname?: string; Vendor?: string };

const capitalised = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

/**
 * A device's title, everywhere in the app: the household's name, else the
 * box's when it reads as one, else the guess (« Téléphone Samsung »), else
 * « Appareil sans nom ». Never a MAC nor an IP.
 */
export function deviceTitle(d: Named): string {
  const custom = d.CustomName?.trim();
  if (custom) return custom;
  if (isReadableHostname(d.Hostname)) return d.Hostname!.trim();
  const g = guessDevice(d.Hostname, d.Vendor);
  return g.kind === 'device' && !g.maker ? fr['device.unnamed'] : capitalised(g.noun);
}

/**
 * The same name in a sentence (a notification): « Un téléphone Samsung »,
 * « Une imprimante Epson »; `unnamed` (« Un appareil ») when nothing tells.
 */
export function deviceInSentence(d: Named, unnamed: string): string {
  const custom = d.CustomName?.trim();
  if (custom) return custom;
  if (isReadableHostname(d.Hostname)) return d.Hostname!.trim();
  const g = guessDevice(d.Hostname, d.Vendor);
  if (g.kind === 'device' && !g.maker) return unnamed;
  return fr[g.fem ? 'device.indefiniteFem' : 'device.indefinite'].replace('{noun}', g.noun);
}

const ICONS: Record<DeviceKind, string> = {
  phone: 'cellphone',
  tablet: 'tablet',
  computer: 'laptop',
  tv: 'television',
  printer: 'printer',
  console: 'gamepad-variant',
  watch: 'watch',
  device: 'devices',
};

/**
 * The row's icon for the type of device, and what the screen reader says for
 * it (« Téléphone », « Type d’appareil non reconnu »). The household's own
 * name helps when the box's says nothing (« TV chambre »).
 */
export function deviceIcon(d: Named): { icon: string; label: string } {
  let kind = guessDevice(d.Hostname, d.Vendor).kind;
  if (kind === 'device') kind = guessDevice(d.CustomName).kind;
  return { icon: ICONS[kind], label: kind === 'device' ? fr['deviceKind.unknown'] : capitalised(noun(kind)) };
}

/**
 * Whether the address is locally administered (bit 0x02 of the first octet):
 * a phone's private address, which can change (RFC-007 D10).
 */
export function isPrivateMac(mac: string): boolean {
  const m = /^([0-9a-f]{2})[:-]/i.exec(mac.trim());
  return !!m && (parseInt(m[1], 16) & 0x02) !== 0;
}

const pad = (n: number) => String(n).padStart(2, '0');
const sameDay = (a: Date, b: Date) =>
  a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();

/**
 * When the app last saw a device: « Vu aujourd’hui à 9 h 05 », « Vu hier à
 * 21 h 00 », « Vu le 01/09/2026 », in the phone's time. null without a time.
 */
export function seenLine(iso: string | undefined, now: Date = new Date()): string | null {
  const d = iso ? new Date(iso) : null;
  if (!d || Number.isNaN(d.getTime())) return null;
  const time = `${d.getHours()} h ${pad(d.getMinutes())}`;
  if (sameDay(d, now)) return fr['device.seenToday'].replace('{time}', time);
  const yesterday = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1);
  if (sameDay(d, yesterday)) return fr['device.seenYesterday'].replace('{time}', time);
  return fr['device.seenOn'].replace('{date}', `${pad(d.getDate())}/${pad(d.getMonth() + 1)}/${d.getFullYear()}`);
}

/**
 * The line under a device's title in the list: « Hors ligne », or nothing.
 * The box gives no time of connection, and the core's LastSeen is the time
 * of its last reading of the box, not of the device: so no « Connecté depuis
 * 10 min » nor « Vu hier » here, rather than a made-up one. A cut device
 * says so with its chip.
 */
export function deviceSubline(d: { Connected: boolean }, cut = false): string | null {
  return d.Connected || cut ? null : fr['devices.offline'];
}
