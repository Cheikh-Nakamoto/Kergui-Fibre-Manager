import * as SecureStore from 'expo-secure-store';

// The certificate fingerprint of each box this phone connected to, by
// address (#122). Kept apart from the session: « Oublier cette box » ends
// the session, but a box that comes back with another certificate is still
// asked about, never trusted in silence as a first connection.

const KEY = 'kergui.boxPins';

function keyOf(url: string): string {
  return url.trim().replace(/\/+$/, '').toLowerCase();
}

async function readAll(): Promise<Record<string, string>> {
  const raw = await SecureStore.getItemAsync(KEY);
  if (!raw) return {};
  const parsed: unknown = JSON.parse(raw);
  return parsed && typeof parsed === 'object' ? (parsed as Record<string, string>) : {};
}

/**
 * The fingerprint remembered for this address, or null when there is none.
 * It throws when the store cannot be read: the caller then asks.
 */
export async function knownBoxPin(url: string): Promise<string | null> {
  const pin = (await readAll())[keyOf(url)];
  return typeof pin === 'string' && pin ? pin : null;
}

/**
 * Remembers the fingerprint of the box at this address. With `ifAbsent`, an
 * address that already has one keeps it (a session saved before #122).
 */
export async function rememberBoxPin(url: string, pin: string, opts: { ifAbsent?: boolean } = {}): Promise<void> {
  if (!pin) return;
  const all = await readAll();
  const k = keyOf(url);
  if (opts.ifAbsent && all[k]) return;
  all[k] = pin;
  await SecureStore.setItemAsync(KEY, JSON.stringify(all), { keychainAccessible: SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY });
}
