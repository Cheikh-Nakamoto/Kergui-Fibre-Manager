import { router, Stack } from 'expo-router';
import type { ReactNode } from 'react';
import { ScrollView, StyleSheet, View } from 'react-native';
import { List } from 'react-native-paper';

import { useAutoReject } from '../features/queries';
import { t } from '../i18n';
import { useGate } from '../licensing/useGate';
import { FeatureTile } from '../ui/FeatureTile';
import { PlanFeatures } from '../ui/PlanFeatures';
import { RelayPhoneSetting } from '../ui/RelayPhoneSetting';
import { StateBadge } from '../ui/StateMarks';
import { fontStyles } from '../ui/theme';

// « Plus de fonctionnalités » (#76, owner: « il y a trop de fonctionnalités
// à découvrir dans les paramètres »), opened from the tile at the top of
// Réglages. On top, what the box's level includes; then the features grouped
// by purpose, each tile opening its existing screen (nothing moved but the
// entries). The testIDs are the ones the entries had in Réglages.

function Group({ title, testID, children }: { title: string; testID: string; children: ReactNode }) {
  return (
    <View testID={testID}>
      <List.Section titleStyle={fontStyles.display} title={title} style={styles.section}>
        {children}
      </List.Section>
    </View>
  );
}

export default function FeaturesScreen() {
  const { demo } = useGate();
  // A box model without Wi-Fi access control has nothing to refuse with.
  const autoRejectSupported = useAutoReject().data !== null;

  return (
    <ScrollView contentContainerStyle={{ padding: 8 }} testID="features-screen">
      <Stack.Screen options={{ headerShown: true, title: t('features.title') }} />
      <PlanFeatures />

      <Group title={t('features.remote')} testID="features-group-remote">
        <FeatureTile
          role="connected"
          icon="access-point-network"
          title={t('relay.title')}
          description={t('relay.settingsHint')}
          onPress={() => router.push('/relay')}
          testID="settings-relay"
        />
        <RelayPhoneSetting demo={demo} left={() => <StateBadge role="connected" icon="cellphone-link" style={styles.badge} />} />
        <FeatureTile
          role="connected"
          icon="home-account"
          title={t('features.household')}
          description={t('features.householdHint')}
          onPress={() => router.push('/household')}
          testID="settings-household"
        />
      </Group>

      <Group title={t('whatsapp.title')} testID="features-group-whatsapp">
        <FeatureTile
          role="attention"
          icon="whatsapp"
          title={t('features.whatsappOptin')}
          description={t('whatsapp.settingsHint')}
          onPress={() => router.push('/whatsapp')}
          testID="settings-whatsapp"
        />
        <FeatureTile
          role="attention"
          icon="numeric-1-box-outline"
          title={t('whatsapp.hubKey')}
          description={t('features.hubKeyHint')}
          onPress={() => router.push('/whatsapp')}
          testID="features-whatsapp-hub-key"
        />
      </Group>

      {autoRejectSupported ? (
        <Group title={t('features.automation')} testID="features-group-automation">
          <FeatureTile
            role="blocked"
            icon="shield-account-outline"
            title={t('settings.autoReject')}
            description={t('features.autoRejectHint')}
            feature="auto_reject"
            onPress={() => router.push('/auto-reject')}
            testID="features-auto-reject"
          />
        </Group>
      ) : null}
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  section: { marginVertical: 4 },
  badge: { marginLeft: 8 },
});
