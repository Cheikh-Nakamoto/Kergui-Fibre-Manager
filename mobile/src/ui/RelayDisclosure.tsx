import { StyleSheet, View } from 'react-native';
import { Button, Text } from 'react-native-paper';

import { t } from '../i18n';

// The relay's prominent disclosure (#53, Play's User Data policy): what this
// phone sends to the Kergui server once it is a relay, why, what never
// leaves it, the permanent notification of the foreground service, and how
// to stop. Shown before the relay is first turned on, with « Accepter » /
// « Annuler »; afterwards it can be read again, with « Fermer » only.

type Props = { onAccept: () => void; onCancel: () => void; busy?: boolean } | { onClose: () => void };

const SECTIONS = [
  ['relayDisclosure.whatTitle', 'relayDisclosure.what'],
  ['relayDisclosure.sentTitle', 'relayDisclosure.sent'],
  ['relayDisclosure.whyTitle', 'relayDisclosure.why'],
  ['relayDisclosure.neverTitle', 'relayDisclosure.never'],
  ['relayDisclosure.notifTitle', 'relayDisclosure.notif'],
  ['relayDisclosure.stopTitle', 'relayDisclosure.stop'],
] as const;

export function RelayDisclosure(props: Props) {
  return (
    <View style={styles.gap} testID="relay-disclosure">
      <Text variant="titleMedium" accessibilityRole="header">
        {t('relayDisclosure.title')}
      </Text>
      {SECTIONS.map(([title, body]) => (
        <View key={title} style={styles.section}>
          <Text variant="titleSmall">{t(title)}</Text>
          <Text variant="bodyMedium">{t(body)}</Text>
        </View>
      ))}
      <Text variant="bodySmall" style={styles.hint}>
        {t('relayDisclosure.policy')}
      </Text>
      {'onClose' in props ? (
        <Button mode="outlined" onPress={props.onClose} testID="relay-disclosure-close">
          {t('relayDisclosure.close')}
        </Button>
      ) : (
        <>
          <Button mode="contained" onPress={props.onAccept} loading={props.busy} disabled={props.busy} testID="relay-disclosure-accept">
            {t('relayDisclosure.accept')}
          </Button>
          <Button mode="outlined" onPress={props.onCancel} disabled={props.busy} testID="relay-disclosure-cancel">
            {t('relayDisclosure.cancel')}
          </Button>
        </>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  gap: { gap: 12 },
  section: { gap: 4 },
  hint: { opacity: 0.8 },
});
