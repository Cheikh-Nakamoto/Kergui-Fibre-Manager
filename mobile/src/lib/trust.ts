import type { Device, DeviceMark, WiFiLockAP, WiFiLockPreview, WiFiLockStatus } from '../core/types';
import { t } from '../i18n';
import type { MessageKey } from '../i18n/fr';
import type { Feature } from '../licensing/gate';
import type { SemanticRole } from '../ui/theme';

// The closed Wi-Fi's rules in the app (#108, RFC-007 D1, D3, D5, D11, D12),
// as the owner arbitrated them (#111). Pure: the screens and their tests
// read the same answers.
//
// The words name the effect (R2): « Ouvert avec mot de passe », « Réservé à
// la maison ». A device is in one of three states (D1): Autorisé, À vérifier
// (neither), Bloqué.

/** A device's state (D1). */
export type Trust = 'authorised' | 'check' | 'blocked';

/** The status card: the box's state in words, never the action (R3). */
export type LockView = {
  state: WiFiLockStatus['state'];
  /** Closed or in an add window: À vérifier refuses the device there (D5). */
  reserved: boolean;
  title: string;
  line: string;
  icon: string;
  role: SemanticRole;
  /** An add window ended and nobody reserved the Wi-Fi again (D4). */
  ended: boolean;
  minutesLeft: number;
};

/** The card's answer; null on a box that cannot reserve its Wi-Fi (D11). */
export function lockView(s: WiFiLockStatus | null | undefined): LockView | null {
  if (!s) return null;
  const base = { state: s.state, ended: false, minutesLeft: s.window?.minutes_left ?? 0 };
  switch (s.state) {
    case 'closed':
      return { ...base, reserved: true, title: t('lock.state.closed'), line: t('lock.state.closedLine'), icon: 'home-lock', role: 'connected' };
    case 'window':
      return {
        ...base,
        reserved: true,
        title: t('lock.state.window', { n: Math.max(1, base.minutesLeft) }),
        line: t('lock.state.windowLine'),
        icon: 'timer-sand',
        role: 'attention',
      };
    case 'busy':
      return { ...base, reserved: false, title: t('lock.state.busy'), line: t('lock.state.busyLine'), icon: 'progress-clock', role: 'neutral' };
    default:
      return {
        ...base,
        reserved: false,
        title: t('lock.state.open'),
        line: t('lock.state.openLine'),
        icon: 'lock-open-variant-outline',
        role: 'neutral',
        // Reserved by the household, open on the box: an add window nobody
        // closed (the box found open some other way is #132's).
        ended: s.intended && !s.found_open,
      };
  }
}

/** Blocked (by the firewall, or « ne peut plus se connecter »), else authorised, else to check. */
export function trustOf(d: Pick<Device, 'Blocked' | 'Approved'>, cut: boolean): Trust {
  if (d.Blocked || cut) return 'blocked';
  return d.Approved ? 'authorised' : 'check';
}

/** The pill under a device's name (R5, R7, R13): colour, icon and word. */
export type Pill = { trust: Trust; role: SemanticRole; icon: string; label: string; sub?: string };

const WEEK = 7 * 86_400_000;
const MONTH = 30 * 86_400_000;

/** When the app last saw the device, in ms; 0 when never. */
function seenAt(d: Pick<Device, 'LastSeen' | 'FirstSeen'>): number {
  const at = Date.parse(d.LastSeen || d.FirstSeen || '');
  return Number.isNaN(at) ? 0 : at;
}

const named = (d: Pick<Device, 'CustomName'>) => !!d.CustomName?.trim();

/**
 * The pill for a device; null where none is shown (an authorised device on
 * a box that cannot reserve its Wi-Fi: « Autorisé » is hidden there, D11).
 * Open, the orange « À vérifier » is for a device without a name seen these
 * 7 days; the others stay neutral, so the household's own TV does not stay
 * orange for ever (R5). Reserved, every device to check is refused: orange.
 */
export function trustPill(
  d: Pick<Device, 'Blocked' | 'Approved' | 'CustomName' | 'LastSeen' | 'FirstSeen' | 'Connected'>,
  ctx: { capable: boolean; reserved: boolean; cut: boolean; now: number },
): Pill | null {
  const trust = trustOf(d, ctx.cut);
  if (trust === 'blocked') {
    return {
      trust,
      role: 'blocked',
      icon: 'block-helper',
      label: t('trust.blocked'),
      sub: t(ctx.cut ? 'trust.cantConnect' : 'trust.noInternet'),
    };
  }
  if (trust === 'authorised') {
    return ctx.capable ? { trust, role: 'connected', icon: 'check-decagram', label: t('trust.authorised') } : null;
  }
  const seen = seenAt(d);
  const fresh = !named(d) && (d.Connected || (seen > 0 && ctx.now - seen <= WEEK));
  return {
    trust,
    role: ctx.reserved || fresh ? 'attention' : 'neutral',
    icon: 'help-circle-outline',
    label: t('trust.check'),
  };
}

/** One of the three choices (D5), with its sentence and what it needs. */
export type Choice = {
  trust: Trust;
  label: string;
  line: string;
  /** The tier it needs (D8); null: free. */
  feature: Feature | null;
  /** Why it cannot be chosen; null when it can. */
  disabled: string | null;
};

/**
 * The choices for a device (D5, D8), the current one included. « Autorisé »
 * is offered on every box: it is the alerts' authorisation (#58) too; on a
 * box that cannot reserve its Wi-Fi only the list's badge hides it (D11).
 * While
 * the Wi-Fi is reserved, « À vérifier » refuses the device: it is offered as
 * « Retirer de la maison » (R4), Basic like a block when it withdraws an
 * authorisation. This phone is never blocked, nor removed while reserved;
 * the owner's phones neither (#113): what blocked them before stays undoable.
 */
export function trustChoices(ctx: { capable: boolean; reserved: boolean; current: Trust; mark?: DeviceMark }): Choice[] {
  const { capable, reserved, current, mark } = ctx;
  const never: string | null = mark === 'Self' ? t('trust.thisPhone') : mark === 'Owner' ? t('trust.ownerPhone') : null;
  const out: Choice[] = [
    { trust: 'authorised', label: t('trust.authorised'), line: t(capable ? 'trust.authorisedLine' : 'trust.authorisedAlerts'), feature: null, disabled: null },
  ];
  const remove = capable && reserved;
  out.push({
    trust: 'check',
    label: t(remove ? 'trust.remove' : 'trust.check'),
    line: t(remove ? 'trust.removeLine' : 'trust.checkLine'),
    feature: remove && current === 'authorised' ? 'cut_wifi_access' : null,
    disabled: remove && current !== 'blocked' ? never : null,
  });
  out.push({
    trust: 'blocked',
    label: t('trust.blocked'),
    line: t('trust.blockedLine'),
    feature: current === 'blocked' ? null : 'block',
    disabled: current === 'blocked' ? null : never,
  });
  return out;
}

/** « 18 / 32 places » (D3): orange from 80 %, full at the limit; the count alone when the limit is unknown. */
export function placesLine(count: number, places: number, known: boolean): { text: string; warn: boolean; full: boolean } {
  if (!known || places <= 0) return { text: t('devices.placesCount', { count }), warn: false, full: false };
  return { text: t('devices.places', { count, places }), warn: count >= 0.8 * places, full: count >= places };
}

/** What « Réserver mon Wi-Fi à la maison » may do now (D9, D10, D12). */
export type ReserveCheck = {
  ok: boolean;
  /** Why the button is off, in words. */
  why: string | null;
  /** Said before the one confirmation; they do not stop it. */
  warnings: string[];
  /** This phone's address is private: the advice line (R10). */
  privateAddress: boolean;
};

/**
 * Off until this phone is in the list (« Autorisez d'abord ce téléphone »):
 * the core always puts it there once it finds it on the box. Through the
 * relay (D9) the relay must be in, and this phone left out is a warning. A
 * household relay Kergui knows no address of is said before anything is
 * written; a full list says the numbers.
 */
export function reserveCheck(p: WiFiLockPreview, ctx: { viaRelay: boolean; hasRelay: boolean }): ReserveCheck {
  const warnings: string[] = [];
  const out = (ok: boolean, why: string | null): ReserveCheck => ({ ok, why, warnings, privateAddress: !!p.self_private });
  if (p.full) {
    return out(false, p.full.limit > 0 ? t('error.listFull', { count: p.full.limit }) : t('error.listFullUnknown'));
  }
  if (ctx.viaRelay) {
    if (!p.relays.length) return out(false, t('lock.relayFirst'));
    if (!p.self) warnings.push(t('lock.phoneNotAllowed'));
    return out(true, null);
  }
  if (!p.self) return out(false, t('lock.selfFirst'));
  if (ctx.hasRelay && !p.relays.length) warnings.push(t('lock.relayUnknown'));
  return out(true, null);
}

/** « Voici les appareils de la maison » (R5, R8): who is listed, in which group, and what is ticked. */
export type HouseDevices = {
  /** This phone, the owner's phones and the relays: ticked, never untickable. */
  always: Device[];
  /** Named or authorised: ticked. */
  named: Device[];
  /** Without a name, seen these 30 days: to tick by hand. */
  recent: Device[];
  checked: Set<string>;
  /** Some were unticked to fit the places. */
  capped: boolean;
};

const lower = (m: string) => m.toLowerCase();

/**
 * The devices seen in the last 30 days and not blocked, in two groups (R8):
 * named or authorised ones ticked, the others unticked. This phone, the
 * relays and the owner's phones come first, ticked for good. Capped at the
 * places: the oldest seen are unticked first.
 */
export function houseDevices(
  devices: Device[],
  ctx: { relays: string[]; places: number; placesKnown: boolean; now: number },
): HouseDevices {
  const relays = new Set(ctx.relays.map(lower));
  const recent = (d: Device) => d.Connected || (seenAt(d) > 0 && ctx.now - seenAt(d) <= MONTH);
  const shown = devices.filter((d) => !d.Blocked && (recent(d) || !!d.Mark || relays.has(lower(d.MAC))));
  const isAlways = (d: Device) => !!d.Mark || relays.has(lower(d.MAC));
  const always = shown.filter(isAlways).sort((a, b) => rank(a, relays) - rank(b, relays));
  const rest = shown.filter((d) => !isAlways(d));
  const namedOnes = rest.filter((d) => named(d) || !!d.Approved);
  const recentOnes = rest.filter((d) => !named(d) && !d.Approved);
  let ticked = [...namedOnes];
  let capped = false;
  if (ctx.placesKnown && ctx.places > 0) {
    const room = Math.max(0, ctx.places - always.length);
    if (ticked.length > room) {
      ticked = [...ticked].sort((a, b) => seenAt(b) - seenAt(a)).slice(0, room);
      capped = true;
    }
  }
  return { always, named: namedOnes, recent: recentOnes, checked: new Set(ticked.map((d) => d.MAC)), capped };
}

/** This phone first, then the owner's phones, then the relays. */
function rank(d: Device, relays: Set<string>): number {
  if (d.Mark === 'Self') return 0;
  if (d.Mark === 'Owner') return 1;
  return relays.has(lower(d.MAC)) ? 2 : 3;
}

/** One network to ask « Ce réseau est-il pour vos visiteurs ? » about (D2, R9). */
export type VisitorCandidate = { name: string; ids: string[]; bands: string; connected: number };

const BAND: Record<string, string> = { '2.4GHz': '2,4 GHz', '5GHz': '5 GHz', '6GHz': '6 GHz' };

/**
 * The enabled networks other than the one this phone is on (and its twin of
 * the same name on the other band), each with its bands and how many
 * devices are on it now, so the household recognises it (R9).
 */
export function visitorCandidates(aps: WiFiLockAP[], devices: Device[]): VisitorCandidate[] {
  const mine = (devices.find((d) => d.Mark === 'Self')?.SSID ?? '').trim().toLowerCase();
  const byName = new Map<string, WiFiLockAP[]>();
  for (const ap of aps) {
    const key = ap.name.trim();
    if (!key || key.toLowerCase() === mine) continue;
    byName.set(key, [...(byName.get(key) ?? []), ap]);
  }
  return [...byName.entries()].map(([name, list]) => ({
    name,
    ids: list.map((a) => a.id),
    bands: list.map((a) => BAND[a.band] ?? a.band).filter(Boolean).join(' et '),
    connected: devices.filter((d) => d.Connected && d.SSID?.trim().toLowerCase() === name.toLowerCase()).length,
  }));
}

/** The words of a choice once made, for the undo bar (R6). */
export function doneKey(to: Trust, reserved: boolean): MessageKey {
  if (to === 'blocked') return 'trust.done.blocked';
  if (to === 'authorised') return 'trust.done.authorised';
  return reserved ? 'trust.done.remove' : 'trust.done.check';
}
