import * as Notifications from 'expo-notifications';
import { router } from 'expo-router';
import { useEffect, useRef } from 'react';

import { handleAlertResponse, registerApproveCategory } from './alerts';

// A tap on a new-device alert opens the device, where it can be blocked or
// given its access back; « Approuver » approves it instead (#58), on this
// phone's alerts and on the hub's push, whose category is registered here.
// The alert comes from this phone (the Android watch, the background check)
// or from the hub's push (issue #49), which carries the device's MAC in its
// data too. A push tapped while the app was closed launches it: that
// response is read at start and handled once the screens are mounted
// (`ready`), and a response is never handled twice.

type Response = Notifications.NotificationResponse;

/** What a pending tap opens when it is an announcement, not a device. */
const ANNOUNCEMENTS = 'announcements';

export function useAlertTaps(ready: boolean) {
  const seen = useRef(new Set<string>());
  const pending = useRef<string | null>(null);
  const readyRef = useRef(ready);
  useEffect(() => {
    readyRef.current = ready;
  }, [ready]);

  const open = (mac: string) => {
    if (readyRef.current) router.push({ pathname: '/device/[mac]', params: { mac } });
    else pending.current = mac;
  };

  const handle = (r: Response | null) => {
    if (!r) return;
    const id = r.notification.request.identifier;
    if (seen.current.has(id)) return;
    seen.current.add(id);
    // An announcement of the Kergui team (#80) opens the notifications screen.
    if (r.notification.request.content.data?.kind === 'announcement') {
      if (readyRef.current) router.push('/notifications');
      else pending.current = ANNOUNCEMENTS;
      return;
    }
    void handleAlertResponse(r.actionIdentifier, r.notification.request.content.data?.mac).then((mac) => {
      if (mac) open(mac);
    });
  };

  useEffect(() => {
    void registerApproveCategory();
    handle(Notifications.getLastNotificationResponse());
    const sub = Notifications.addNotificationResponseReceivedListener(handle);
    return () => sub.remove();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!ready || !pending.current) return;
    const mac = pending.current;
    pending.current = null;
    if (mac === ANNOUNCEMENTS) router.push('/notifications');
    else router.push({ pathname: '/device/[mac]', params: { mac } });
  }, [ready]);
}
