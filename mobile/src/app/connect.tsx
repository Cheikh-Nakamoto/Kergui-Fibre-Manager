import { router, Stack } from 'expo-router';
import { useState } from 'react';
import { Alert, KeyboardAvoidingView, Platform, ScrollView, StyleSheet } from 'react-native';
import { Button, HelperText, Text, TextInput } from 'react-native-paper';

import { api, core } from '../core/client';
import { errorMessage, t } from '../i18n';
import { formatFingerprint, normaliseRouterUrl } from '../lib/format';
import { useSession } from '../session/store';

function askTrust(fingerprint: string): Promise<boolean> {
  return new Promise((resolve) =>
    Alert.alert(t('connect.trustTitle'), t('connect.trustBody', { fingerprint: formatFingerprint(fingerprint) }), [
      { text: t('common.cancel'), style: 'cancel', onPress: () => resolve(false) },
      { text: t('connect.trust'), onPress: () => resolve(true) },
    ]),
  );
}

/**
 * Finds how to reach the box: https first (pinning its self-signed
 * certificate on first use), then plain http for the older models.
 */
async function reach(input: string): Promise<{ url: string; pin: string } | null> {
  const url = normaliseRouterUrl(input);
  const candidates = url.startsWith('https://') ? [url, url.replace(/^https:/, 'http:')] : [url];
  for (const candidate of candidates) {
    let pin = '';
    if (candidate.startsWith('https://')) {
      try {
        pin = await core.fingerprint(candidate);
      } catch {
        continue; // nothing listens on 443: try http
      }
      if (!(await askTrust(pin))) return null;
    }
    await core.configure(candidate, pin);
    const info = await api.discover();
    if (info.Reachable) return { url: candidate, pin };
  }
  throw new Error(t('connect.unreachable'));
}

export default function Connect() {
  const connected = useSession((s) => s.connected);
  const [address, setAddress] = useState('192.168.1.1');
  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      const target = await reach(address);
      if (!target) return;
      await api.login(username, password); // stored encrypted by the core, never echoed back
      await connected({ mode: 'router', routerUrl: target.url, pin: target.pin });
      router.replace('/devices');
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <KeyboardAvoidingView style={{ flex: 1 }} behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
      <Stack.Screen options={{ headerShown: true, title: t('connect.title') }} />
      <ScrollView contentContainerStyle={styles.body} keyboardShouldPersistTaps="handled">
        <Text variant="bodyMedium">{t('connect.hint')}</Text>
        <TextInput
          label={t('connect.address')}
          value={address}
          onChangeText={setAddress}
          autoCapitalize="none"
          autoCorrect={false}
          keyboardType="url"
        />
        <TextInput label={t('connect.username')} value={username} onChangeText={setUsername} autoCapitalize="none" />
        <TextInput
          label={t('connect.password')}
          value={password}
          onChangeText={setPassword}
          secureTextEntry
          autoCapitalize="none"
          testID="connect-password"
        />
        <Button mode="contained" onPress={submit} loading={busy} disabled={busy || !password} testID="connect-submit">
          {t('connect.submit')}
        </Button>
        {error ? <HelperText type="error">{errorMessage(error)}</HelperText> : null}
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

const styles = StyleSheet.create({
  body: { padding: 24, gap: 16 },
});
