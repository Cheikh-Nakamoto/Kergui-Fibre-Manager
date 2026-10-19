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
 *
 * Every attempt is written to the core's journal, because this is the step
 * that fails in a household and the one nobody can describe afterwards. A
 * candidate that does not work is a reason to try the next one, never a
 * reason to stop: an https port that answers the handshake but serves no
 * router page used to end the search before http was ever tried.
 */
async function reach(input: string): Promise<{ url: string; pin: string } | null> {
  const url = normaliseRouterUrl(input);
  const candidates = url.startsWith('https://') ? [url, url.replace(/^https:/, 'http:')] : [url];
  let refused = false;
  for (const candidate of candidates) {
    core.note('info', `connexion: essai de ${candidate}`);
    let pin = '';
    if (candidate.startsWith('https://')) {
      try {
        pin = await core.fingerprint(candidate);
      } catch (e) {
        core.note('warn', `connexion: pas de certificat sur ${candidate} (${message(e)})`);
        continue; // nothing listens on 443: try http
      }
      if (!(await askTrust(pin))) {
        core.note('warn', 'connexion: certificat refusé par l’utilisateur');
        refused = true;
        continue;
      }
    }
    try {
      await core.configure(candidate, pin);
      const info = await api.discover();
      if (info.Reachable) {
        core.note('info', `connexion: ${candidate} répond (${info.Vendor} ${info.Model}, adaptateur ${info.AdapterID})`);
        return { url: candidate, pin };
      }
      core.note('warn', `connexion: ${candidate} répond mais n’est pas une box reconnue`);
    } catch (e) {
      core.note('warn', `connexion: ${candidate} a échoué (${message(e)})`);
    }
  }
  if (refused) return null; // the household said no; that is not an error
  throw new Error(t('connect.unreachable'));
}

/** A short reason for the journal, never a stack. */
function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
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
      // The journal keeps why; the screen says what to do about it.
      core.note('error', `connexion: abandon (${message(e)})`);
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
