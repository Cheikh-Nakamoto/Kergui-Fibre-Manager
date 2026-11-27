import { allows, canUse, FEATURES, includes, requiredTier, type Feature } from '../licensing/gate';

// The RFC-006 table, as the hub's domain/tier.go has it.
const TABLE: Record<Feature, 'free' | 'basic' | 'max'> = {
  connect: 'free',
  device_list: 'free',
  rename: 'free',
  journal: 'free',
  alerts: 'free',
  current_month_consumption: 'free',
  network_on: 'free',
  unblock: 'free',
  restore_wifi_access: 'free',
  wifi_unlock: 'free',
  block: 'basic',
  cut_wifi_access: 'basic',
  consumption_history: 'max',
  monthly_filter: 'max',
  ssid_manage: 'max',
  wifi_password: 'max',
  auto_reject: 'max',
  wifi_lock: 'max',
};

test('every feature needs the tier the hub says', () => {
  expect([...FEATURES].sort()).toEqual(Object.keys(TABLE).sort());
  for (const f of FEATURES) expect(requiredTier(f)).toBe(TABLE[f]);
});

test('Max includes Basic includes Free', () => {
  expect(includes('max', 'basic')).toBe(true);
  expect(includes('basic', 'free')).toBe(true);
  expect(includes('basic', 'max')).toBe(false);
  expect(includes('free', 'basic')).toBe(false);
});

test('each tier allows exactly its features and those below', () => {
  for (const f of FEATURES) {
    expect(allows('free', f)).toBe(TABLE[f] === 'free');
    expect(allows('basic', f)).toBe(TABLE[f] !== 'max');
    expect(allows('max', f)).toBe(true);
  }
});

test('giving access back is free forever', () => {
  for (const f of ['unblock', 'restore_wifi_access', 'network_on', 'wifi_unlock'] as const) {
    expect(canUse(f, { demo: false, tier: 'free' })).toBe(true);
  }
});

test('an unknown feature or tier fails closed', () => {
  expect(allows('max', 'teleport' as Feature)).toBe(false);
  expect(allows('gold' as 'max', 'block')).toBe(false);
  expect(requiredTier('teleport' as Feature)).toBeNull();
});

test('the demo box is fully open', () => {
  for (const f of FEATURES) expect(canUse(f, { demo: true, tier: 'free' })).toBe(true);
  expect(canUse('block', { demo: false, tier: 'free' })).toBe(false);
  expect(canUse('block', { demo: false, tier: 'basic' })).toBe(true);
  expect(canUse('ssid_manage', { demo: false, tier: 'basic' })).toBe(false);
});
