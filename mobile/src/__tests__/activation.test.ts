jest.mock('expo-crypto', () => ({
  CryptoDigestAlgorithm: { SHA256: 'SHA-256' },
  digestStringAsync: async (_: string, s: string) => require('crypto').createHash('sha256').update(s).digest('hex'),
}));
const mockStore = new Map<string, string>();
jest.mock('expo-secure-store', () => ({
  getItemAsync: async (k: string) => mockStore.get(k) ?? null,
  setItemAsync: async (k: string, v: string) => void mockStore.set(k, v),
}));

import { activate, isActivated, isValidKey } from '../activation/activation';

test('the owner key opens the app, surrounding spaces ignored', async () => {
  expect(await isValidKey('Talent01@Kergui')).toBe(true);
  expect(await isValidKey('  Talent01@Kergui ')).toBe(true);
});

test('a wrong key or a different case is refused', async () => {
  expect(await isValidKey('talent01@kergui')).toBe(false);
  expect(await isValidKey('')).toBe(false);
});

test('activation is remembered only after a valid key', async () => {
  expect(await isActivated()).toBe(false);
  expect(await activate('nope')).toBe(false);
  expect(await isActivated()).toBe(false);
  expect(await activate('Talent01@Kergui')).toBe(true);
  expect(await isActivated()).toBe(true);
});
