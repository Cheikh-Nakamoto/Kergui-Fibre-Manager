import { router } from 'expo-router';
import { useMemo, useState } from 'react';
import { FlatList, Pressable, RefreshControl, StyleSheet, View } from 'react-native';
import { IconButton, Searchbar, Text, useTheme } from 'react-native-paper';

import { useDemoRemote } from '../../features/demoRemote';
import { useDevices, useRemoteMode, useUsage, useUsageMonth } from '../../features/queries';
import { t } from '../../i18n';
import { formatBytes } from '../../lib/format';
import { monthLabel, OLDEST_MONTHS_BACK, periodOf, shiftPeriod, totalBytes, usageRows, type UsageRow } from '../../lib/usage';
import { useNow } from '../../lib/useNow';
import { useGate } from '../../licensing/useGate';
import { useSession } from '../../session/store';
import { ErrorBanner } from '../../ui/ErrorBanner';
import { Muted } from '../../ui/Muted';
import { DemoRemoteBanner, RemoteAsOf } from '../../ui/RemoteBanner';

// The Conso tab (RFC-006): the current month per device, most first, is
// free. Going back to an earlier month (up to the 13 the core keeps) is the
// Max monthly filter: without it the arrow shows a lock, the screen reader
// says « pas disponible pour votre compte », and a tap the one neutral
// sentence, titled « Mois précédent » (#120). The phone is the only one reading the box's counters, so the
// figures are a minimum, and the screen says so.

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
  const { guard, can } = useGate();
  const now = useNow();
  const locked = !can('monthly_filter');
  // Through the household's relay (#50): this month, as the relay measured
  // it; the past months are read at home.
  const remote = !!useRemoteMode().data;
  const demo = useSession((s) => s.session?.mode === 'demo');
  const demoRelay = useDemoRemote((s) => s.relay);
  // How many months back the screen shows: 0 is this month (free). A pass
  // that lapses while a past month is shown brings the screen back to this one.
  const [picked, setBack] = useState(0);
  const back = locked ? 0 : picked;
  const period = shiftPeriod(periodOf(new Date(now)), -back);
  const current = useUsage();
  const past = useUsageMonth(period, back > 0 && can('monthly_filter'));
  const usage = back > 0 ? past : current;
  const [query, setQuery] = useState('');

  const measurable = usage.data?.measurable ?? true;
  const all = usage.data?.usage;
  const rows = useMemo(() => usageRows(devices.data ?? [], all ?? [], query), [devices.data, all, query]);
  const total = totalBytes(all ?? []);

  const refresh = () => {
    void usage.refetch();
    void devices.refetch();
  };
  const previous = () =>
    guard('monthly_filter', () => setBack((b) => Math.min(b + 1, OLDEST_MONTHS_BACK)), t('usage.prevMonth'));
  // Coming back towards this month needs nothing: this month is free.
  const next = () => setBack((b) => Math.max(b - 1, 0));

  const empty = usage.isFetching || devices.isFetching ? undefined : query.trim() ? t('usage.noMatch') : t('devices.empty');

  return (
    <View style={{ flex: 1 }} testID="usage-screen">
      <ErrorBanner error={usage.error ?? devices.error} onRetry={refresh} />
      {remote ? <RemoteAsOf asOf={current.data?.asOf ?? null} kind="usage" testID="usage-remote" /> : null}
      {!remote && demo && demoRelay ? <DemoRemoteBanner relay={demoRelay} updatedAt={current.dataUpdatedAt} /> : null}
      <Searchbar
        placeholder={t('devices.search')}
        accessibilityLabel={t('devices.search')}
        value={query}
        onChangeText={setQuery}
        style={styles.search}
        testID="usage-search"
      />
      <View style={styles.months}>
        <IconButton
          icon={locked ? 'lock' : 'chevron-left'}
          onPress={previous}
          disabled={remote || back >= OLDEST_MONTHS_BACK}
          accessibilityLabel={t(locked ? 'usage.prevMonthLocked' : 'usage.prevMonth')}
          testID="usage-month-prev"
        />
        <Text variant="titleSmall" style={styles.month} testID="usage-month">
          {monthLabel(period)}
        </Text>
        <IconButton
          icon="chevron-right"
          onPress={next}
          disabled={back === 0}
          accessibilityLabel={t('usage.nextMonth')}
          testID="usage-month-next"
        />
      </View>
      <View style={styles.header}>
        {remote ? (
          <Text variant="bodySmall" testID="usage-remote-history">
            {t('remote.historyLocal')}
          </Text>
        ) : null}
        {measurable ? (
          <Text variant="titleMedium" testID={back > 0 ? 'usage-month-past' : 'usage-total'}>
            {back > 0
              ? t('usage.totalMonth', { month: monthLabel(period), total: formatBytes(total) })
              : t('usage.total', { total: formatBytes(total) })}
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
            {t(back > 0 ? 'usage.emptyMonth' : 'usage.empty')}
          </Text>
        ) : null}
      </View>
      <FlatList
        data={rows}
        keyExtractor={(r) => r.mac}
        renderItem={({ item }) => <Row r={item} measurable={measurable} />}
        refreshControl={<RefreshControl refreshing={usage.isFetching} onRefresh={refresh} />}
        ListEmptyComponent={empty ? <Muted style={styles.emptyList}>{empty}</Muted> : undefined}
      />
    </View>
  );
}

const styles = StyleSheet.create({
  search: { margin: 12, marginBottom: 0 },
  months: { flexDirection: 'row', alignItems: 'center', justifyContent: 'center' },
  month: { minWidth: 140, textAlign: 'center' },
  header: { marginHorizontal: 16, marginBottom: 8, gap: 4 },
  row: { paddingHorizontal: 16, paddingVertical: 10, gap: 6 },
  rowHead: { flexDirection: 'row', alignItems: 'center', gap: 12 },
  name: { flex: 1 },
  track: { height: 6, borderRadius: 3, overflow: 'hidden' },
  bar: { height: 6, borderRadius: 3 },
  emptyList: { textAlign: 'center', marginTop: 32 },
});
