import { Redirect, router } from 'expo-router';
import { useState } from 'react';
import { Image, StyleSheet, View } from 'react-native';
import { Button, HelperText, Text, useTheme } from 'react-native-paper';
import { SafeAreaView } from 'react-native-safe-area-context';

import { errorMessage, t } from '../i18n';
import { useSession } from '../session/store';
import { Hud } from '../ui/Hud';
import { Muted } from '../ui/Muted';

// Two choices for a first-time user (#116, audit M9): « Connecter ma box »
// and « Essayer la démo ». The relay phone and the lost phone wait under a
// small « Autres choix » link.
export default function Welcome() {
  const { session, bootError, enterDemo } = useSession();
  const theme = useTheme();
  const [more, setMore] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  if (session) return <Redirect href="/devices" />;

  const demo = async () => {
    setBusy(true);
    setError(null);
    try {
      await enterDemo();
      router.replace('/devices');
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <SafeAreaView style={styles.root}>
      <View style={styles.body}>
        {/* One of the two places with the site's HUD corners (#128). */}
        <Hud style={styles.hud}>
          <Image
            source={theme.dark ? require('../../assets/logo-dark.png') : require('../../assets/logo.png')}
            style={styles.logo}
            accessibilityIgnoresInvertColors
          />
        </Hud>
        <Text variant="displaySmall" style={styles.title}>
          {t('welcome.title')}
        </Text>
        <Muted variant="bodyLarge" style={styles.subtitle}>
          {t('welcome.subtitle')}
        </Muted>
        {bootError ? <HelperText type="error">{t('welcome.bootError')}</HelperText> : null}
      </View>
      <View style={styles.actions}>
        <Button mode="contained" onPress={() => router.push('/connect')} disabled={!!bootError} testID="welcome-connect">
          {t('welcome.connect')}
        </Button>
        <Button mode="outlined" onPress={demo} loading={busy} disabled={busy || !!bootError} testID="welcome-demo">
          {t('welcome.demo')}
        </Button>
        <HelperText type={error ? 'error' : 'info'}>{error ? errorMessage(error) : t('welcome.demoHint')}</HelperText>
        {more ? (
          <View testID="welcome-others">
            <Button mode="text" icon="access-point-network" onPress={() => router.push('/become-relay')} disabled={!!bootError} testID="welcome-relay">
              {t('welcome.relay')}
            </Button>
            <Button mode="text" icon="cellphone-key" onPress={() => router.push('/lost-phone')} disabled={!!bootError} testID="welcome-lost">
              {t('welcome.lost')}
            </Button>
          </View>
        ) : (
          <Button mode="text" compact onPress={() => setMore(true)} testID="welcome-more">
            {t('welcome.more')}
          </Button>
        )}
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, padding: 24 },
  body: { flex: 1, justifyContent: 'center', alignItems: 'center', gap: 16 },
  hud: { padding: 12, marginBottom: 8 },
  logo: { width: 120, height: 120 },
  title: { textAlign: 'center' },
  subtitle: { textAlign: 'center' },
  actions: { gap: 12 },
});
