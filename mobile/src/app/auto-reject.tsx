import { useQueryClient } from '@tanstack/react-query';
import { Stack } from 'expo-router';
import { ScrollView } from 'react-native';

import { alertsEnabled } from '../alerts/alerts';
import { switchAlertsOn } from '../alerts/switchOn';
import type { Device } from '../core/types';
import { keys } from '../features/queries';
import { t } from '../i18n';
import { useSession } from '../session/store';
import { AutoRejectSetting } from '../ui/AutoRejectSetting';

// Rejet automatique (#35), opened from « Plus de fonctionnalités » (#76).
// The setting itself is AutoRejectSetting, unchanged.
export default function AutoRejectScreen() {
  const qc = useQueryClient();
  const router = useSession((s) => s.session?.mode === 'router');
  return (
    <ScrollView contentContainerStyle={{ padding: 8 }}>
      <Stack.Screen options={{ headerShown: true, title: t('settings.autoReject') }} />
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
