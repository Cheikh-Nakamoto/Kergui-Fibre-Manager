import MaterialCommunityIcons from '@expo/vector-icons/MaterialCommunityIcons';
import { Redirect, Tabs } from 'expo-router';

import { t } from '../../i18n';
import { useSession } from '../../session/store';

export default function TabsLayout() {
  const session = useSession((s) => s.session);
  if (!session) return <Redirect href="/" />;

  return (
    <Tabs>
      <Tabs.Screen
        name="devices"
        options={{
          title: t('tabs.devices'),
          tabBarIcon: ({ color, size }) => <MaterialCommunityIcons name="devices" color={color as string} size={size} />,
        }}
      />
      <Tabs.Screen
        name="wifi"
        options={{
          title: t('tabs.wifi'),
          tabBarIcon: ({ color, size }) => <MaterialCommunityIcons name="wifi" color={color as string} size={size} />,
        }}
      />
      <Tabs.Screen
        name="settings"
        options={{
          title: t('tabs.settings'),
          tabBarIcon: ({ color, size }) => <MaterialCommunityIcons name="cog-outline" color={color as string} size={size} />,
        }}
      />
    </Tabs>
  );
}
