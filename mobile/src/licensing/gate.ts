// What each Kergui tier unlocks (RFC-006, Tiers). This mirrors the hub's
// Feature -> Tier table in internal/hub/licensing/domain/tier.go; keep the
// two in step. The one rule behind it: anything that CUTS a connection is
// paid, anything that gives one back stays free forever, so a household whose
// pass lapsed is never stuck with a blocked phone or a network left off.

export type Tier = 'free' | 'basic' | 'max';
export type PaidTier = Exclude<Tier, 'free'>;

const FEATURE_TIER = {
  // Free forever: they create the need to block, or give access back.
  connect: 'free',
  device_list: 'free',
  rename: 'free',
  journal: 'free',
  alerts: 'free',
  current_month_consumption: 'free',
  network_on: 'free',
  unblock: 'free',
  restore_wifi_access: 'free',
  // « Ouvrir mon Wi-Fi » (RFC-007 D8), even with a pass that lapsed.
  wifi_unlock: 'free',

  // Basic.
  block: 'basic',
  cut_wifi_access: 'basic',

  // Max.
  consumption_history: 'max',
  monthly_filter: 'max',
  ssid_manage: 'max',
  wifi_password: 'max',
  auto_reject: 'max',
  // « Fermer mon Wi-Fi », « Ajouter un nouvel appareil », « Encore 5 min ».
  wifi_lock: 'max',
} as const satisfies Record<string, Tier>;

export type Feature = keyof typeof FEATURE_TIER;
export const FEATURES = Object.keys(FEATURE_TIER) as readonly Feature[];

const RANK: Record<string, number> = { free: 1, basic: 2, max: 3 };

/** Whether everything `other` unlocks, `t` unlocks too. */
export function includes(t: Tier, other: Tier): boolean {
  const a = RANK[t];
  const b = RANK[other];
  return !!a && !!b && a >= b;
}

/** The lowest tier that unlocks the feature; null for an unknown one. */
export function requiredTier(f: Feature): Tier | null {
  return Object.prototype.hasOwnProperty.call(FEATURE_TIER, f) ? FEATURE_TIER[f] : null;
}

/** Whether `tier` unlocks `f`. Unknown features fail closed. */
export function allows(tier: Tier, f: Feature): boolean {
  const min = requiredTier(f);
  return min !== null && includes(tier, min);
}

/**
 * Whether the feature can be used now. The demo box is simulated, so
 * everything is open there: it is how people (and store reviewers) see what
 * Pro does.
 */
export function canUse(f: Feature, ctx: { demo: boolean; tier: Tier }): boolean {
  return ctx.demo || allows(ctx.tier, f);
}
