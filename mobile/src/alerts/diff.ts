import type { Device } from '../core/types';

/**
 * The devices that were not known before this reading.
 *
 * `known === null` means this is the first reading for this box: everything
 * on it is recorded as known without an alert, otherwise connecting to a box
 * would ring once per existing device.
 */
export function newDevices(known: string[] | null, devices: Device[]): { fresh: Device[]; next: string[] } {
  const seen = new Set(known ?? []);
  const fresh = known === null ? [] : devices.filter((d) => !seen.has(d.MAC));
  for (const d of devices) seen.add(d.MAC);
  return { fresh, next: [...seen] };
}
