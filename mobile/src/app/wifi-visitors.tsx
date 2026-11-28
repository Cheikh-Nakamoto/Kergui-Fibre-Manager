import { Stack } from 'expo-router';
import { useEffect, useMemo } from 'react';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Button, Text } from 'react-native-paper';

import { useDevices } from '../features/queries';
import { useBoxKey, useLockPrefs, useWiFiLock, type VisitorAnswer } from '../features/wifiLock';
import { t } from '../i18n';
import { lockView, visitorCandidates } from '../lib/trust';
import { Muted } from '../ui/Muted';

/**
 * « Réseaux des visiteurs », from the Wi-Fi tab (#108, #134, RFC-007 D2,
 * R9): the answer to « Ce réseau est-il pour vos visiteurs ? », asked once
 * in the panel and changed here. Each network with its band and the
 * devices on it now. A visitors' network stays open with its own password;
 * the answer is used at the next reservation.
 */
export default function WiFiVisitors() {
  const lock = useWiFiLock();
  const { data: devices } = useDevices();
  const box = useBoxKey();
  const prefs = useLockPrefs();
  useEffect(() => {
    void prefs.load();
  }, [prefs]);
  const candidates = useMemo(() => visitorCandidates(lock.data?.aps ?? [], devices ?? []), [lock.data, devices]);
  const answers = prefs.byBox[box]?.visitors ?? {};
  const reserved = !!lockView(lock.data)?.reserved;
  const answer = (name: string, a: VisitorAnswer) => void prefs.update(box, { visitors: { [name]: a } });
  return (
    <ScrollView contentContainerStyle={styles.body} testID="wifi-visitors">
      <Stack.Screen options={{ headerShown: true, title: t('lock.visitorsTitle') }} />
      <Muted variant="bodyMedium">{t('lock.visitorsSettingsHint')}</Muted>
      {!candidates.length && lock.data ? <Text variant="bodyMedium">{t('lock.visitorsNone')}</Text> : null}
      {candidates.map((c, i) => (
        <View key={c.name} style={styles.question} testID={`wifi-visitors-${i}`}>
          <Text variant="titleSmall">{t('lock.visitorsQuestion')}</Text>
          <Text variant="bodyMedium">{t('lock.visitorsNet', { name: c.name, bands: c.bands, n: c.connected })}</Text>
          <View style={styles.row}>
            {(['yes', 'no', 'unsure'] as const).map((a) => (
              <Button
                key={a}
                compact
                mode={answers[c.name] === a ? 'contained' : 'outlined'}
                onPress={() => answer(c.name, a)}
                testID={`wifi-visitors-${i}-${a}`}
              >
                {t(a === 'yes' ? 'lock.visitorsYes' : a === 'no' ? 'lock.visitorsNo' : 'lock.visitorsUnsure')}
              </Button>
            ))}
          </View>
        </View>
      ))}
      {reserved && candidates.length ? <Muted variant="bodySmall">{t('lock.visitorsSaved')}</Muted> : null}
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  body: { padding: 16, gap: 16 },
  question: { gap: 6 },
  row: { flexDirection: 'row', gap: 8, flexWrap: 'wrap' },
});
