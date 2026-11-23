/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Who enforces auto-reject (#36): before each round the app asks the hub
// (refreshEnforcer hands the answer to the core). While the household's
// relay enforces, the round touches nothing and nothing rings; otherwise the
// phone refuses as before. On Android the watch service runs the rounds, so
// the open app only keeps the core's answer fresh for it.
jest.mock('expo-notifications', () => ({
  AndroidImportance: { HIGH: 4 },
  setNotificationHandler: jest.fn(),
  setNotificationCategoryAsync: jest.fn(async () => null),
  scheduleNotificationAsync: jest.fn(async () => 'id'),
}));
jest.mock('expo-background-task', () => ({ BackgroundTaskResult: { Success: 1, Failed: 2 } }));
jest.mock('expo-task-manager', () => ({ defineTask: jest.fn() }));
jest.mock('expo-secure-store', () => ({
  getItemAsync: jest.fn(async () => null),
  setItemAsync: jest.fn(async () => undefined),
  deleteItemAsync: jest.fn(async () => undefined),
}));
jest.mock('react-native', () => ({ Platform: { OS: 'ios' } }));
jest.mock('../features/approvals', () => ({ approvals: { approve: jest.fn() } }));
const mockRefresh = jest.fn();
jest.mock('../features/relayPolicy', () => ({ refreshEnforcer: (...a: unknown[]) => mockRefresh(...a) }));

import * as Notifications from 'expo-notifications';

import KerguiCore from '../../modules/kergui-core';
import { enforceAutoReject } from '../alerts/alerts';
import { useInbox } from '../alerts/inbox';

const request = KerguiCore.request as jest.Mock;
const schedule = Notifications.scheduleNotificationAsync as jest.Mock;
const STRANGER = { mac: 'aa:bb:cc:00:00:09', name: 'Inconnu' };

/** The core: a round answers as the hub's last word made it. */
function core(enforcer: 'phone' | 'relay') {
  request.mockImplementation(async (method: string, path: string) => {
    if (method === 'POST' && path === '/api/autoreject/run') {
      const refused = enforcer === 'relay' ? [] : [STRANGER];
      return { status: 200, body: JSON.stringify({ enabled: true, entitled: true, refused, pending: 0, enforcer }) };
    }
    return { status: 404, body: '{}' };
  });
}

const rounds = () => request.mock.calls.filter(([m, p]) => m === 'POST' && p === '/api/autoreject/run').length;

beforeEach(() => {
  request.mockReset();
  schedule.mockClear();
  mockRefresh.mockReset();
  useInbox.setState({ items: [] } as never);
});

test('the hub is asked before the round, and the relay enforcing means nothing is refused here', async () => {
  const order: string[] = [];
  mockRefresh.mockImplementation(async () => {
    order.push('hub');
    return 'relay';
  });
  core('relay');
  request.mockImplementationOnce(async () => {
    order.push('round');
    return { status: 200, body: JSON.stringify({ enabled: true, entitled: true, refused: [], pending: 0, enforcer: 'relay' }) };
  });
  await expect(enforceAutoReject()).resolves.toBe(0);
  expect(order).toEqual(['hub', 'round']);
  expect(schedule).not.toHaveBeenCalled();
});

test('the phone enforces: the stranger is refused and rings, as before', async () => {
  mockRefresh.mockResolvedValue('phone');
  core('phone');
  await expect(enforceAutoReject()).resolves.toBe(1);
  expect(schedule).toHaveBeenCalledTimes(1);
});

test('the hub cannot be asked: the phone still runs its round', async () => {
  mockRefresh.mockRejectedValue(new Error('offline'));
  core('phone');
  await expect(enforceAutoReject()).resolves.toBe(1);
  expect(rounds()).toBe(1);
});

test('the demo never asks the hub', async () => {
  core('phone');
  await enforceAutoReject({ demo: true });
  expect(mockRefresh).not.toHaveBeenCalled();
  expect(rounds()).toBe(1);
});

test('Android with the watch service on: the app keeps the core’s answer fresh, the service runs the round', async () => {
  let enforce: typeof enforceAutoReject = enforceAutoReject;
  let nativeRequest: jest.Mock = request;
  jest.isolateModules(() => {
    // A fresh registry: alerts.ts reads the platform and the watch at load.
    /* eslint-disable @typescript-eslint/no-require-imports */
    (require('react-native') as { Platform: { OS: string } }).Platform.OS = 'android';
    const native = require('../../modules/kergui-core').default as Record<string, unknown>;
    native.startWatch = jest.fn();
    native.watchEnabled = () => true;
    nativeRequest = native.request as jest.Mock;
    enforce = (require('../alerts/alerts') as typeof import('../alerts/alerts')).enforceAutoReject;
    /* eslint-enable @typescript-eslint/no-require-imports */
  });
  mockRefresh.mockResolvedValue('relay');
  await expect(enforce()).resolves.toBe(0);
  expect(mockRefresh).toHaveBeenCalledTimes(1);
  expect(nativeRequest.mock.calls.filter(([m, p]) => m === 'POST' && p === '/api/autoreject/run')).toHaveLength(0);
});
