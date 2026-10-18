import { router } from 'expo-router';
import { useMemo, useState } from 'react';
import { FlatList, RefreshControl, StyleSheet, View } from 'react-native';
import { Chip, List, Searchbar, Text } from 'react-native-paper';

import type { Device } from '../../core/types';
import { useDevices } from '../../features/queries';
import { t } from '../../i18n';
import { displayName, sortDevices } from '../../lib/format';
import { ErrorBanner } from '../../ui/ErrorBanner';

function DeviceRow({ d }: { d: Device }) {
  return (
    <List.Item
      title={displayName(d)}
      description={`${d.IP || '—'} · ${d.MAC}${d.Connected ? '' : ` · ${t('devices.offline')}`}`}
      left={(p) => <List.Icon {...p} icon={d.Blocked ? 'cancel' : d.Connected ? 'lan-connect' : 'lan-disconnect'} />}
      right={() => (
        <View style={styles.badges}>
          {d.NewlySeen ? <Chip compact>{t('devices.new')}</Chip> : null}
          {d.Blocked ? <Chip compact icon="block-helper">{t('devices.blocked')}</Chip> : null}
        </View>
      )}
      onPress={() => router.push({ pathname: '/device/[mac]', params: { mac: d.MAC } })}
      testID={`device-${d.MAC}`}
    />
  );
}

export default function Devices() {
  const { data, error, isFetching, refetch } = useDevices();
  const [query, setQuery] = useState('');

  const list = useMemo(() => {
    const q = query.trim().toLowerCase();
    const all = sortDevices(data ?? []);
    if (!q) return all;
    return all.filter((d) =>
      [d.CustomName, d.Hostname, d.MAC, d.IP, d.Vendor].some((v) => v?.toLowerCase().includes(q)),
    );
  }, [data, query]);

  const counts = useMemo(() => {
    const all = data ?? [];
    return {
      connected: all.filter((d) => d.Connected).length,
      blocked: all.filter((d) => d.Blocked).length,
      new: all.filter((d) => d.NewlySeen).length,
    };
  }, [data]);

  return (
    <View style={{ flex: 1 }}>
      <ErrorBanner error={error} onRetry={refetch} />
      <Searchbar placeholder={t('devices.search')} value={query} onChangeText={setQuery} style={styles.search} />
      <Text variant="labelMedium" style={styles.summary}>
        {t('devices.summary', counts)}
      </Text>
      <FlatList
        data={list}
        keyExtractor={(d) => d.MAC}
        renderItem={({ item }) => <DeviceRow d={item} />}
        refreshControl={<RefreshControl refreshing={isFetching} onRefresh={() => void refetch()} />}
        ListEmptyComponent={!isFetching ? <Text style={styles.empty}>{t('devices.empty')}</Text> : undefined}
      />
    </View>
  );
}

const styles = StyleSheet.create({
  search: { margin: 12 },
  summary: { marginHorizontal: 16, marginBottom: 4, opacity: 0.7 },
  badges: { flexDirection: 'row', gap: 4, alignItems: 'center' },
  empty: { textAlign: 'center', marginTop: 32, opacity: 0.6 },
});
