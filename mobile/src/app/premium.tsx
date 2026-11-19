import MaterialCommunityIcons from '@expo/vector-icons/MaterialCommunityIcons';
import { Stack } from 'expo-router';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Button, HelperText, Text, useTheme } from 'react-native-paper';

import { useBilling } from '../billing/store';
import { t } from '../i18n';

const FEATURES = [
  { icon: 'block-helper', key: 'premium.featureBlock' },
  { icon: 'shield-lock-outline', key: 'premium.featureFilter' },
  { icon: 'wifi-off', key: 'premium.featureSsid' },
] as const;

export default function PremiumScreen() {
  const theme = useTheme();
  const { premium, available, price, trial, status, error, buy, restore, manage } = useBilling();

  return (
    <ScrollView contentContainerStyle={styles.body}>
      <Stack.Screen options={{ headerShown: true, title: t('premium.title') }} />
      <Text variant="headlineSmall" style={styles.heading}>
        {premium ? t('premium.active') : t('premium.pitch')}
      </Text>
      <View style={styles.features}>
        {FEATURES.map((f) => (
          <View key={f.key} style={styles.feature}>
            <MaterialCommunityIcons name={f.icon} size={24} color={theme.colors.primary} />
            <Text variant="bodyLarge" style={styles.featureText}>
              {t(f.key)}
            </Text>
          </View>
        ))}
      </View>
      <Text variant="bodyMedium" style={styles.free}>
        {t('premium.free')}
      </Text>

      {premium ? (
        <Button mode="outlined" onPress={manage} testID="premium-manage">
          {t('premium.manage')}
        </Button>
      ) : (
        <>
          <Button
            mode="contained"
            onPress={buy}
            loading={status === 'buying'}
            disabled={!available || status !== 'idle'}
            testID="premium-buy"
          >
            {trial ? t('premium.startTrial') : t('premium.subscribe')}
          </Button>
          {price ? (
            <Text variant="bodyMedium" style={styles.center}>
              {trial ? t('premium.priceAfterTrial', { price }) : t('premium.price', { price })}
            </Text>
          ) : null}
          <Button mode="text" onPress={restore} disabled={!available}>
            {t('premium.restore')}
          </Button>
        </>
      )}

      {status === 'pending' ? <HelperText type="info">{t('premium.pending')}</HelperText> : null}
      {!available && !premium ? <HelperText type="info">{t('premium.unavailable')}</HelperText> : null}
      {error ? <HelperText type="error">{t('error.generic', { error })}</HelperText> : null}

      <Text variant="bodySmall" style={styles.legal}>
        {t('premium.legal')}
      </Text>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  body: { padding: 24, gap: 16 },
  heading: { fontFamily: 'Inter_700Bold' },
  features: { gap: 12 },
  feature: { flexDirection: 'row', alignItems: 'center', gap: 12 },
  featureText: { flex: 1 },
  free: { opacity: 0.8 },
  center: { textAlign: 'center' },
  legal: { opacity: 0.6 },
});
