import type { Device, Usage } from '../core/types';
import { formatBytes, matchesDevice } from '../lib/format';
import { createSampler, curveBars, monthLabel, OLDEST_MONTHS_BACK, periodOf, shiftPeriod, shortMonth, usageRows } from '../lib/usage';

test('formatBytes speaks French: o, Ko, Mo, Go, with a decimal comma', () => {
  expect(formatBytes(0)).toBe('0 o');
  expect(formatBytes(999)).toBe('999 o');
  expect(formatBytes(1000)).toBe('1 Ko');
  expect(formatBytes(1500)).toBe('1,5 Ko');
  expect(formatBytes(12_345_678)).toBe('12 Mo');
  expect(formatBytes(345_000_000)).toBe('345 Mo');
  expect(formatBytes(2_500_000_000)).toBe('2,5 Go');
  expect(formatBytes(48_200_000_000)).toBe('48 Go');
  expect(formatBytes(3_000_000_000_000)).toBe('3 To');
});

test('formatBytes never shows "1000 Ko": it moves to the next unit', () => {
  expect(formatBytes(999_960)).toBe('1 Mo');
  expect(formatBytes(9_960)).toBe('10 Ko');
});

test('formatBytes copes with what is not a count', () => {
  expect(formatBytes(-5)).toBe('0 o');
  expect(formatBytes(Number.NaN)).toBe('0 o');
});

const dev = (mac: string, over: Partial<Device> = {}): Device => ({
  ID: mac,
  MAC: mac,
  IP: '192.168.1.10',
  Hostname: '',
  CustomName: '',
  Vendor: '',
  SSID: '',
  FirstSeen: '',
  LastSeen: '',
  Connected: true,
  Blocked: false,
  NewlySeen: false,
  ...over,
});
const use = (mac: string, sent: number, received: number): Usage => ({
  mac: mac.toLowerCase(),
  period: '2026-11',
  bytes: sent + received,
  sent,
  received,
});

test('matchesDevice searches the name, hostname, MAC, IP and vendor', () => {
  const d = dev('AC:BB:CC:00:00:22', { CustomName: 'Bureau', Hostname: 'PC-Bureau', Vendor: 'Dell' });
  expect(matchesDevice(d, '')).toBe(true);
  expect(matchesDevice(d, ' bureau ')).toBe(true);
  expect(matchesDevice(d, 'dell')).toBe(true);
  expect(matchesDevice(d, '00:00:22')).toBe(true);
  expect(matchesDevice(d, 'salon')).toBe(false);
});

test('usageRows sorts by consumption, names devices, keeps the silent ones last', () => {
  const devices = [
    dev('AC:BB:CC:00:00:11', { Hostname: 'Phone-Salon' }),
    dev('AC:BB:CC:00:00:22', { Hostname: 'PC-Bureau' }),
    dev('AC:BB:CC:00:00:33', { Hostname: 'TV' }),
  ];
  const usage = [use('AC:BB:CC:00:00:11', 100, 900), use('AC:BB:CC:00:00:22', 2_000, 8_000), use('02:00:00:00:00:99', 5, 5)];
  const rows = usageRows(devices, usage, '');
  expect(rows.map((r) => r.name)).toEqual(['PC-Bureau', 'Phone-Salon', '02:00:00:00:00:99', 'TV']);
  expect(rows[0]).toMatchObject({ bytes: 10_000, sent: 2_000, received: 8_000, share: 1 });
  expect(rows[1].share).toBeCloseTo(0.1);
  expect(rows[3]).toMatchObject({ bytes: 0, share: 0 });
});

test('usageRows filters by the same search as the device list', () => {
  const devices = [dev('AC:BB:CC:00:00:11', { Hostname: 'Phone-Salon' }), dev('AC:BB:CC:00:00:22', { Hostname: 'PC-Bureau' })];
  const usage = [use('AC:BB:CC:00:00:11', 1, 1), use('AC:BB:CC:00:00:22', 2, 2)];
  expect(usageRows(devices, usage, 'salon').map((r) => r.name)).toEqual(['Phone-Salon']);
  // The share stays relative to the whole house, not to the filtered list.
  expect(usageRows(devices, usage, 'salon')[0].share).toBeCloseTo(0.5);
});

test('the sampler asks the box at most once per interval', async () => {
  let now = 0;
  const sample = jest.fn(async () => undefined);
  const sampler = createSampler(sample, 10_000, () => now);
  await sampler();
  await sampler();
  expect(sample).toHaveBeenCalledTimes(1);
  now = 10_000;
  await sampler();
  expect(sample).toHaveBeenCalledTimes(2);
});

test('a failed sample does not break the caller and can be retried at once', async () => {
  let now = 0;
  const sample = jest.fn(async () => {
    throw new Error('box unreachable');
  });
  const sampler = createSampler(sample, 10_000, () => now);
  await expect(sampler()).resolves.toBe(false);
  now = 1;
  await sampler();
  expect(sample).toHaveBeenCalledTimes(2);
});

test('periods: the month of a date, and months before and after it', () => {
  expect(periodOf(new Date(2026, 10, 22))).toBe('2026-11');
  expect(periodOf(new Date(2026, 0, 31))).toBe('2026-01');
  expect(shiftPeriod('2026-11', -1)).toBe('2026-10');
  expect(shiftPeriod('2026-01', -1)).toBe('2025-12');
  expect(shiftPeriod('2025-12', 1)).toBe('2026-01');
  // 13 months are kept: this one and the 12 before it.
  expect(shiftPeriod('2026-11', -OLDEST_MONTHS_BACK)).toBe('2025-11');
});

test('month names are French', () => {
  expect(monthLabel('2026-10')).toBe('octobre 2026');
  expect(monthLabel('2027-02')).toBe('février 2027');
  expect(shortMonth('2026-06')).toBe('juin');
  expect(shortMonth('2026-07')).toBe('juil.');
});

test('curveBars: one bar a month, as high as its part of the busiest month', () => {
  const u = (period: string, bytes: number): Usage => ({ mac: 'aa', period, bytes, sent: 0, received: 0 });
  expect(curveBars([u('2026-09', 0), u('2026-10', 5), u('2026-11', 10)])).toEqual([
    { period: '2026-09', bytes: 0, share: 0 },
    { period: '2026-10', bytes: 5, share: 0.5 },
    { period: '2026-11', bytes: 10, share: 1 },
  ]);
  expect(curveBars([u('2026-11', 0)])).toEqual([{ period: '2026-11', bytes: 0, share: 0 }]);
});
