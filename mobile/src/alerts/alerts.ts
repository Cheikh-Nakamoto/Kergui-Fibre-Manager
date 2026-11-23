import * as BackgroundTask from 'expo-background-task';
import * as Notifications from 'expo-notifications';
import { AndroidImportance } from 'expo-notifications';
import * as SecureStore from 'expo-secure-store';
import * as TaskManager from 'expo-task-manager';
import { Platform } from 'react-native';

import KerguiCore from '../../modules/kergui-core';
import { api } from '../core/client';
import { approvals } from '../features/approvals';
import type { Device } from '../core/types';
import { t } from '../i18n';
import { displayName } from '../lib/format';
import { useArrivals } from './arrivals';
import { arrivals } from './diff';

const TASK = 'kergui.new-device-check';
const ENABLED_KEY = 'kergui.alerts.enabled';
const KNOWN_KEY = 'kergui.alerts.known';
/** Who was on the Wi-Fi at the last reading (#58), the demo's apart. */
const PRESENT_KEY = 'kergui.alerts.present';
const presentKey = (demo: boolean) => `${PRESENT_KEY}.${demo ? 'demo' : 'router'}`;
const CHANNEL = 'new-devices';
/**
 * The notification category whose « Approuver » approves the device (#58):
 * it never alerts again, and auto-reject spares it. The hub's push carries
 * the same category.
 */
export const APPROVE_CATEGORY = 'new-device-approve';
export const APPROVE_ACTION = 'approve';

// A box id is not needed: one app talks to one box at a time, and signing out
// calls forget() so the next box starts from a clean first reading.

export async function alertsEnabled(): Promise<boolean> {
  return (await SecureStore.getItemAsync(ENABLED_KEY)) === '1';
}

async function readPresent(demo: boolean): Promise<string[] | null> {
  const raw = await SecureStore.getItemAsync(presentKey(demo));
  if (!raw) return null;
  try {
    return JSON.parse(raw) as string[];
  } catch {
    return null;
  }
}

/** Forget the devices seen so far: the next reading is a first reading. */
export async function forgetKnown(): Promise<void> {
  await SecureStore.deleteItemAsync(KNOWN_KEY);
  await SecureStore.deleteItemAsync(presentKey(false));
  useArrivals.getState().clear();
}

// Android watches from a native foreground service (WatchService), which
// keeps polling the box with the app closed. The JS check below then stays
// silent, or every new device would ring twice.
const nativeWatch = Platform.OS === 'android' && typeof KerguiCore.startWatch === 'function';

/**
 * Starts (or restarts) the Android watch for the current box. resetKnown
 * makes its next reading a silent first reading. No-op in demo mode.
 */
export async function startWatch(resetKnown: boolean): Promise<void> {
  if (!nativeWatch) return;
  const { useSession, vaultKey } = await import('../session/store');
  const session = useSession.getState().session;
  if (session?.mode !== 'router') return;
  await KerguiCore.startWatch!(
    {
      vaultKey: await vaultKey(),
      routerUrl: session.routerUrl,
      pin: session.pin,
      intervalSec: 30,
      ongoingTitle: t('watch.title'),
      ongoingText: t('watch.starting'),
      ongoingCountText: t('watch.count'),
      ongoingErrorText: t('watch.unreachable'),
      ongoingChannel: t('watch.channel'),
      alertTitle: t('alerts.title'),
      alertBody: t('alerts.body', { name: '{name}', mac: '{mac}' }),
      alertChannel: t('alerts.channel'),
      approveText: t('alerts.approve'),
      refusedTitle: t('alerts.refusedTitle'),
      refusedBody: t('alerts.refusedBody', { name: '{name}', mac: '{mac}' }),
    },
    resetKnown,
  );
}

/** Restarts the watch at launch if it was on: covers updates from older builds. */
export async function resumeWatch(): Promise<void> {
  if (!nativeWatch || !(await alertsEnabled())) return;
  await startWatch(!KerguiCore.watchEnabled!()).catch(() => undefined);
}

/** True when Android may still put the app to sleep and stop the watch. */
export function batteryRestricted(): boolean {
  return nativeWatch && KerguiCore.batteryUnrestricted?.() === false;
}

export async function askBatteryUnrestricted(): Promise<void> {
  await KerguiCore.requestBatteryUnrestricted?.();
}

Notifications.setNotificationHandler({
  handleNotification: async () => ({
    shouldShowBanner: true,
    shouldShowList: true,
    shouldPlaySound: true,
    shouldSetBadge: false,
  }),
});

// « Approuver » on a new-device notification raised from JS or pushed by
// the hub (the Android watch service has its own). Best effort: no
// category, no button.
let categoryReady: Promise<unknown> | null = null;
export function registerApproveCategory(): Promise<unknown> {
  categoryReady ??= Notifications.setNotificationCategoryAsync(APPROVE_CATEGORY, [
    { identifier: APPROVE_ACTION, buttonTitle: t('alerts.approve'), options: { opensAppToForeground: false } },
  ]).catch(() => null);
  return categoryReady;
}

/**
 * Compare one device list with the last reading: each device nobody approved
 * that arrived since (#58) is signalled in the app, and rings where the
 * Android watch service does not (iOS, the demo, the background task).
 * Returns how many arrived. Called from the screen that just read the list,
 * and from the background task. The demo keeps its own last reading.
 */
export async function checkNewDevices(devices: Device[], { demo = false }: { demo?: boolean } = {}): Promise<number> {
  const { fresh, next } = arrivals(await readPresent(demo), devices);
  await SecureStore.setItemAsync(presentKey(demo), JSON.stringify(next));
  if (!fresh.length) return 0;
  useArrivals.getState().add(fresh.map((d) => ({ mac: d.MAC.toLowerCase(), name: displayName(d) })));
  if ((nativeWatch && !demo) || !(await alertsEnabled())) return fresh.length;
  await registerApproveCategory();
  for (const d of fresh) {
    await Notifications.scheduleNotificationAsync({
      content: {
        title: t('alerts.title'),
        body: t('alerts.body', { name: displayName(d), mac: d.MAC }),
        data: { mac: d.MAC },
        categoryIdentifier: APPROVE_CATEGORY,
        ...(Platform.OS === 'android' ? { channelId: CHANNEL } : {}),
      },
      trigger: null,
    });
  }
  return fresh.length;
}

/**
 * One round of auto-reject (Max) where the Android watch service is not
 * running it: iOS, the demo, alerts turned off, and the background task.
 * Each device it refused gets a notification that opens its sheet. Returns
 * how many were refused.
 */
export async function enforceAutoReject({ demo = false }: { demo?: boolean } = {}): Promise<number> {
  if (nativeWatch && !demo && KerguiCore.watchEnabled?.() === true) return 0;
  const run = await api.runAutoReject();
  for (const d of run.refused) {
    await Notifications.scheduleNotificationAsync({
      content: {
        title: t('alerts.refusedTitle'),
        body: t('alerts.refusedBody', { name: d.name || d.mac, mac: d.mac }),
        data: { mac: d.mac },
        ...(Platform.OS === 'android' ? { channelId: CHANNEL } : {}),
      },
      trigger: null,
    }).catch(() => undefined);
  }
  return run.refused.length;
}

/**
 * What a tap on an alert does: « Approuver » approves the device (#58) and
 * accepts it under auto-reject, anything else opens its sheet. Returns the
 * MAC to open, or null.
 */
export async function handleAlertResponse(actionIdentifier: string, mac: unknown): Promise<string | null> {
  if (typeof mac !== 'string') return null;
  if (actionIdentifier === APPROVE_ACTION) {
    await approvals.approve(mac).catch(() => undefined);
    useArrivals.getState().dismiss(mac);
    // Off, or a box without Wi-Fi access control: nothing to accept.
    await api.acceptDevice(mac).catch(() => undefined);
    return null;
  }
  return mac;
}

// Background: the OS wakes the app now and then (Android roughly every 15 min
// or more, iOS when it decides). The task only works on the box's own Wi-Fi.
TaskManager.defineTask(TASK, async () => {
  try {
    // Imported here: the session store pulls in the native core, which only
    // a running JS context can open.
    const { useSession } = await import('../session/store');
    const s = useSession.getState();
    if (!s.ready) await s.boot();
    if (useSession.getState().session?.mode !== 'router') return BackgroundTask.BackgroundTaskResult.Success;
    await checkNewDevices(await api.devices());
    await enforceAutoReject().catch(() => 0);
    // The Conso tab counts only what was sampled: take one while awake.
    await api.sampleUsage().catch(() => undefined);
    return BackgroundTask.BackgroundTaskResult.Success;
  } catch {
    // The box is unreachable (phone off the home Wi-Fi): not an error worth
    // retrying harder than the next wake-up.
    return BackgroundTask.BackgroundTaskResult.Failed;
  }
});

/** Ask for the permission and turn the alerts on. False if refused. */
export async function enableAlerts(devices?: Device[]): Promise<boolean> {
  if (Platform.OS === 'android') {
    await Notifications.setNotificationChannelAsync(CHANNEL, {
      name: t('alerts.channel'),
      importance: AndroidImportance.HIGH,
    });
  }
  let { status } = await Notifications.getPermissionsAsync();
  if (status !== 'granted') status = (await Notifications.requestPermissionsAsync()).status;
  if (status !== 'granted') return false;
  await SecureStore.setItemAsync(ENABLED_KEY, '1');
  // Record what is on the box now, so only later arrivals ring.
  await forgetKnown();
  await registerApproveCategory();
  if (nativeWatch) {
    await startWatch(true);
    if (batteryRestricted()) await askBatteryUnrestricted().catch(() => undefined);
    return true;
  }
  if (devices) await checkNewDevices(devices);
  await BackgroundTask.registerTaskAsync(TASK, { minimumInterval: 15 }).catch(() => undefined);
  return true;
}

export async function disableAlerts(): Promise<void> {
  await SecureStore.setItemAsync(ENABLED_KEY, '0');
  if (nativeWatch) await KerguiCore.stopWatch!(true).catch(() => undefined);
  await BackgroundTask.unregisterTaskAsync(TASK).catch(() => undefined);
}
