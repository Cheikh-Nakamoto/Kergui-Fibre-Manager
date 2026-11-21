import { useState } from 'react';
import { KeyboardAvoidingView, Platform, StyleSheet, View } from 'react-native';
import { Button, HelperText, Text, TextInput } from 'react-native-paper';
import { SafeAreaView } from 'react-native-safe-area-context';

import { t } from '../i18n';
import { activate } from './activation';

export function ActivationScreen({ onActivated }: { onActivated: () => void }) {
  const [key, setKey] = useState('');
  const [hidden, setHidden] = useState(true);
  const [busy, setBusy] = useState(false);
  const [wrong, setWrong] = useState(false);

  const submit = async () => {
    setBusy(true);
    setWrong(false);
    try {
      if (await activate(key)) onActivated();
      else setWrong(true);
    } finally {
      setBusy(false);
    }
  };

  return (
    <SafeAreaView style={styles.root}>
      <KeyboardAvoidingView style={styles.body} behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
        <Text variant="headlineMedium" style={styles.title}>
          {t('activation.title')}
        </Text>
        <Text variant="bodyLarge" style={styles.hint}>
          {t('activation.hint')}
        </Text>
        <View style={styles.form}>
          <TextInput
            mode="outlined"
            label={t('activation.key')}
            value={key}
            onChangeText={(v) => {
              setKey(v);
              setWrong(false);
            }}
            secureTextEntry={hidden}
            autoCapitalize="none"
            autoCorrect={false}
            onSubmitEditing={submit}
            right={<TextInput.Icon icon={hidden ? 'eye' : 'eye-off'} onPress={() => setHidden(!hidden)} />}
            testID="activation-key"
          />
          <HelperText type="error" visible={wrong}>
            {t('activation.wrong')}
          </HelperText>
          <Button mode="contained" onPress={submit} loading={busy} disabled={busy || !key.trim()} testID="activation-submit">
            {t('activation.submit')}
          </Button>
        </View>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, padding: 24 },
  body: { flex: 1, justifyContent: 'center', gap: 16 },
  title: { fontFamily: 'Inter_700Bold', textAlign: 'center' },
  hint: { opacity: 0.8, textAlign: 'center' },
  form: { gap: 4, marginTop: 16 },
});
