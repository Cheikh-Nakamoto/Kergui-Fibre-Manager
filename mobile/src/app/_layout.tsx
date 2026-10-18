import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { Stack } from 'expo-router';
import { StatusBar } from 'expo-status-bar';
import { useEffect } from 'react';
import { useColorScheme } from 'react-native';
import { ActivityIndicator, MD3DarkTheme, MD3LightTheme, PaperProvider } from 'react-native-paper';

import { ApiError } from '../core/client';
import { useSession } from '../session/store';

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
  const { ready, boot } = useSession();

  useEffect(() => {
    void boot();
  }, [boot]);

  return (
    <PaperProvider theme={scheme === 'dark' ? MD3DarkTheme : MD3LightTheme}>
      <QueryClientProvider client={queryClient}>
        <StatusBar style="auto" />
        {ready ? <Stack screenOptions={{ headerShown: false }} /> : <ActivityIndicator style={{ flex: 1 }} />}
      </QueryClientProvider>
    </PaperProvider>
  );
}
