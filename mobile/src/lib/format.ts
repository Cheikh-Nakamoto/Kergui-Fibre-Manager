/** "AB12CD..." → "AB:12:CD:…", grouped by two for reading a fingerprint aloud. */
export function formatFingerprint(hex: string): string {
  return (hex.toUpperCase().match(/.{1,2}/g) ?? []).join(':');
}

export function displayName(d: { CustomName: string; Hostname: string; MAC: string }): string {
  return d.CustomName || d.Hostname || d.MAC;
}

/** Normalises what a user types in the address field into a base URL. */
export function normaliseRouterUrl(input: string): string {
  const v = input.trim().replace(/\/+$/, '');
  if (!v) return '';
  return /^https?:\/\//i.test(v) ? v : `https://${v}`;
}

/** The device list's search: custom name, hostname, MAC, IP or vendor. */
export function matchesDevice(
  d: { CustomName: string; Hostname: string; MAC: string; IP: string; Vendor: string },
  query: string,
): boolean {
  const q = query.trim().toLowerCase();
  return !q || [d.CustomName, d.Hostname, d.MAC, d.IP, d.Vendor].some((v) => v?.toLowerCase().includes(q));
}

const BYTE_UNITS = ['o', 'Ko', 'Mo', 'Go', 'To'];

/**
 * A byte count in French: "999 o", "1,5 Ko", "12 Mo", "2,5 Go". Decimal units
 * (1 Ko = 1 000 o), as operators count their Go; one decimal under 10.
 */
export function formatBytes(bytes: number): string {
  let v = Number.isFinite(bytes) && bytes > 0 ? bytes : 0;
  let unit = 0;
  while (v >= 1000 && unit < BYTE_UNITS.length - 1) {
    v /= 1000;
    unit++;
  }
  if (unit === 0) return `${Math.round(v)} o`;
  let shown = v < 10 ? Math.round(v * 10) / 10 : Math.round(v);
  // 999,96 Ko rounds to 1000: say 1 Mo instead.
  if (shown >= 1000 && unit < BYTE_UNITS.length - 1) {
    shown = 1;
    unit++;
  }
  return `${String(shown).replace('.', ',')} ${BYTE_UNITS[unit]}`;
}

/** Sorts devices: connected first, then by display name. */
export function sortDevices<T extends { Connected: boolean; CustomName: string; Hostname: string; MAC: string }>(
  list: T[],
): T[] {
  return [...list].sort(
    (a, b) => Number(b.Connected) - Number(a.Connected) || displayName(a).localeCompare(displayName(b), 'fr'),
  );
}

/** The time of a journal entry, to the second: 14:05:09. */
export function formatLogTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '--:--:--';
  return [d.getHours(), d.getMinutes(), d.getSeconds()].map((n) => String(n).padStart(2, '0')).join(':');
}

/** One journal line, as it is shared: date, level, message. */
export function logLine(e: { time: string; level: string; msg: string }): string {
  const d = new Date(e.time);
  const stamp = Number.isNaN(d.getTime()) ? e.time : d.toISOString();
  return `${stamp} ${e.level.toUpperCase().padEnd(5)} ${e.msg}`;
}

/** "21/11/2026" from a Unix-seconds time or an ISO string (local date). */
export function formatDay(time: number | string): string {
  const d = typeof time === 'number' ? new Date(time * 1000) : new Date(time);
  if (Number.isNaN(d.getTime())) return '—';
  const p = (n: number) => String(n).padStart(2, '0');
  return `${p(d.getDate())}/${p(d.getMonth() + 1)}/${d.getFullYear()}`;
}

/** "14:02" from an ISO time, in the phone's time zone; "—" when there is none. */
export function clockTime(iso: string | undefined | null): string {
  const d = iso ? new Date(iso) : null;
  if (!d || Number.isNaN(d.getTime())) return '—';
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}
