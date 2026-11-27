import type { ReactNode } from 'react';
import { StyleSheet, View } from 'react-native';
import { Icon, List, Text, useTheme } from 'react-native-paper';

import { t } from '../i18n';
import type { MessageKey } from '../i18n/fr';
import { requiredTier, type Feature, type Tier } from '../licensing/gate';
import { useGate } from '../licensing/useGate';
import { StateBadge } from './StateMarks';
import { useSemantic, type SemanticRole } from './theme';
import { KCard } from './KCard';

// The tiles of « Plus de fonctionnalités » (#76): a round coloured badge,
// a title and one sentence. A feature the box's level does not include
// shows a lock and the level that includes it, from the gate's own table;
// the demo opens everything, so it never shows one. Information only: no
// price, no way to buy (RFC-006, V1 rule).

export const TIER_LABEL: Record<Tier, MessageKey> = { free: 'pro.freeTier', basic: 'pro.basic', max: 'pro.max' };

/** The lock and the level that includes the feature, or null when it is included. */
export function LevelLock({ feature, testID }: { feature: Feature; testID: string }) {
  const theme = useTheme();
  const { can } = useGate();
  const tier = requiredTier(feature);
  if (can(feature) || !tier) return null;
  const level = t('features.level', { tier: t(TIER_LABEL[tier]) });
  return (
    <View style={styles.lock} testID={testID} accessibilityLabel={`${t('plan.locked')} · ${level}`}>
      <Icon source="lock" size={16} color={theme.colors.onSurfaceVariant} />
      <Text variant="labelMedium" style={{ color: theme.colors.onSurfaceVariant }}>
        {level}
      </Text>
    </View>
  );
}

export function FeatureTile({
  role,
  icon,
  title,
  description,
  feature,
  onPress,
  right,
  testID,
}: {
  role: SemanticRole;
  icon: string;
  title: string;
  description: string;
  /** The gate's feature, when the tile is one a level may not include. */
  feature?: Feature;
  onPress?: () => void;
  /** In place of the chevron: e.g. a switch. */
  right?: () => ReactNode;
  testID: string;
}) {
  const { can } = useGate();
  const locked = !!feature && !can(feature);
  return (
    <List.Item
      title={title}
      titleNumberOfLines={2}
      description={description}
      descriptionNumberOfLines={4}
      left={() => <StateBadge role={role} icon={icon} style={styles.badge} />}
      right={
        right ??
        ((p) =>
          locked && feature ? (
            <LevelLock feature={feature} testID={`${testID}-locked`} />
          ) : (
            <List.Icon {...p} icon="chevron-right" />
          ))
      }
      onPress={onPress}
      testID={testID}
    />
  );
}

/** The coloured tile at the top of Réglages that opens « Plus de fonctionnalités ». */
export function MoreFeaturesTile({ onPress }: { onPress: () => void }) {
  const tone = useSemantic().attention;
  return (
    <KCard style={[styles.more, { backgroundColor: tone.container }]} onPress={onPress} testID="settings-more-features">
      <View style={styles.moreBody}>
        <StateBadge role="attention" icon="star-four-points" size={44} />
        <View style={styles.moreText}>
          <Text variant="titleMedium" style={{ color: tone.onContainer }}>
            {t('features.title')}
          </Text>
          <Text variant="bodyMedium" style={{ color: tone.onContainer }}>
            {t('features.tileHint')}
          </Text>
        </View>
        <Icon source="chevron-right" size={24} color={tone.onContainer} />
      </View>
    </KCard>
  );
}

const styles = StyleSheet.create({
  badge: { marginLeft: 8 },
  lock: { flexDirection: 'row', alignItems: 'center', gap: 4, alignSelf: 'center' },
  more: { marginHorizontal: 8, marginTop: 8, marginBottom: 4 },
  moreBody: { flexDirection: 'row', alignItems: 'center', gap: 12, padding: 16 },
  moreText: { flex: 1, gap: 2 },
});
