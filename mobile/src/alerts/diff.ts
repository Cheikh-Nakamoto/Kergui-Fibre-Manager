import type { Device } from '../core/types';

/**
 * The devices that arrived since the last reading and nobody approved
 * (issue #58): on the Wi-Fi now, not at the last reading. `next` is who is on
 * the Wi-Fi now, the next reading's `previous`. A device that stays does not
 * ring again; one that leaves and comes back does, until it is approved.
 * Blocked devices never ring (auto-reject and the block handle them).
 *
 * `previous === null` is the first reading for this box: everything on it is
 * recorded without an alert, otherwise connecting to a box would ring once
 * per device already there.
 */
export function arrivals(previous: string[] | null, devices: Device[]): { fresh: Device[]; next: string[] } {
  const before = new Set((previous ?? []).map((m) => m.toLowerCase()));
  const present = devices.filter((d) => d.Connected !== false);
  const fresh =
    previous === null ? [] : present.filter((d) => !before.has(d.MAC.toLowerCase()) && !d.Approved && !d.Blocked);
  return { fresh, next: present.map((d) => d.MAC.toLowerCase()) };
}
