/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The Go core gates the paid writes itself: the app hands it the build's
// public key and every change of the entitlement token.
jest.mock('../core/client', () => ({
  api: { inspect: jest.fn() },
  core: { setPublicKeys: jest.fn(async () => undefined), setEntitlement: jest.fn(async () => undefined) },
}));
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));
jest.mock('../licensing/config', () => ({
  config: { hubUrl: 'https://hub.test', publicKeys: [{ keyId: '65b60673d6ed884b', publicKey: 'ebVWLo/mVPlAeLES6KmLp5AfhTrmlb7X4OORC60ElmQ=' }] },
}));

import { core } from '../core/client';
import { syncCore, useEntitlement } from '../licensing/entitlement';
import { makeRing } from '../licensing/keyring';

const mockCore = core as unknown as { setPublicKeys: jest.Mock; setEntitlement: jest.Mock };

const flush = () => new Promise((ok) => setTimeout(ok, 0));

beforeEach(() => {
  mockCore.setPublicKeys.mockClear();
  mockCore.setEntitlement.mockClear();
});

test('syncCore hands the core the build key and the current token', async () => {
  useEntitlement.setState({ token: 'a.b' });
  await flush();
  mockCore.setEntitlement.mockClear();
  await syncCore();
  expect(mockCore.setPublicKeys).toHaveBeenCalledWith('ebVWLo/mVPlAeLES6KmLp5AfhTrmlb7X4OORC60ElmQ=');
  expect(mockCore.setEntitlement).toHaveBeenCalledWith('a.b');
});

test('every token change reaches the core; no token is ""', async () => {
  useEntitlement.setState({ token: 'c.d' });
  await flush();
  expect(mockCore.setEntitlement).toHaveBeenLastCalledWith('c.d');
  useEntitlement.setState({ token: null });
  await flush();
  expect(mockCore.setEntitlement).toHaveBeenLastCalledWith('');
});

test('a key learned or revoked reaches the core, revocations included', async () => {
  useEntitlement.setState({
    keys: makeRing(
      [{ keyId: 'c945cbf2a5602002', publicKey: '5/FioQvsVZr+oZXk3OhLaVaNXSywlj60RsBoXisX8vA=' }],
      ['65b60673d6ed884b'],
    ),
  });
  await flush();
  expect(mockCore.setPublicKeys).toHaveBeenLastCalledWith('5/FioQvsVZr+oZXk3OhLaVaNXSywlj60RsBoXisX8vA=,revoked:65b60673d6ed884b');
});

test('a core that is not open does not break anything', async () => {
  mockCore.setPublicKeys.mockRejectedValueOnce(new Error('Kergui core is not open'));
  await expect(syncCore()).resolves.toBeUndefined();
});
