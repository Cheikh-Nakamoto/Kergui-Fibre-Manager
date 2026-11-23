/* eslint-disable import/first -- the mocks must come before the modules they replace */
// A new-device notification opens the device sheet (issue #49): a tap while
// the app runs, and the hub's push that launched a closed app — handled
// once the screens are up, and once only.
const mockListeners: ((r: unknown) => void)[] = [];
const mockLast = { current: null as unknown };
jest.mock('expo-notifications', () => ({
  addNotificationResponseReceivedListener: (fn: (r: unknown) => void) => {
    mockListeners.push(fn);
    return { remove: () => mockListeners.splice(mockListeners.indexOf(fn), 1) };
  },
  getLastNotificationResponse: () => mockLast.current,
}));
const mockPush = jest.fn();
jest.mock('expo-router', () => ({ router: { push: (to: unknown) => mockPush(to) } }));
const mockHandle = jest.fn();
jest.mock('../alerts/alerts', () => ({
  handleAlertResponse: (action: string, mac: unknown) => mockHandle(action, mac),
  registerApproveCategory: async () => null,
}));

import { act } from 'react';
import { createRoot, type Root } from 'test-renderer';

import { useAlertTaps } from '../alerts/taps';

const DEFAULT = 'expo.modules.notifications.actions.DEFAULT';
const response = (id: string, data: object, action = DEFAULT) => ({
  actionIdentifier: action,
  notification: { request: { identifier: id, content: { data } } },
});
// What the hub pushes (internal/hub/alerts/usecase, push): the MAC opens the sheet.
const HUB_PUSH = { kind: 'new_device', tenantId: 'T1', alertId: 'A1', mac: 'aa:11:22:33:44:55' };

function Probe({ ready }: { ready: boolean }) {
  useAlertTaps(ready);
  return null;
}

let root: Root | null = null;
const settle = async () => {
  for (let i = 0; i < 5; i++) await act(async () => void (await new Promise((ok) => setTimeout(ok, 1))));
};
async function render(ready: boolean) {
  if (!root) root = createRoot();
  await act(async () => root!.render(<Probe ready={ready} />));
  await settle();
}

beforeEach(() => {
  mockListeners.length = 0;
  mockLast.current = null;
  mockPush.mockReset();
  mockHandle.mockReset().mockImplementation(async (action: string, mac: unknown) => (action === DEFAULT && typeof mac === 'string' ? mac : null));
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
});

test('a tap on a new-device notification opens its sheet', async () => {
  await render(true);
  await act(async () => mockListeners.forEach((l) => l(response('n1', HUB_PUSH))));
  await settle();
  expect(mockHandle).toHaveBeenCalledWith(DEFAULT, 'aa:11:22:33:44:55');
  expect(mockPush).toHaveBeenCalledWith({ pathname: '/device/[mac]', params: { mac: 'aa:11:22:33:44:55' } });
});

test('the push that launched the app opens the sheet once the screens are up, once', async () => {
  mockLast.current = response('n2', HUB_PUSH);
  await render(false);
  expect(mockPush).not.toHaveBeenCalled(); // the navigator is not mounted yet
  await render(true);
  expect(mockPush).toHaveBeenCalledTimes(1);
  expect(mockPush).toHaveBeenCalledWith({ pathname: '/device/[mac]', params: { mac: 'aa:11:22:33:44:55' } });
  // The same response reported again by the listener is not opened twice.
  await act(async () => mockListeners.forEach((l) => l(response('n2', HUB_PUSH))));
  await settle();
  expect(mockPush).toHaveBeenCalledTimes(1);
});

test('« Approuver », or a notification with no device, opens nothing', async () => {
  await render(true);
  await act(async () => mockListeners.forEach((l) => l(response('n3', HUB_PUSH, 'approve'))));
  await act(async () => mockListeners.forEach((l) => l(response('n4', { kind: 'other' }))));
  await settle();
  expect(mockPush).not.toHaveBeenCalled();
});
