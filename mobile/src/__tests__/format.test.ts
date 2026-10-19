import { t } from '../i18n';
import { displayName, formatFingerprint, formatLogTime, logLine, normaliseRouterUrl, sortDevices } from '../lib/format';

test('formatFingerprint groups by two', () => {
  expect(formatFingerprint('ab12cd')).toBe('AB:12:CD');
});

test('normaliseRouterUrl defaults to https and trims slashes', () => {
  expect(normaliseRouterUrl(' 192.168.1.1/ ')).toBe('https://192.168.1.1');
  expect(normaliseRouterUrl('http://192.168.1.1')).toBe('http://192.168.1.1');
  expect(normaliseRouterUrl('')).toBe('');
});

test('displayName prefers the custom name, then hostname, then MAC', () => {
  expect(displayName({ CustomName: 'TV', Hostname: 'tv-01', MAC: 'm' })).toBe('TV');
  expect(displayName({ CustomName: '', Hostname: 'tv-01', MAC: 'm' })).toBe('tv-01');
  expect(displayName({ CustomName: '', Hostname: '', MAC: 'm' })).toBe('m');
});

test('sortDevices puts connected devices first, then by name', () => {
  const d = (n: string, c: boolean) => ({ CustomName: n, Hostname: '', MAC: n, Connected: c });
  expect(sortDevices([d('b', false), d('c', true), d('a', true)]).map((x) => x.MAC)).toEqual(['a', 'c', 'b']);
});

test('t fills parameters and French plurals', () => {
  expect(t('devices.summary', { connected: 3, blocked: 1, new: 0 })).toBe('3 connectés · 1 bloqué · 0 nouveau');
  expect(t('devices.summary', { connected: 1, blocked: 2, new: 5 })).toBe('1 connecté · 2 bloqués · 5 nouveaux');
});

test('formats a journal timestamp to the second, and says so when it cannot', () => {
  const at = new Date(2026, 9, 19, 14, 5, 9);
  expect(formatLogTime(at.toISOString())).toBe('14:05:09');
  expect(formatLogTime('pas une date')).toBe('--:--:--');
});

test('a shared journal line carries the date, the level and the message', () => {
  const line = logLine({ time: '2026-10-19T14:05:09.000Z', level: 'warn', msg: 'HTTP GET /api/devices -> 502' });
  expect(line).toBe('2026-10-19T14:05:09.000Z WARN  HTTP GET /api/devices -> 502');
});
