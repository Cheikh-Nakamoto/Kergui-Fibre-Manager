import { newDevices } from '../alerts/diff';
import type { Device } from '../core/types';

const dev = (MAC: string) => ({ MAC }) as Device;

test('first reading records everything without alerting', () => {
  const r = newDevices(null, [dev('a'), dev('b')]);
  expect(r.fresh).toEqual([]);
  expect(r.next.sort()).toEqual(['a', 'b']);
});

test('only devices not seen before ring', () => {
  const r = newDevices(['a'], [dev('a'), dev('b')]);
  expect(r.fresh.map((d) => d.MAC)).toEqual(['b']);
  expect(r.next.sort()).toEqual(['a', 'b']);
});

test('a device that left stays known, so it does not ring again on return', () => {
  const r = newDevices(['a', 'b'], [dev('a')]);
  expect(r.fresh).toEqual([]);
  expect(r.next.sort()).toEqual(['a', 'b']);
});
