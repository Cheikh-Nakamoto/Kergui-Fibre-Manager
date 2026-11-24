import { Stack } from 'expo-router';
import { useState } from 'react';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Button, Checkbox, Chip, HelperText, Text, TextInput } from 'react-native-paper';

import {
  COMPLAINT_CATEGORIES,
  COMPLAINT_MAX_MESSAGE,
  ComplaintInputError,
  complaintBlock,
  complaints,
} from '../features/complaints';
import { errorMessage, t } from '../i18n';
import type { ComplaintCategory, ComplaintReceipt } from '../licensing/api';
import { useGate } from '../licensing/useGate';

/**
 * Réglages > « Signaler un problème » (issue #84): a category, a message,
 * and the app's journal only when the customer ticks the box. The hub
 * acknowledges with a complaint number; the team's answer comes back in the
 * notifications, where the customer can write again on that complaint. The
 * demo, a build without a hub and a phone not signed in show the form and
 * send nothing.
 */
export default function Report() {
  const { demo } = useGate();
  const [category, setCategory] = useState<ComplaintCategory | null>(null);
  const [message, setMessage] = useState('');
  const [withJournal, setWithJournal] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [blocked, setBlocked] = useState('');
  const [receipt, setReceipt] = useState<ComplaintReceipt | null>(null);

  const send = async () => {
    setError('');
    setBlocked('');
    if (!category) return setError(t('report.needCategory'));
    if (!message.trim()) return setError(t('report.needMessage'));
    const block = complaintBlock(demo);
    if (block) return setBlocked(t(`report.blocked.${block}`));
    setBusy(true);
    try {
      setReceipt(await complaints.send({ category, message, withJournal }));
      setCategory(null);
      setMessage('');
      setWithJournal(false);
    } catch (e) {
      if (e instanceof ComplaintInputError) {
        setError(t(e.reason === 'category' ? 'report.needCategory' : e.reason === 'message' ? 'report.needMessage' : 'report.tooLong'));
      } else {
        setError(errorMessage(e));
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <ScrollView contentContainerStyle={styles.body} keyboardShouldPersistTaps="handled" testID="report-screen">
      <Stack.Screen options={{ headerShown: true, title: t('report.title') }} />
      {receipt ? (
        <View style={styles.section}>
          <Text variant="titleMedium" testID="report-sent">
            {t('report.sent', { number: receipt.number })}
          </Text>
          <Button mode="outlined" onPress={() => setReceipt(null)} testID="report-another">
            {t('report.another')}
          </Button>
        </View>
      ) : (
        <>
          <View style={styles.section}>
            <Text variant="titleSmall">{t('report.category')}</Text>
            <View style={styles.chips}>
              {COMPLAINT_CATEGORIES.map((c) => (
                <Chip
                  key={c}
                  selected={category === c}
                  showSelectedCheck
                  onPress={() => {
                    setCategory(c);
                    setError('');
                  }}
                  accessibilityState={{ selected: category === c }}
                  testID={`report-category-${c}`}
                >
                  {t(`report.category.${c}`)}
                </Chip>
              ))}
            </View>
          </View>
          <TextInput
            mode="outlined"
            label={t('report.message')}
            placeholder={t('report.messageHint')}
            value={message}
            onChangeText={setMessage}
            multiline
            numberOfLines={5}
            maxLength={COMPLAINT_MAX_MESSAGE}
            style={styles.message}
            testID="report-message"
          />
          <Checkbox.Item
            label={t('report.journal')}
            status={withJournal ? 'checked' : 'unchecked'}
            onPress={() => setWithJournal((v) => !v)}
            position="leading"
            labelStyle={styles.checkLabel}
            labelVariant="bodyMedium"
            testID="report-journal"
          />
          <Text variant="bodySmall" style={styles.hint}>
            {t('report.journalHint')}
          </Text>
          {error ? (
            <HelperText type="error" testID="report-error">
              {error}
            </HelperText>
          ) : null}
          {blocked ? (
            <HelperText type="info" testID="report-blocked">
              {blocked}
            </HelperText>
          ) : null}
          <Button mode="contained" icon="send" onPress={() => void send()} loading={busy} disabled={busy} testID="report-send">
            {t('report.send')}
          </Button>
        </>
      )}
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  body: { padding: 16, gap: 12 },
  section: { gap: 8 },
  chips: { flexDirection: 'row', flexWrap: 'wrap', gap: 8 },
  message: { minHeight: 120 },
  checkLabel: { textAlign: 'left' },
  hint: { opacity: 0.7, paddingHorizontal: 8 },
});
