import { arrivals } from '../alerts/diff';
import type { Device } from '../core/types';

// Issue #58: a device nobody approved rings each time it arrives (absent
// from the last reading, present now), not only the first time it is seen.

const dev = (MAC: string, extra: Partial<Device> = {}) => ({ MAC, Connected: true, Approved: false, Blocked: false, ...extra }) as Device;

test('first reading records what is there without alerting', () => {
  const r = arrivals(null, [dev('a'), dev('b')]);
  expect(r.fresh).toEqual([]);
  expect(r.next.sort()).toEqual(['a', 'b']);
});

test('a device that was not there rings', () => {
  const r = arrivals(['a'], [dev('a'), dev('b')]);
  expect(r.fresh.map((d) => d.MAC)).toEqual(['b']);
  expect(r.next.sort()).toEqual(['a', 'b']);
});

test('a device that stays does not ring again', () => {
  expect(arrivals(['a', 'b'], [dev('a'), dev('b')]).fresh).toEqual([]);
});

test('a device that left and comes back rings again', () => {
  const gone = arrivals(['a', 'b'], [dev('a')]);
  expect(gone.fresh).toEqual([]);
  expect(gone.next).toEqual(['a']);
  expect(arrivals(gone.next, [dev('a'), dev('b')]).fresh.map((d) => d.MAC)).toEqual(['b']);
});

test('a device listed but off the Wi-Fi is absent', () => {
  const r = arrivals(['a'], [dev('a'), dev('b', { Connected: false })]);
  expect(r.fresh).toEqual([]);
  expect(r.next).toEqual(['a']);
});

test('an approved device never rings, a blocked one neither', () => {
  const r = arrivals([], [dev('a', { Approved: true }), dev('b', { Blocked: true }), dev('c')]);
  expect(r.fresh.map((d) => d.MAC)).toEqual(['c']);
  // All three are on the Wi-Fi: none rings again while it stays.
  expect(r.next.sort()).toEqual(['a', 'b', 'c']);
});

test('MACs compare whatever their case', () => {
  expect(arrivals(['AA:BB'], [dev('aa:bb')]).fresh).toEqual([]);
});
