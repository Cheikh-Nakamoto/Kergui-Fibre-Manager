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

/** Sorts devices: connected first, then by display name. */
export function sortDevices<T extends { Connected: boolean; CustomName: string; Hostname: string; MAC: string }>(
  list: T[],
): T[] {
  return [...list].sort(
    (a, b) => Number(b.Connected) - Number(a.Connected) || displayName(a).localeCompare(displayName(b), 'fr'),
  );
}
