/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The new-device notification's "Accepter" (#35): it accepts the device
// under auto-reject without opening the app; a plain tap opens its sheet.
// With auto-reject on, a new-device notification carries the action.
jest.mock('expo-notifications', () => ({
  AndroidImportance: { HIGH: 4 },
  setNotificationHandler: jest.fn(),
  setNotificationCategoryAsync: jest.fn(async () => null),
  scheduleNotificationAsync: jest.fn(async () => 'id'),
}));
jest.mock('expo-background-task', () => ({ BackgroundTaskResult: { Success: 1, Failed: 2 } }));
jest.mock('expo-task-manager', () => ({ defineTask: jest.fn() }));
jest.mock('expo-secure-store', () => ({
  getItemAsync: jest.fn(async (k: string) => (k === 'kergui.alerts.enabled' ? '1' : k === 'kergui.alerts.known' ? '[]' : null)),
  setItemAsync: jest.fn(async () => undefined),
  deleteItemAsync: jest.fn(async () => undefined),
}));
jest.mock('react-native', () => ({ Platform: { OS: 'ios' } }));

import * as Notifications from 'expo-notifications';

import KerguiCore from '../../modules/kergui-core';
import { APPROVE_ACTION, APPROVE_CATEGORY, checkNewDevices, enforceAutoReject, handleAlertResponse } from '../alerts/alerts';
import type { Device } from '../core/types';

const request = KerguiCore.request as jest.Mock;
const schedule = Notifications.scheduleNotificationAsync as jest.Mock;

function core(autoReject: { enabled: boolean }, refused: { mac: string; name: string }[] = []) {
  request.mockImplementation(async (method: string, path: string) => {
    if (method === 'GET' && path === '/api/autoreject') return { status: 200, body: JSON.stringify({ ...autoReject, devices: [] }) };
    if (method === 'POST' && path === '/api/autoreject/run')
      return { status: 200, body: JSON.stringify({ enabled: autoReject.enabled, entitled: true, refused, pending: 0 }) };
    if (method === 'POST' && path.endsWith('/accept')) return { status: 200, body: '{}' };
    return { status: 404, body: '{}' };
  });
}

beforeEach(() => {
  request.mockReset();
  schedule.mockClear();
});

test('"Accepter" accepts the device and opens nothing', async () => {
  core({ enabled: true });
  await expect(handleAlertResponse(APPROVE_ACTION, 'aa:bb:cc:00:00:01')).resolves.toBeNull();
  expect(request).toHaveBeenCalledWith('POST', '/api/devices/aa%3Abb%3Acc%3A00%3A00%3A01/accept', '');
});

test('a tap opens the device sheet', async () => {
  core({ enabled: true });
  await expect(handleAlertResponse('expo.modules.notifications.actions.DEFAULT', 'aa:bb:cc:00:00:01')).resolves.toBe('aa:bb:cc:00:00:01');
  expect(request).not.toHaveBeenCalled();
});

test.each([
  [true, APPROVE_CATEGORY],
  [false, undefined],
])('with auto-reject %s, a new device notification has the category %s', async (enabled, category) => {
  core({ enabled });
  await checkNewDevices([{ MAC: 'aa:bb:cc:00:00:09', Hostname: 'Inconnu', CustomName: '' } as Device]);
  expect(schedule).toHaveBeenCalledTimes(1);
  expect(schedule.mock.calls[0][0].content.categoryIdentifier).toBe(category);
});

test('a device auto-reject refused gets a notification that opens its sheet', async () => {
  core({ enabled: true }, [{ mac: 'aa:bb:cc:00:00:09', name: 'Inconnu' }]);
  await expect(enforceAutoReject()).resolves.toBe(1);
  const content = schedule.mock.calls[0][0].content;
  expect(content.title).toBe('Appareil refusé au Wi-Fi');
  expect(content.body).toContain('Inconnu');
  expect(content.data).toEqual({ mac: 'aa:bb:cc:00:00:09' });
});
