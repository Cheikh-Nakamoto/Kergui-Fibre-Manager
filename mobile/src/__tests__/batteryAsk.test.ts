/* eslint-disable import/first -- the mocks must come before the modules they replace */
// REQUEST_IGNORE_BATTERY_OPTIMIZATIONS (#37): the exemption is asked for
// only right after the user turned the alerts on, and only once an
// explanation was shown and accepted.
const mockEnable = jest.fn();
const mockRestricted = jest.fn();
const mockAsk = jest.fn();
jest.mock('../alerts/alerts', () => ({
  enableAlerts: (d: unknown) => mockEnable(d),
  batteryRestricted: () => mockRestricted(),
  askBatteryUnrestricted: () => mockAsk(),
}));
jest.mock('../alerts/push', () => ({ pushToken: { sync: async () => undefined } }));
const mockConfirm = jest.fn();
jest.mock('../ui/confirm', () => ({ confirm: (m: string, label?: string) => mockConfirm(m, label) }));

import { switchAlertsOn } from '../alerts/switchOn';
import { fr } from '../i18n/fr';

beforeEach(() => {
  mockEnable.mockReset().mockResolvedValue(true);
  mockRestricted.mockReset().mockReturnValue(true);
  mockAsk.mockReset().mockResolvedValue(undefined);
  mockConfirm.mockReset().mockResolvedValue(true);
});

test('alerts on, battery restricted: the explanation first, then the system prompt', async () => {
  expect(await switchAlertsOn()).toBe(true);
  expect(mockConfirm).toHaveBeenCalledTimes(1);
  expect(mockConfirm.mock.calls[0][0]).toBe(fr['watch.batteryExplain']);
  expect(mockAsk).toHaveBeenCalledTimes(1);
  expect(mockConfirm.mock.invocationCallOrder[0]).toBeLessThan(mockAsk.mock.invocationCallOrder[0]);
});

test('the explanation declined: Android is not asked', async () => {
  mockConfirm.mockResolvedValue(false);
  expect(await switchAlertsOn()).toBe(true);
  expect(mockAsk).not.toHaveBeenCalled();
});

test('not restricted, or the alerts refused: nothing is asked', async () => {
  mockRestricted.mockReturnValue(false);
  await switchAlertsOn();
  mockRestricted.mockReturnValue(true);
  mockEnable.mockResolvedValue(false);
  expect(await switchAlertsOn()).toBe(false);
  expect(mockConfirm).not.toHaveBeenCalled();
  expect(mockAsk).not.toHaveBeenCalled();
});
