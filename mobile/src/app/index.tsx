import { Redirect, router } from 'expo-router';
import { useState } from 'react';
import { StyleSheet, View } from 'react-native';
import { Button, HelperText, Text } from 'react-native-paper';
import { SafeAreaView } from 'react-native-safe-area-context';

import { errorMessage, t } from '../i18n';
import { useSession } from '../session/store';

export default function Welcome() {
  const { session, bootError, enterDemo } = useSession();
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
        <Text variant="displaySmall">{t('welcome.title')}</Text>
        <Text variant="bodyLarge" style={styles.subtitle}>
          {t('welcome.subtitle')}
        </Text>
        {bootError ? <HelperText type="error">{t('welcome.bootError', { error: bootError })}</HelperText> : null}
      </View>
      <View style={styles.actions}>
        <Button mode="contained" onPress={() => router.push('/connect')} disabled={!!bootError} testID="welcome-connect">
          {t('welcome.connect')}
        </Button>
        <Button mode="outlined" onPress={demo} loading={busy} disabled={busy || !!bootError} testID="welcome-demo">
          {t('welcome.demo')}
        </Button>
        <HelperText type={error ? 'error' : 'info'}>{error ? errorMessage(error) : t('welcome.demoHint')}</HelperText>
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, padding: 24 },
  body: { flex: 1, justifyContent: 'center', gap: 16 },
  subtitle: { opacity: 0.8 },
  actions: { gap: 12 },
});
