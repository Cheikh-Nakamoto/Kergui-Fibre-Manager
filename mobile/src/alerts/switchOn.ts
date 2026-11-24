import type { Device } from '../core/types';
import { enableAlerts } from './alerts';
import { pushToken } from './push';

/**
 * Turn the new-device alerts on (asks for the permission). With the app
 * closed, the hub's push brings them (#49): the token is registered too.
 * False if the permission is refused. Réglages' switch and the auto-reject
 * screen (#76: the watch service runs the rule) share it.
 */
export async function switchAlertsOn(devices?: Device[]): Promise<boolean> {
  const ok = await enableAlerts(devices);
  if (ok) void pushToken.sync();
  return ok;
}
