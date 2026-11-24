import { useEffect } from 'react';
import { AppState } from 'react-native';

import { useUpdate } from './update';

/**
 * Checks for a new website APK at launch and whenever the app comes back to
 * the front (the store keeps it to once every 6 h), and carries on with the
 * install when the household comes back from the permission's settings page.
 */
export function useSelfUpdate(enabled: boolean) {
  useEffect(() => {
    if (!enabled) return;
    void useUpdate.getState().check({ launch: true });
    const sub = AppState.addEventListener('change', (s) => {
      if (s !== 'active') return;
      const u = useUpdate.getState();
      if (u.permission === 'asked') void u.resume();
      else void u.check();
    });
    return () => sub.remove();
  }, [enabled]);
}
