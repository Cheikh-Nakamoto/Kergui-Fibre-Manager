import { useCallback } from 'react';
import { Alert } from 'react-native';

import { t } from '../i18n';
import { useNow } from '../lib/useNow';
import { useSession } from '../session/store';
import { currentTier, useEntitlement } from './entitlement';
import { canUse, type Feature } from './gate';

/**
 * `guard(feature, run, title)` runs `run` when the current tier unlocks the
 * feature, and otherwise shows one neutral sentence in an alert titled with
 * the action (« Bloquer »: #120, audit H4). It opens no screen: the Google
 * Play app has no payment surface (RFC-006, decision of 2026-11-21). Screens
 * call it in front of every paid write, and show the lock on the control
 * before the tap (ui/LockedButton); writes that give access back do not need
 * it (they are free in the table anyway).
 */
export function useGate() {
  const demo = useSession((s) => s.session?.mode === 'demo');
  const claims = useEntitlement((s) => s.claims);
  const boxId = useEntitlement((s) => s.boxId);
  const now = useNow();
  const tier = currentTier({ claims, boxId }, now);

  const guard = useCallback(
    (feature: Feature, run: () => void | Promise<void>, title: string) => {
      if (canUse(feature, { demo, tier })) return run();
      Alert.alert(title, t('pro.locked'), [{ text: t('common.ok') }]);
    },
    [demo, tier],
  );

  return { tier, demo, guard, can: (f: Feature) => canUse(f, { demo, tier }) };
}
