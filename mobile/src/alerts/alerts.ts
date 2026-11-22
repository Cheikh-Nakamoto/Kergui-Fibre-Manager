import * as BackgroundTask from 'expo-background-task';
import * as Notifications from 'expo-notifications';
import { AndroidImportance } from 'expo-notifications';
import * as SecureStore from 'expo-secure-store';
import * as TaskManager from 'expo-task-manager';
import { Platform } from 'react-native';

import KerguiCore from '../../modules/kergui-core';
import { api } from '../core/client';
import type { Device } from '../core/types';
import { t } from '../i18n';
import { displayName } from '../lib/format';
import { newDevices } from './diff';

const TASK = 'kergui.new-device-check';
const ENABLED_KEY = 'kergui.alerts.enabled';
const KNOWN_KEY = 'kergui.alerts.known';
const CHANNEL = 'new-devices';

// A box id is not needed: one app talks to one box at a time, and signing out
// calls forget() so the next box starts from a clean first reading.

export async function alertsEnabled(): Promise<boolean> {
  return (await SecureStore.getItemAsync(ENABLED_KEY)) === '1';
}

async function readKnown(): Promise<string[] | null> {
  const raw = await SecureStore.getItemAsync(KNOWN_KEY);
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

/**
 * Compare one device list with the ones already seen and ring for each new
 * device. Returns how many alerts were raised. Called from the screen that
 * just read the list, and from the background task.
 */
export async function checkNewDevices(devices: Device[]): Promise<number> {
  if (nativeWatch || !(await alertsEnabled())) return 0;
  const { fresh, next } = newDevices(await readKnown(), devices);
  await SecureStore.setItemAsync(KNOWN_KEY, JSON.stringify(next));
  for (const d of fresh) {
    await Notifications.scheduleNotificationAsync({
      content: {
        title: t('alerts.title'),
        body: t('alerts.body', { name: displayName(d), mac: d.MAC }),
        data: { mac: d.MAC },
        ...(Platform.OS === 'android' ? { channelId: CHANNEL } : {}),
      },
      trigger: null,
    });
  }
  return fresh.length;
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
