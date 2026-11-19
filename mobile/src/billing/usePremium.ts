import { router } from 'expo-router';
import { useCallback } from 'react';

import { useSession } from '../session/store';
import { canUse, type PaidAction } from './gate';
import { useBilling } from './store';

/**
 * `guard(action, run)` runs `run` when the action is unlocked, and otherwise
 * opens the Premium screen instead. Screens call it in front of every paid
 * write; free writes (unblock, filter off, network on) do not go through it.
 */
export function usePremium() {
  const demo = useSession((s) => s.session?.mode === 'demo');
  const premium = useBilling((s) => s.premium);

  const guard = useCallback(
    (action: PaidAction, run: () => void | Promise<void>) => {
      if (canUse(action, { demo, premium })) return run();
      router.push('/premium');
    },
    [demo, premium],
  );

  return { premium, demo, guard };
}
