import { useState } from 'react';
import { StyleSheet } from 'react-native';
import { List, useTheme } from 'react-native-paper';

import { t } from '../i18n';
import type { MessageKey } from '../i18n/fr';
import { requiredTier, type Feature, type Tier } from '../licensing/gate';
import { useGate } from '../licensing/useGate';
import { Muted } from './Muted';
import { fontStyles, useSemantic } from './theme';

// The box's features (owner request, 2026-11-23): what the box's level includes
// and what stays locked, from the same table as the gate (gate.ts, itself
// the hub's). Information only: the Play app shows no price and no way to
// buy (RFC-006, V1 rule).

/** The features shown, one line each, in the order of the tiers. */
const SHOWN: { feature: Feature; label: MessageKey }[] = [
  { feature: 'device_list', label: 'plan.deviceList' },
  { feature: 'alerts', label: 'plan.alerts' },
  { feature: 'rename', label: 'plan.rename' },
  { feature: 'current_month_consumption', label: 'plan.monthUsage' },
  { feature: 'unblock', label: 'plan.unblock' },
  { feature: 'block', label: 'plan.block' },
  { feature: 'cut_wifi_access', label: 'plan.cutWifi' },
  { feature: 'consumption_history', label: 'plan.usageHistory' },
  { feature: 'ssid_manage', label: 'plan.ssids' },
  { feature: 'wifi_password', label: 'plan.wifiPassword' },
  { feature: 'auto_reject', label: 'plan.autoReject' },
  { feature: 'wifi_lock', label: 'plan.wifiLock' },
];

const TIER_LABEL: Record<Tier, MessageKey> = { free: 'pro.freeTier', basic: 'pro.basic', max: 'pro.max' };

export function PlanFeatures() {
  const theme = useTheme();
  const included = useSemantic().connected.color;
  const { tier, demo, can } = useGate();
  const [open, setOpen] = useState(false);
  const active = SHOWN.filter((s) => can(s.feature)).length;

  return (
    <List.Accordion
      title={t('plan.title', { tier: t(TIER_LABEL[tier]) })}
      description={demo ? t('pro.demo') : t('plan.summary', { active, total: SHOWN.length })}
      descriptionNumberOfLines={2}
      left={(p) => <List.Icon {...p} icon="star-circle-outline" />}
      expanded={open}
      onPress={() => setOpen(!open)}
      testID="settings-plan"
    >
      {(['free', 'basic', 'max'] as const).map((group) => (
        <List.Section key={group} style={styles.section}>
          <List.Subheader style={[styles.subheader, fontStyles.display]}>{t(TIER_LABEL[group])}</List.Subheader>
          {SHOWN.filter((s) => requiredTier(s.feature) === group).map((s) => {
            const ok = can(s.feature);
            return (
              <List.Item
                key={s.feature}
                title={t(s.label)}
                titleNumberOfLines={2}
                titleStyle={ok ? undefined : { color: theme.colors.onSurfaceVariant }}
                left={(p) => (
                  <List.Icon
                    {...p}
                    icon={ok ? 'check-circle' : 'lock-outline'}
                    color={ok ? included : theme.colors.onSurfaceVariant}
                  />
                )}
                accessibilityLabel={`${t(s.label)} : ${t(ok ? 'plan.included' : 'plan.locked')}`}
                testID={`plan-${s.feature}-${ok ? 'on' : 'off'}`}
              />
            );
          })}
        </List.Section>
      ))}
      {!demo && tier !== 'max' ? (
        <Muted variant="bodySmall" style={styles.note}>
          {t('plan.note')}
        </Muted>
      ) : null}
    </List.Accordion>
  );
}

const styles = StyleSheet.create({
  section: { marginVertical: 0 },
  subheader: { paddingBottom: 0 },
  note: { marginHorizontal: 16, marginBottom: 12 },
});
