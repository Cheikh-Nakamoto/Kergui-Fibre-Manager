import { router, Stack } from 'expo-router';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Button, Icon, Text } from 'react-native-paper';

import { t } from '../i18n';
import type { MessageKey } from '../i18n/fr';

// « J'ai perdu mon téléphone » from the welcome screen (#116, audit C4, M9):
// the ways back first, with nothing asked, then the « Téléphone perdu »
// screen (recovery.tsx), which signs in and runs the chosen way.

const WAYS: { icon: string; title: MessageKey; hint: MessageKey }[] = [
  { icon: 'account-key', title: 'lostPhone.coOwner', hint: 'lostPhone.coOwnerHint' },
  { icon: 'home-import-outline', title: 'lostPhone.atHome', hint: 'lostPhone.atHomeHint' },
  { icon: 'ethernet-cable', title: 'lostPhone.cable', hint: 'lostPhone.cableHint' },
];

export default function LostPhone() {
  return (
    <>
      <Stack.Screen options={{ headerShown: true, title: t('welcome.lost') }} />
      <ScrollView contentContainerStyle={styles.body} testID="lost-phone">
        <Text variant="bodyLarge">{t('lostPhone.intro')}</Text>
        {WAYS.map((w) => (
          <View key={w.title} style={styles.way}>
            <Icon source={w.icon} size={28} />
            <View style={styles.wayText}>
              <Text variant="titleSmall" accessibilityRole="header">
                {t(w.title)}
              </Text>
              <Text variant="bodyMedium">{t(w.hint)}</Text>
            </View>
          </View>
        ))}
        <Button mode="contained" onPress={() => router.push('/recovery')} testID="lost-phone-next">
          {t('lostPhone.next')}
        </Button>
      </ScrollView>
    </>
  );
}

const styles = StyleSheet.create({
  body: { padding: 16, gap: 16 },
  way: { flexDirection: 'row', gap: 12, alignItems: 'flex-start' },
  wayText: { flex: 1, gap: 4 },
});
