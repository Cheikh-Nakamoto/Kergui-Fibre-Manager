import { Inter_400Regular } from '@expo-google-fonts/inter/400Regular';
import { Inter_500Medium } from '@expo-google-fonts/inter/500Medium';
import { Inter_600SemiBold } from '@expo-google-fonts/inter/600SemiBold';
import { Inter_700Bold } from '@expo-google-fonts/inter/700Bold';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useFonts } from 'expo-font';
import { Stack, ThemeProvider } from 'expo-router';
import { StatusBar } from 'expo-status-bar';
import { useEffect, useMemo } from 'react';
import { useColorScheme, View } from 'react-native';
import { ActivityIndicator, PaperProvider } from 'react-native-paper';

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
  const { ready, boot } = useSession();
  // Per-weight imports: only these four files are bundled, not all of Inter.
  // A font that fails to load falls back to the system font rather than
  // blocking the app.
  const [fontsLoaded, fontError] = useFonts({ Inter_400Regular, Inter_500Medium, Inter_600SemiBold, Inter_700Bold });

  useEffect(() => {
    void boot();
  }, [boot]);

  const loading = !ready || (!fontsLoaded && !fontError);
  return (
    <PaperProvider theme={paper}>
      <ThemeProvider value={navigation}>
        <QueryClientProvider client={queryClient}>
          <StatusBar style={paper.dark ? 'light' : 'dark'} />
          {loading ? (
            <View style={{ flex: 1, backgroundColor: paper.colors.background, justifyContent: 'center' }}>
              <ActivityIndicator />
            </View>
          ) : (
            <Stack screenOptions={{ headerShown: false, contentStyle: { backgroundColor: paper.colors.background } }} />
          )}
        </QueryClientProvider>
      </ThemeProvider>
    </PaperProvider>
  );
}
