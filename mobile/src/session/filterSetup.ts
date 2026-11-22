import { api, core } from '../core/client';
import type { FilterSetupResult } from '../core/types';

/**
 * The setup step after the first sign-in to a box (#56): its MAC filter on,
 * in blocklist mode (an empty list cuts nobody). Never in the user's way: a
 * box without the switch, or one that fails, only leaves a journal line.
 */
export async function setupMACFilter(): Promise<FilterSetupResult | null> {
  try {
    const { result } = await api.setupFilter();
    return result;
  } catch (e) {
    core.note('warn', `filtrage MAC : mise en place ignorée (${e instanceof Error ? e.message : String(e)})`);
    return null;
  }
}
