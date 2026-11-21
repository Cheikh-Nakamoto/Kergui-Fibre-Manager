import { Inter_400Regular } from '@expo-google-fonts/inter/400Regular';
import { Inter_500Medium } from '@expo-google-fonts/inter/500Medium';
import { Inter_600SemiBold } from '@expo-google-fonts/inter/600SemiBold';
import { Inter_700Bold } from '@expo-google-fonts/inter/700Bold';
import { focusManager, QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useFonts } from 'expo-font';
import * as Notifications from 'expo-notifications';
import { router, Stack, ThemeProvider } from 'expo-router';
import { StatusBar } from 'expo-status-bar';
import { useEffect, useMemo, useState } from 'react';
import { AppState, useColorScheme, View } from 'react-native';
import { ActivityIndicator, PaperProvider } from 'react-native-paper';

import { isActivated } from '../activation/activation';
import { ActivationScreen } from '../activation/ActivationScreen';
import { resumeWatch } from '../alerts/alerts';
import { ApiError } from '../core/client';
import { useSession } from '../session/store';
import { themes } from '../ui/theme';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Only a box that is momentarily unreachable or busy is worth a retry;
      // 401 / 501 will answer the same again.
      retry: (count, e) => count < 1 && e instanceof ApiError && [0, 502, 503].includes(e.status),
    },
  },
});

export default function RootLayout() {
  const scheme = useColorScheme();
  const { paper, navigation } = useMemo(() => themes(scheme), [scheme]);
  const { ready, boot, session } = useSession();
  // null while SecureStore answers; the app stays behind the activation
  // screen until the owner's key has been entered once on this phone.
  const [activated, setActivated] = useState<boolean | null>(null);
  useEffect(() => {
    void isActivated().then(setActivated);
  }, []);
  // Per-weight imports: only these four files are bundled, not all of Inter.
  // A font that fails to load falls back to the system font rather than
  // blocking the app.
  const [fontsLoaded, fontError] = useFonts({ Inter_400Regular, Inter_500Medium, Inter_600SemiBold, Inter_700Bold });

  useEffect(() => {
    void boot();
  }, [boot]);

  // The Android watch survives the app being closed; this restarts it after
  // an update from a build that did not have it, or a new box.
  useEffect(() => {
    if (ready && activated && session?.mode === 'router') void resumeWatch();
  }, [ready, activated, session]);

  // React Query knows nothing about an app leaving the screen: on a phone it
  // kept polling the box every 30 s in the background, waking the Wi-Fi radio
  // for a list nobody was looking at. Telling it when the app is in front
  // also makes it refetch on return, which is when the list is stale.
  useEffect(() => {
    focusManager.setFocused(AppState.currentState === 'active');
    const sub = AppState.addEventListener('change', (state) => focusManager.setFocused(state === 'active'));
    return () => sub.remove();
  }, []);

  // Tapping an alert opens the new device, where it can be blocked.
  useEffect(() => {
    const sub = Notifications.addNotificationResponseReceivedListener((r) => {
      const mac = r.notification.request.content.data?.mac;
      if (typeof mac === 'string') router.push({ pathname: '/device/[mac]', params: { mac } });
    });
    return () => sub.remove();
  }, []);

  const loading = !ready || activated === null || (!fontsLoaded && !fontError);
  return (
    <PaperProvider theme={paper}>
      <ThemeProvider value={navigation}>
        <QueryClientProvider client={queryClient}>
          <StatusBar style={paper.dark ? 'light' : 'dark'} />
          {loading ? (
            <View style={{ flex: 1, backgroundColor: paper.colors.background, justifyContent: 'center' }}>
              <ActivityIndicator />
            </View>
          ) : !activated ? (
            <ActivationScreen onActivated={() => setActivated(true)} />
          ) : (
            <Stack screenOptions={{ headerShown: false, contentStyle: { backgroundColor: paper.colors.background } }} />
          )}
        </QueryClientProvider>
      </ThemeProvider>
    </PaperProvider>
  );
}
