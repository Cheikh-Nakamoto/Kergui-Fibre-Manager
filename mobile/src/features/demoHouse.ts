import type { Device } from '../core/types';

/**
 * What the demo's « Couper Internet » for the whole house blocks: every
 * device not blocked yet, except the ones the relay spares for real — this
 * phone and the owner's phones (#113). Blocking this phone makes the core
 * answer 409 self_device, which used to stop the whole cut.
 */
export function demoHouseTargets(devices: Device[]): { cut: string[]; spared: string[] } {
  const out = { cut: [] as string[], spared: [] as string[] };
  for (const d of devices) {
    if (d.Blocked) continue;
    if (d.Mark === 'Self' || d.Mark === 'Owner') out.spared.push(d.MAC);
    else out.cut.push(d.MAC);
  }
  return out;
}
