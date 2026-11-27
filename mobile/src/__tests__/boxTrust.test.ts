// Trust on first use without a question (#122, audit H6): the very first
// connection to the box's default private address (https://192.168.1.1)
// keeps its certificate without a dialog. Any other address asks, a known
// box with the same certificate goes on, and a different certificate is
// never accepted in silence.
import { DEFAULT_BOX_ADDRESS, isDefaultBoxUrl, isPrivateIPv4, trustDecision } from '../lib/boxTrust';

const PIN = 'ab'.repeat(32);
const OTHER = 'cd'.repeat(32);

test('the default address is the one the connection screen is filled with, a private one', () => {
  expect(DEFAULT_BOX_ADDRESS).toBe('192.168.1.1');
  expect(isPrivateIPv4(DEFAULT_BOX_ADDRESS)).toBe(true);
});

test('only https on the default address, on its own port, counts as the default box', () => {
  expect(isDefaultBoxUrl('https://192.168.1.1')).toBe(true);
  expect(isDefaultBoxUrl('https://192.168.1.1/')).toBe(true);
  expect(isDefaultBoxUrl('https://192.168.1.1:443')).toBe(true);
  expect(isDefaultBoxUrl('HTTPS://192.168.1.1')).toBe(true);
  // Another port, another host, a name that only starts like it.
  expect(isDefaultBoxUrl('https://192.168.1.1:8443')).toBe(false);
  expect(isDefaultBoxUrl('https://192.168.1.10')).toBe(false);
  expect(isDefaultBoxUrl('https://192.168.1.1.example.com')).toBe(false);
  expect(isDefaultBoxUrl('https://example.com@192.168.1.1')).toBe(false);
  expect(isDefaultBoxUrl('https://box.local')).toBe(false);
  expect(isDefaultBoxUrl('http://192.168.1.1')).toBe(false);
  expect(isDefaultBoxUrl('')).toBe(false);
  expect(isDefaultBoxUrl('not a url')).toBe(false);
});

test('private IPv4 is RFC 1918 only', () => {
  for (const ip of ['10.0.0.1', '172.16.0.1', '172.31.255.254', '192.168.0.1', '192.168.1.1']) {
    expect(isPrivateIPv4(ip)).toBe(true);
  }
  for (const ip of ['8.8.8.8', '172.15.0.1', '172.32.0.1', '192.169.1.1', '100.64.0.1', '127.0.0.1', '1.2.3', '192.168.1.256', 'box']) {
    expect(isPrivateIPv4(ip)).toBe(false);
  }
});

test('the first connection to the default address keeps the certificate without a question', () => {
  expect(trustDecision('https://192.168.1.1', PIN, null)).toBe('firstDefault');
});

test('a box already known with the same certificate goes on without a question', () => {
  expect(trustDecision('https://192.168.1.1', PIN, PIN)).toBe('known');
  expect(trustDecision('https://192.168.1.1', PIN, PIN.toUpperCase())).toBe('known');
  expect(trustDecision('https://10.0.0.138', PIN, PIN)).toBe('known');
});

test('a different certificate than the known one is never accepted in silence', () => {
  expect(trustDecision('https://192.168.1.1', OTHER, PIN)).toBe('changed');
  expect(trustDecision('https://10.0.0.138', OTHER, PIN)).toBe('changed');
});

test('an address typed by hand, private or not, asks on the first connection', () => {
  expect(trustDecision('https://10.0.0.138', PIN, null)).toBe('ask');
  expect(trustDecision('https://192.168.100.1', PIN, null)).toBe('ask');
  expect(trustDecision('https://41.82.10.1', PIN, null)).toBe('ask');
  expect(trustDecision('https://192.168.1.1:8443', PIN, null)).toBe('ask');
  expect(trustDecision('https://box.example.com', PIN, null)).toBe('ask');
});

test('when what Kergui remembers cannot be read, it asks', () => {
  expect(trustDecision('https://192.168.1.1', PIN, undefined)).toBe('ask');
});
