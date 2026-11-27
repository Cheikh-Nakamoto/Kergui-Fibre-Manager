// The demo's « Couper Internet » for the whole house spares the devices the
// relay spares for real: this phone and the owner's phones (#113). Blocking
// this phone made the core answer 409 self_device and stopped the whole cut
// (mobile-ci run 46051580442, house-remote-steps.yaml).
import { demoHouseTargets } from '../features/demoHouse';
import type { Device } from '../core/types';

const device = (MAC: string, extra: Partial<Device> = {}): Device => ({ MAC, Blocked: false, ...extra }) as Device;

test('this phone and the owner phones are spared, blocked devices skipped, the rest cut', () => {
  const out = demoHouseTargets([
    device('ac:bb:cc:00:00:22'),
    device('ac:bb:cc:00:00:99', { Mark: 'Self' }),
    device('ac:bb:cc:00:00:98', { Mark: 'Owner' }),
    device('ac:bb:cc:00:00:11', { Blocked: true }),
    device('ac:bb:cc:00:00:33'),
  ]);
  expect(out.cut).toEqual(['ac:bb:cc:00:00:22', 'ac:bb:cc:00:00:33']);
  expect(out.spared).toEqual(['ac:bb:cc:00:00:99', 'ac:bb:cc:00:00:98']);
});
