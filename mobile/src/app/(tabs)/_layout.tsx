import MaterialCommunityIcons from '@expo/vector-icons/MaterialCommunityIcons';
import { Redirect, Tabs } from 'expo-router';
import { StatusBar } from 'expo-status-bar';

import { t } from '../../i18n';
import { useSession } from '../../session/store';
import { HeaderBackground } from '../../ui/HeaderBackground';
import { InboxBell } from '../../ui/InboxBell';
import { headerTitleStyle, useChrome } from '../../ui/theme';

export default function TabsLayout() {
  const session = useSession((s) => s.session);
  const bars = useChrome();
  if (!session) return <Redirect href="/" />;

  return (
    <>
      {/* Status icons that read on the header (#128). */}
      <StatusBar style={bars.statusBar} />
      {/* The bell, top right on every tab: the notifications' own screen. */}
      <Tabs
        screenOptions={{
          headerRight: () => <InboxBell color={bars.onBackground} />,
          // Header and tab bar: the site's header, the page's background over a thin
          // line, no more solid green block (#128).
          headerStyle: { backgroundColor: bars.background },
          headerTintColor: bars.onBackground,
          headerTitleStyle,
          headerShadowVisible: false,
          headerBackground: HeaderBackground,
          tabBarStyle: { backgroundColor: bars.background, borderTopColor: bars.border, borderTopWidth: 1 },
          tabBarActiveTintColor: bars.active,
          tabBarInactiveTintColor: bars.inactive,
        }}
      >
      <Tabs.Screen
        name="devices"
        options={{
          title: t('tabs.devices'),
          tabBarButtonTestID: 'tab-devices',
          tabBarIcon: ({ color, size }) => <MaterialCommunityIcons name="devices" color={color as string} size={size} />,
        }}
      />
      <Tabs.Screen
        name="usage"
        options={{
          title: t('tabs.usage'),
          tabBarButtonTestID: 'tab-usage',
          tabBarIcon: ({ color, size }) => <MaterialCommunityIcons name="chart-bar" color={color as string} size={size} />,
        }}
      />
      <Tabs.Screen
        name="wifi"
        options={{
          title: t('tabs.wifi'),
          tabBarButtonTestID: 'tab-wifi',
          tabBarIcon: ({ color, size }) => <MaterialCommunityIcons name="wifi" color={color as string} size={size} />,
        }}
      />
      <Tabs.Screen
        name="settings"
        options={{
          title: t('tabs.settings'),
          tabBarButtonTestID: 'tab-settings',
          tabBarIcon: ({ color, size }) => <MaterialCommunityIcons name="cog-outline" color={color as string} size={size} />,
        }}
      />
      </Tabs>
    </>
  );
}
