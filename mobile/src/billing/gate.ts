// What Kergui Premium unlocks, and the one rule behind it: anything that CUTS
// a connection is paid; anything that gives one back stays free. A household
// whose subscription lapsed must never be stuck with a blocked phone, a
// filter it cannot turn off or a Wi-Fi network it cannot turn back on.

/** Writes that need Kergui Premium on a real box. */
export type PaidAction =
  | 'block' // put a device on the box's blacklist
  | 'filterOn' // turn the MAC filter on (only allowed devices get in)
  | 'ssidOff'; // switch a Wi-Fi network off

export const PAID_ACTIONS: readonly PaidAction[] = ['block', 'filterOn', 'ssidOff'];

/**
 * Whether the action can run now. The demo box is simulated, so everything
 * is open there: it is how people (and store reviewers) see what Premium does.
 */
export function canUse(_action: PaidAction, ctx: { demo: boolean; premium: boolean }): boolean {
  return ctx.demo || ctx.premium;
}
