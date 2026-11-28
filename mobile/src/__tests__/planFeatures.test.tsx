/* eslint-disable import/first -- the mocks must come before the modules they replace */
// The box's features (owner request, 2026-11-23): the box's level, what it
// includes and what stays locked, from the gate's own table.
import { act } from 'react';
import { PaperProvider } from 'react-native-paper';
import { createRoot, type Root, type TestInstance } from 'test-renderer';

let mockGate = { tier: 'basic', demo: false };
jest.mock('../licensing/useGate', () => {
  const { canUse } = jest.requireActual('../licensing/gate');
  return { useGate: () => ({ ...mockGate, can: (f: string) => canUse(f, mockGate) }) };
});

import { PlanFeatures } from '../ui/PlanFeatures';

async function open(): Promise<Root> {
  const r = createRoot();
  await act(async () => r.render(<PaperProvider><PlanFeatures /></PaperProvider>));
  const nodes = r.container.queryAll((n: TestInstance) => n.props.testID === 'settings-plan');
  if (!nodes.length) throw new Error('no settings-plan');
  // The accordion's pressable exposes the press as onClick in the renderer.
  const press = nodes.map((n) => n.props.onPress ?? n.props.onClick).find((f) => typeof f === 'function');
  if (!press) throw new Error('settings-plan cannot be pressed');
  await act(async () => press());
  return r;
}

const ids = (r: Root) =>
  r.container.queryAll((n: TestInstance) => typeof n.props.testID === 'string' && /^plan-.*-(on|off)$/.test(n.props.testID)).map((n: TestInstance) => n.props.testID as string);

test('Basic: free and Basic on, Max locked', async () => {
  mockGate = { tier: 'basic', demo: false };
  const r = await open();
  const shown = new Set(ids(r));
  expect(shown).toContain('plan-device_list-on');
  expect(shown).toContain('plan-block-on');
  expect(shown).toContain('plan-cut_wifi_access-on');
  expect(shown).toContain('plan-auto_reject-off');
  expect(shown).toContain('plan-ssid_manage-off');
  // « Réserver le Wi-Fi à la maison » (#108) is Max.
  expect(shown).toContain('plan-wifi_lock-off');
});

test('Free: only the free features on; unblocking stays on', async () => {
  mockGate = { tier: 'free', demo: false };
  const shown = new Set(ids(await open()));
  expect(shown).toContain('plan-unblock-on');
  expect(shown).toContain('plan-block-off');
  expect(shown).toContain('plan-consumption_history-off');
});

test('the demo opens everything', async () => {
  mockGate = { tier: 'free', demo: true };
  expect(ids(await open()).every((id) => id.endsWith('-on'))).toBe(true);
});
