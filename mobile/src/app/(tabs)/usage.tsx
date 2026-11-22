import { router } from 'expo-router';
import { useMemo, useState } from 'react';
import { FlatList, Pressable, RefreshControl, StyleSheet, View } from 'react-native';
import { Searchbar, Text, useTheme } from 'react-native-paper';

import { useDevices, useUsage } from '../../features/queries';
import { t } from '../../i18n';
import { formatBytes } from '../../lib/format';
import { totalBytes, usageRows, type UsageRow } from '../../lib/usage';
import { ErrorBanner } from '../../ui/ErrorBanner';

// The Conso tab (free, RFC-006): the current month per device, most first.
// The phone is the only one reading the box's counters, so the figures are a
// minimum, and the screen says so.

function Row({ r, measurable }: { r: UsageRow; measurable: boolean }) {
  const theme = useTheme();
  const open = r.device ? () => router.push({ pathname: '/device/[mac]', params: { mac: r.mac } }) : undefined;
  return (
    <Pressable onPress={open} disabled={!open} style={styles.row} testID={`usage-row-${r.mac}`} accessibilityRole="button">
      <View style={styles.rowHead}>
        <Text variant="titleMedium" numberOfLines={1} style={styles.name} testID={`usage-name-${r.mac}`}>
          {r.name}
        </Text>
        <Text variant="titleMedium" testID={`usage-bytes-${r.mac}`}>
          {measurable ? formatBytes(r.bytes) : '—'}
        </Text>
      </View>
      <View style={[styles.track, { backgroundColor: theme.colors.surfaceVariant }]}>
        <View style={[styles.bar, { width: `${Math.round(r.share * 100)}%`, backgroundColor: theme.colors.primary }]} />
      </View>
      <Text variant="bodySmall" style={{ color: theme.colors.onSurfaceVariant }}>
        {!measurable || r.bytes === 0
          ? t('usage.nothing')
          : t('usage.split', { sent: formatBytes(r.sent), received: formatBytes(r.received) })}
      </Text>
    </Pressable>
  );
}

export default function UsageTab() {
  const theme = useTheme();
  const devices = useDevices();
  const usage = useUsage();
  const [query, setQuery] = useState('');

  const measurable = usage.data?.measurable ?? true;
  const all = usage.data?.usage;
  const rows = useMemo(() => usageRows(devices.data ?? [], all ?? [], query), [devices.data, all, query]);
  const total = totalBytes(all ?? []);

  const refresh = () => {
    void usage.refetch();
    void devices.refetch();
  };

  const empty = usage.isFetching || devices.isFetching ? undefined : query.trim() ? t('usage.noMatch') : t('devices.empty');

  return (
    <View style={{ flex: 1 }} testID="usage-screen">
      <ErrorBanner error={usage.error ?? devices.error} onRetry={refresh} />
      <Searchbar
        placeholder={t('devices.search')}
        accessibilityLabel={t('devices.search')}
        value={query}
        onChangeText={setQuery}
        style={styles.search}
        testID="usage-search"
      />
      <View style={styles.header}>
        {measurable ? (
          <Text variant="titleMedium" testID="usage-total">
            {t('usage.total', { total: formatBytes(total) })}
          </Text>
        ) : (
          <Text variant="bodyMedium" testID="usage-unsupported">
            {t('usage.unsupported')}
          </Text>
        )}
        <Text variant="bodySmall" style={{ color: theme.colors.onSurfaceVariant }} testID="usage-estimate">
          {t('usage.estimate')}
        </Text>
        {measurable && usage.data && total === 0 ? (
          <Text variant="bodySmall" style={{ color: theme.colors.onSurfaceVariant }} testID="usage-empty">
            {t('usage.empty')}
          </Text>
        ) : null}
      </View>
      <FlatList
        data={rows}
        keyExtractor={(r) => r.mac}
        renderItem={({ item }) => <Row r={item} measurable={measurable} />}
        refreshControl={<RefreshControl refreshing={usage.isFetching} onRefresh={refresh} />}
        ListEmptyComponent={empty ? <Text style={styles.emptyList}>{empty}</Text> : undefined}
      />
    </View>
  );
}

const styles = StyleSheet.create({
  search: { margin: 12 },
  header: { marginHorizontal: 16, marginBottom: 8, gap: 4 },
  row: { paddingHorizontal: 16, paddingVertical: 10, gap: 6 },
  rowHead: { flexDirection: 'row', alignItems: 'center', gap: 12 },
  name: { flex: 1 },
  track: { height: 6, borderRadius: 3, overflow: 'hidden' },
  bar: { height: 6, borderRadius: 3 },
  emptyList: { textAlign: 'center', marginTop: 32, opacity: 0.6 },
});
