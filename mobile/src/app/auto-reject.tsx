import { useQueryClient } from '@tanstack/react-query';
import { Stack } from 'expo-router';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Text } from 'react-native-paper';

import { alertsEnabled } from '../alerts/alerts';
import { switchAlertsOn } from '../alerts/switchOn';
import type { Device } from '../core/types';
import { keys } from '../features/queries';
import { useWiFiLock } from '../features/wifiLock';
import { t } from '../i18n';
import { useSession } from '../session/store';
import { lockView } from '../lib/trust';
import { AutoRejectSetting } from '../ui/AutoRejectSetting';
import { Muted } from '../ui/Muted';

// Rejet automatique (#35), opened from « Plus de fonctionnalités » (#76).
// The setting itself is AutoRejectSetting, unchanged.
export default function AutoRejectScreen() {
  const qc = useQueryClient();
  const router = useSession((s) => s.session?.mode === 'router');
  // The closed Wi-Fi (#108, RFC-007 D5): reserved, nobody unknown gets in,
  // so auto-reject has nothing left to do; open, it is the middle way (#111 R2).
  const lock = useWiFiLock();
  const view = lockView(lock.data);
  return (
    <ScrollView contentContainerStyle={{ padding: 8 }}>
      <Stack.Screen options={{ headerShown: true, title: t('settings.autoReject') }} />
      {view ? (
        <View style={styles.lock}>
          {view.reserved ? (
            <Text variant="bodyMedium" testID="auto-reject-paused">
              {t('autoReject.paused')}
            </Text>
          ) : (
            <Muted variant="bodyMedium" testID="auto-reject-middle">
              {t('autoReject.middle')}
            </Muted>
          )}
        </View>
      ) : null}
      <AutoRejectSetting
        // The watch service runs the rule and posts the "Accepter"
        // notifications: on a real box, it needs the alerts on.
        onEnabled={async () => {
          if (router && !(await alertsEnabled())) await switchAlertsOn(qc.getQueryData<Device[]>(keys.devices));
        }}
      />
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  lock: { paddingHorizontal: 8, paddingVertical: 8 },
});
