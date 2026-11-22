import { StyleSheet, View } from 'react-native';
import { Text, useTheme } from 'react-native-paper';

import { ApiError } from '../core/client';
import { useDeviceUsage } from '../features/queries';
import { t } from '../i18n';
import { formatBytes } from '../lib/format';
import { curveBars, shortMonth } from '../lib/usage';
import { useGate } from '../licensing/useGate';
import { isLocked, Locked } from './ActionError';

const CHART_HEIGHT = 96;

/**
 * A device's consumption over the last 6 months, one bar a month, the
 * current one last (Max: the consumption history, RFC-006). Without Max it
 * shows the lock and the neutral sentence and asks the core nothing; a
 * refusal by the core shows the same.
 */
export function UsageCurve({ mac }: { mac: string }) {
  const theme = useTheme();
  const { can } = useGate();
  const allowed = can('consumption_history');
  const history = useDeviceUsage(mac, allowed);

  const title = (
    <Text variant="titleSmall" testID="device-usage-title">
      {t('device.usageTitle')}
    </Text>
  );
  if (!allowed || isLocked(history.error)) {
    return (
      <View style={styles.box}>
        {title}
        <Locked testID="device-usage-locked" />
      </View>
    );
  }
  if (history.error instanceof ApiError && history.error.notSupported) {
    return (
      <View style={styles.box}>
        {title}
        <Text variant="bodySmall">{t('usage.unsupported')}</Text>
      </View>
    );
  }
  if (!history.data) return <View style={styles.box}>{title}</View>;

  const bars = curveBars(history.data);
  const empty = bars.every((b) => b.bytes === 0);
  return (
    <View style={styles.box} testID="device-usage-chart">
      {title}
      <View style={[styles.chart, { height: CHART_HEIGHT + 36 }]}>
        {bars.map((b) => (
          <View key={b.period} style={styles.column} testID={`device-usage-bar-${b.period}`}>
            <Text variant="labelSmall" numberOfLines={1} style={{ color: theme.colors.onSurfaceVariant }}>
              {b.bytes > 0 ? formatBytes(b.bytes) : ''}
            </Text>
            <View
              style={[
                styles.bar,
                {
                  height: Math.max(2, Math.round(b.share * CHART_HEIGHT)),
                  backgroundColor: b.bytes > 0 ? theme.colors.primary : theme.colors.surfaceVariant,
                },
              ]}
            />
            <Text variant="labelSmall">{shortMonth(b.period)}</Text>
          </View>
        ))}
      </View>
      {empty ? (
        <Text variant="bodySmall" style={{ color: theme.colors.onSurfaceVariant }}>
          {t('device.usageEmpty')}
        </Text>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  box: { gap: 8 },
  chart: { flexDirection: 'row', alignItems: 'flex-end', gap: 6 },
  column: { flex: 1, alignItems: 'center', justifyContent: 'flex-end', gap: 4 },
  bar: { width: '70%', borderRadius: 3 },
});
