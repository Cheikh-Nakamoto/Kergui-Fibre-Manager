import { router } from 'expo-router';
import { useCallback } from 'react';

import { useNow } from '../lib/useNow';
import { useSession } from '../session/store';
import { currentTier, useEntitlement } from './entitlement';
import { canUse, type Feature } from './gate';

/**
 * `guard(feature, run)` runs `run` when the current tier unlocks the
 * feature, and otherwise opens the Pro screen. Screens call it in front of
 * every paid write; writes that give access back do not need it (they are
 * free in the table anyway).
 */
export function useGate() {
  const demo = useSession((s) => s.session?.mode === 'demo');
  const claims = useEntitlement((s) => s.claims);
  const boxId = useEntitlement((s) => s.boxId);
  const now = useNow();
  const tier = currentTier({ claims, boxId }, now);

  const guard = useCallback(
    (feature: Feature, run: () => void | Promise<void>) => {
      if (canUse(feature, { demo, tier })) return run();
      router.push({ pathname: '/premium', params: { feature } });
    },
    [demo, tier],
  );

  return { tier, demo, guard, can: (f: Feature) => canUse(f, { demo, tier }) };
}
