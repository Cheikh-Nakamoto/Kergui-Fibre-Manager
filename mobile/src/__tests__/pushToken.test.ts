// The app's push token (issue #49): once the alerts are on and the phone is
// signed in to its household, it registers its Expo push token on the hub,
// so a new device reaches it with the app closed. A build without an EAS
// project registers nothing (push off, like the hub without Expo), a refused
// permission registers nothing, and turning the alerts off removes it.
jest.mock('expo-notifications', () => ({ getPermissionsAsync: jest.fn(), getExpoPushTokenAsync: jest.fn() }));
jest.mock('expo-secure-store', () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'x',
  getItemAsync: async () => null,
  setItemAsync: async () => undefined,
  deleteItemAsync: async () => undefined,
}));

// eslint-disable-next-line import/first -- the mocks come first
import { createPushToken, type PushDeps } from '../alerts/push';

function deps(over: Partial<PushDeps> = {}) {
  const d = {
    projectId: jest.fn(() => '0f8b1c2d-3e4f-4a5b-8c6d-7e8f9a0b1c2d' as string | undefined),
    platform: 'android' as const,
    permitted: jest.fn(async () => true),
    token: jest.fn(async () => 'ExponentPushToken[abc]'),
    signedIn: jest.fn(() => true),
    register: jest.fn(async () => undefined),
    unregister: jest.fn(async () => undefined),
    ...over,
  };
  return d;
}

test('registers the Expo token for this platform', async () => {
  const d = deps();
  expect(await createPushToken(d).sync()).toBe('registered');
  expect(d.token).toHaveBeenCalledWith('0f8b1c2d-3e4f-4a5b-8c6d-7e8f9a0b1c2d');
  expect(d.register).toHaveBeenCalledWith({ token: 'ExponentPushToken[abc]', platform: 'android' });
});

test('no EAS project in the build: push is off, nothing is asked', async () => {
  const d = deps({ projectId: jest.fn(() => undefined) });
  expect(await createPushToken(d).sync()).toBe('not_configured');
  expect(d.permitted).not.toHaveBeenCalled();
  expect(d.token).not.toHaveBeenCalled();
});

test('not signed in to a household, or notifications refused: nothing registered', async () => {
  const out = deps({ signedIn: jest.fn(() => false) });
  expect(await createPushToken(out).sync()).toBe('signed_out');
  expect(out.token).not.toHaveBeenCalled();

  const denied = deps({ permitted: jest.fn(async () => false) });
  expect(await createPushToken(denied).sync()).toBe('denied');
  expect(denied.register).not.toHaveBeenCalled();
});

test('a hub or token failure is reported, never thrown', async () => {
  const d = deps({ register: jest.fn(async () => Promise.reject(new Error('offline'))) });
  expect(await createPushToken(d).sync()).toBe('failed');
  const t = deps({ token: jest.fn(async () => Promise.reject(new Error('no FCM'))) });
  expect(await createPushToken(t).sync()).toBe('failed');
});

test('forget removes this phone’s token from the hub', async () => {
  const d = deps();
  await createPushToken(d).forget();
  expect(d.unregister).toHaveBeenCalledWith('ExponentPushToken[abc]');
  // Nothing to remove without a project, or signed out; never throws.
  const off = deps({ projectId: jest.fn(() => undefined) });
  await createPushToken(off).forget();
  expect(off.unregister).not.toHaveBeenCalled();
  const failing = deps({ unregister: jest.fn(async () => Promise.reject(new Error('offline'))) });
  await expect(createPushToken(failing).forget()).resolves.toBeUndefined();
});
