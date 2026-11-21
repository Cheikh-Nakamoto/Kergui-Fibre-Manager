import { router, Stack } from 'expo-router';
import { useState } from 'react';
import { KeyboardAvoidingView, Platform, ScrollView, StyleSheet } from 'react-native';
import { Button, HelperText, Text, TextInput } from 'react-native-paper';

import { hubErrorMessage, t } from '../i18n';
import { useAccount } from '../licensing/account';
import { useEntitlement } from '../licensing/entitlement';

// Kergui account sign-in: the phone number, then the 6-digit code the hub
// sends over WhatsApp (RFC-006, Accounts).
export default function SignInScreen() {
  const { requestCode, verify } = useAccount();
  const [phone, setPhone] = useState('');
  const [sentTo, setSentTo] = useState<string | null>(null);
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const run = async (step: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await step();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  const send = () =>
    run(async () => {
      const out = await requestCode(phone);
      setSentTo(out.phone);
      setCode('');
    });

  const submit = () =>
    run(async () => {
      await verify(sentTo ?? phone, code);
      await useEntitlement.getState().refresh({ force: true });
      if (router.canGoBack()) router.back();
      else router.replace('/premium');
    });

  return (
    <KeyboardAvoidingView style={styles.root} behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
      <ScrollView contentContainerStyle={styles.body} keyboardShouldPersistTaps="handled">
        <Stack.Screen options={{ headerShown: true, title: t('signIn.title') }} />
        <Text variant="bodyLarge">{t('signIn.hint')}</Text>
        {sentTo === null ? (
          <>
            <TextInput
              mode="outlined"
              label={t('signIn.phone')}
              value={phone}
              onChangeText={setPhone}
              keyboardType="phone-pad"
              autoComplete="tel"
              textContentType="telephoneNumber"
              onSubmitEditing={() => void send()}
              testID="sign-in-phone"
            />
            <Button mode="contained" onPress={() => void send()} loading={busy} disabled={busy || !phone.trim()} testID="sign-in-send">
              {t('signIn.send')}
            </Button>
          </>
        ) : (
          <>
            <Text variant="bodyMedium">{t('signIn.codeHint', { phone: sentTo })}</Text>
            <TextInput
              mode="outlined"
              label={t('signIn.code')}
              value={code}
              onChangeText={(v) => setCode(v.replace(/\D/g, '').slice(0, 6))}
              keyboardType="number-pad"
              autoComplete="one-time-code"
              textContentType="oneTimeCode"
              onSubmitEditing={() => void submit()}
              testID="sign-in-code"
            />
            <Button mode="contained" onPress={() => void submit()} loading={busy} disabled={busy || code.length !== 6} testID="sign-in-verify">
              {t('signIn.verify')}
            </Button>
            <Button mode="text" onPress={() => void send()} disabled={busy}>
              {t('signIn.resend')}
            </Button>
            <Button mode="text" onPress={() => setSentTo(null)} disabled={busy}>
              {t('signIn.change')}
            </Button>
          </>
        )}
        {error ? <HelperText type="error">{hubErrorMessage(error)}</HelperText> : null}
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1 },
  body: { padding: 24, gap: 12 },
});
