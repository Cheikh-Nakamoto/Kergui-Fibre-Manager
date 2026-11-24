import type { Device } from '../core/types';
import { t } from '../i18n';
import { confirm } from '../ui/confirm';
import { askBatteryUnrestricted, batteryRestricted, enableAlerts } from './alerts';
import { pushToken } from './push';

/**
 * Turn the new-device alerts on (asks for the permission). With the app
 * closed, the hub's push brings them (#49): the token is registered too.
 * False if the permission is refused. Réglages' switch and the auto-reject
 * screen (#76: the watch service runs the rule) share it.
 */
export async function switchAlertsOn(devices?: Device[]): Promise<boolean> {
  const ok = await enableAlerts(devices);
  if (!ok) return false;
  void pushToken.sync();
  // The watch must keep running with the app closed: Android's battery
  // optimisation stops it. The exemption (REQUEST_IGNORE_BATTERY_OPTIMIZATIONS,
  // #37) is asked for only here, right after the person turned the alerts
  // on, and only once they read why and agreed.
  if (batteryRestricted() && (await confirm(t('watch.batteryExplain'), t('watch.batteryAllow')))) {
    await askBatteryUnrestricted().catch(() => undefined);
  }
  return true;
}
