/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The new-device notification (#58): it says the device is not approved and
// offers « Approuver », which approves it (it never alerts again) and accepts
// it under auto-reject, without opening the app; a plain tap opens its
// sheet. Each arrival of a device nobody approved rings, and is signalled in
// the app too.
const mockStore = new Map<string, string>();
jest.mock('expo-notifications', () => ({
  AndroidImportance: { HIGH: 4 },
  setNotificationHandler: jest.fn(),
  setNotificationCategoryAsync: jest.fn(async () => null),
  scheduleNotificationAsync: jest.fn(async () => 'id'),
}));
jest.mock('expo-background-task', () => ({ BackgroundTaskResult: { Success: 1, Failed: 2 } }));
jest.mock('expo-task-manager', () => ({ defineTask: jest.fn() }));
jest.mock('expo-secure-store', () => ({
  getItemAsync: jest.fn(async (k: string) => mockStore.get(k) ?? null),
  setItemAsync: jest.fn(async (k: string, v: string) => void mockStore.set(k, v)),
  deleteItemAsync: jest.fn(async (k: string) => void mockStore.delete(k)),
}));
jest.mock('react-native', () => ({ Platform: { OS: 'ios' } }));
// The household's approvals (approvals.test.ts): here, the core's alone.
const mockApprove = jest.fn(async (mac: string) => {
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  await require('../core/client').api.approve(mac);
});
jest.mock('../features/approvals', () => ({ approvals: { approve: (mac: string) => mockApprove(mac) } }));
// Who enforces (#36, autoRejectElection.test.ts): here, always the phone.
jest.mock('../features/relayPolicy', () => ({ refreshEnforcer: async () => 'phone' }));

import * as Notifications from 'expo-notifications';

import KerguiCore from '../../modules/kergui-core';
import { APPROVE_ACTION, APPROVE_CATEGORY, checkNewDevices, enforceAutoReject, handleAlertResponse } from '../alerts/alerts';
import { useArrivals } from '../alerts/arrivals';
import type { Device } from '../core/types';
import { t } from '../i18n';

const request = KerguiCore.request as jest.Mock;
const schedule = Notifications.scheduleNotificationAsync as jest.Mock;
const category = Notifications.setNotificationCategoryAsync as jest.Mock;

function core(autoReject: { enabled: boolean }, refused: { mac: string; name: string }[] = []) {
  request.mockImplementation(async (method: string, path: string) => {
    if (method === 'GET' && path === '/api/autoreject') return { status: 200, body: JSON.stringify({ ...autoReject, devices: [] }) };
    if (method === 'POST' && path === '/api/autoreject/run')
      return { status: 200, body: JSON.stringify({ enabled: autoReject.enabled, entitled: true, refused, pending: 0 }) };
    if (method === 'POST' && path.endsWith('/accept')) return { status: 200, body: '{}' };
    if (method === 'PUT' && path.startsWith('/api/approvals/')) return { status: 200, body: '{}' };
    return { status: 404, body: '{}' };
  });
}

const visitor = (extra: Partial<Device> = {}) =>
  ({ MAC: 'aa:bb:cc:00:00:09', Hostname: 'Inconnu', CustomName: '', Connected: true, Approved: false, Blocked: false, ...extra }) as Device;

beforeEach(() => {
  request.mockReset();
  schedule.mockClear();
  category.mockClear();
  mockStore.clear();
  mockStore.set('kergui.alerts.enabled', '1');
  useArrivals.getState().clear();
});

test('« Approuver » approves the device, accepts it under auto-reject and opens nothing', async () => {
  core({ enabled: true });
  await expect(handleAlertResponse(APPROVE_ACTION, 'aa:bb:cc:00:00:01')).resolves.toBeNull();
  expect(mockApprove).toHaveBeenCalledWith('aa:bb:cc:00:00:01');
  expect(request).toHaveBeenCalledWith('PUT', '/api/approvals/aa%3Abb%3Acc%3A00%3A00%3A01', '');
  expect(request).toHaveBeenCalledWith('POST', '/api/devices/aa%3Abb%3Acc%3A00%3A00%3A01/accept', '');
});

test('a tap opens the device sheet', async () => {
  core({ enabled: true });
  await expect(handleAlertResponse('expo.modules.notifications.actions.DEFAULT', 'aa:bb:cc:00:00:01')).resolves.toBe('aa:bb:cc:00:00:01');
  expect(request).not.toHaveBeenCalled();
});

test('the « Approuver » button is the category the hub pushes too', async () => {
  core({ enabled: false });
  mockStore.set('kergui.alerts.present.router', '[]');
  await checkNewDevices([visitor()]);
  expect(category).toHaveBeenCalledWith(APPROVE_CATEGORY, [expect.objectContaining({ identifier: APPROVE_ACTION, buttonTitle: 'Approuver' })]);
  const content = schedule.mock.calls[0][0].content;
  expect(content.categoryIdentifier).toBe('new-device-approve');
  expect(content.title).toBe(t('alerts.title'));
  expect(content.title).toMatch(/non approuvé/);
  expect(content.body).toContain('Inconnu');
  expect(content.data).toEqual({ mac: 'aa:bb:cc:00:00:09' });
});

test('each arrival of a device nobody approved rings and is signalled in the app; staying does not', async () => {
  core({ enabled: false });
  await expect(checkNewDevices([])).resolves.toBe(0); // first reading
  await expect(checkNewDevices([visitor()])).resolves.toBe(1);
  expect(useArrivals.getState().items.map((a) => a.mac)).toEqual(['aa:bb:cc:00:00:09']);
  await expect(checkNewDevices([visitor()])).resolves.toBe(0); // still there
  await expect(checkNewDevices([])).resolves.toBe(0); // left
  await expect(checkNewDevices([visitor()])).resolves.toBe(1); // back: rings again
  expect(schedule).toHaveBeenCalledTimes(2);
});

test('an approved device never rings, nor is signalled', async () => {
  core({ enabled: false });
  await checkNewDevices([]);
  await expect(checkNewDevices([visitor({ Approved: true })])).resolves.toBe(0);
  expect(schedule).not.toHaveBeenCalled();
  expect(useArrivals.getState().items).toEqual([]);
});

test('alerts off: no notification, but the app still signals the arrival', async () => {
  core({ enabled: false });
  mockStore.set('kergui.alerts.enabled', '0');
  await checkNewDevices([]);
  await expect(checkNewDevices([visitor()])).resolves.toBe(1);
  expect(schedule).not.toHaveBeenCalled();
  expect(useArrivals.getState().items).toHaveLength(1);
});

test('the demo keeps its own last reading', async () => {
  core({ enabled: false });
  await checkNewDevices([visitor()]);
  // The demo's first reading is silent even though the box's had none of it.
  await expect(checkNewDevices([visitor({ MAC: 'aa:bb:cc:00:00:58' })], { demo: true })).resolves.toBe(0);
});

test('a device auto-reject refused gets a notification that opens its sheet', async () => {
  core({ enabled: true }, [{ mac: 'aa:bb:cc:00:00:09', name: 'Inconnu' }]);
  await expect(enforceAutoReject()).resolves.toBe(1);
  const content = schedule.mock.calls[0][0].content;
  expect(content.title).toBe('Appareil refusé au Wi-Fi');
  expect(content.body).toContain('Inconnu');
  expect(content.data).toEqual({ mac: 'aa:bb:cc:00:00:09' });
});
