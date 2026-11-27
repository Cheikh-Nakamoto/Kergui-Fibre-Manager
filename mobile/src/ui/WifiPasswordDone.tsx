import { Modal, ScrollView, StyleSheet } from 'react-native';
import { Button, Card, Text, useTheme } from 'react-native-paper';
import { SafeAreaView } from 'react-native-safe-area-context';

import { t } from '../i18n';
import { sendOnWhatsApp } from '../lib/sendOnWhatsApp';
import { wifiQrValue } from '../lib/wifiQr';
import { KCard } from './KCard';
import { Mono } from './Mono';
import { QrCode } from './QrCode';

/** The network and the password just typed for it. */
export type NewWifiPassword = { name: string; password: string; hidden: boolean };

/**
 * The screen after a new Wi-Fi password (#123, audit H8): every device of
 * the house must now type it, so it is shown big, as the QR code a phone's
 * camera joins with, and « Envoyer sur WhatsApp » hands it to the
 * household. It shows only what the owner just typed, held by the Wi-Fi tab
 * while it is open: nothing stores it, nothing logs it, and closing it
 * (« Terminé » or back) forgets it.
 */
export function WifiPasswordDone({ shown, onClose }: { shown: NewWifiPassword | null; onClose: () => void }) {
  const { colors } = useTheme();
  return (
    <Modal visible={!!shown} animationType="slide" onRequestClose={onClose} testID="wifi-password-done-modal">
      {shown ? (
        <SafeAreaView style={[styles.root, { backgroundColor: colors.background }]} testID="wifi-password-done">
          <ScrollView contentContainerStyle={styles.content}>
            <Text variant="titleLarge" accessibilityRole="header">
              {t('wifi.passwordChanged')}
            </Text>
            <KCard>
              <Card.Content style={styles.card}>
                <Text variant="titleMedium">{t('wifi.doneTitle', { name: shown.name })}</Text>
                {/* Big and monospaced: every character reads, 0 from O, l from 1. */}
                <Text variant="displaySmall" selectable style={styles.center} testID="wifi-password-done-password">
                  <Mono>{shown.password}</Mono>
                </Text>
                <Text variant="bodyMedium">{t('wifi.doneHint')}</Text>
                <QrCode
                  value={wifiQrValue(shown)}
                  size={220}
                  label={t('wifi.doneQr', { name: shown.name })}
                  testID="wifi-password-done-qr"
                />
              </Card.Content>
            </KCard>
            <Button
              mode="contained"
              icon="whatsapp"
              onPress={() => void sendOnWhatsApp(t('wifi.doneShareText', { name: shown.name, password: shown.password }))}
              testID="wifi-password-done-whatsapp"
            >
              {t('wifi.doneWhatsApp')}
            </Button>
            <Button mode="outlined" onPress={onClose} testID="wifi-password-done-close">
              {t('wifi.doneClose')}
            </Button>
          </ScrollView>
        </SafeAreaView>
      ) : null}
    </Modal>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1 },
  content: { padding: 16, gap: 16 },
  card: { paddingVertical: 16, gap: 12 },
  center: { textAlign: 'center' },
});
