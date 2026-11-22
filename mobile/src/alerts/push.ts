import Constants from 'expo-constants';
import * as Notifications from 'expo-notifications';
import { Platform } from 'react-native';

import { useAccount } from '../licensing/account';
import { hub } from '../licensing/hub';
import { withHousehold } from '../licensing/household';

// The app's push token (issue #49, RFC-005 Alerts). The hub pushes a new
// device to every installation of the household that registered one, through
// Expo's push service (FCM on Android, APNs on iOS), so the alert arrives
// with the app closed and opens the device sheet (taps.ts). The token is an
// Expo push token for the build's EAS project: a build without one
// (config/push.js) registers nothing, and the alerts then reach the app only
// while it runs, as before.

export type PushResult = 'registered' | 'not_configured' | 'signed_out' | 'denied' | 'failed';

export type PushDeps = {
  projectId: () => string | undefined;
  platform: 'android' | 'ios';
  /** Notification permission granted (asked for by enableAlerts). */
  permitted: () => Promise<boolean>;
  token: (projectId: string) => Promise<string>;
  /** Signed in to a hub household. */
  signedIn: () => boolean;
  register: (push: { token: string; platform: 'android' | 'ios' }) => Promise<void>;
  unregister: (token: string) => Promise<void>;
};

export function createPushToken(d: PushDeps) {
  return {
    /** Registers this phone's token on the hub. Never throws: push is best effort. */
    sync: async (): Promise<PushResult> => {
      const projectId = d.projectId();
      if (!projectId) return 'not_configured';
      if (!d.signedIn()) return 'signed_out';
      try {
        if (!(await d.permitted())) return 'denied';
        await d.register({ token: await d.token(projectId), platform: d.platform });
        return 'registered';
      } catch {
        return 'failed';
      }
    },
    /** Removes it (alerts turned off). Best effort too. */
    forget: async (): Promise<void> => {
      const projectId = d.projectId();
      if (!projectId || !d.signedIn()) return;
      try {
        await d.unregister(await d.token(projectId));
      } catch {
        // The hub forgets a token Expo calls gone; nothing more to do here.
      }
    },
  };
}

type Extra = { eas?: { projectId?: string } };

export const pushToken = createPushToken({
  projectId: () => (Constants.expoConfig?.extra as Extra | undefined)?.eas?.projectId || undefined,
  platform: Platform.OS === 'ios' ? 'ios' : 'android',
  permitted: async () => (await Notifications.getPermissionsAsync()).status === 'granted',
  token: async (projectId) => (await Notifications.getExpoPushTokenAsync({ projectId })).data,
  signedIn: () => !!useAccount.getState().account,
  register: (push) => withHousehold((tok, tenantId) => hub.putPushToken(tok, tenantId, push)),
  unregister: (token) => withHousehold((tok, tenantId) => hub.deletePushToken(tok, tenantId, token)),
});
