import { router } from 'expo-router';
import { useMemo, useState } from 'react';
import { FlatList, RefreshControl, StyleSheet, View } from 'react-native';
import { Chip, List, Searchbar, Text, useTheme } from 'react-native-paper';

import type { Device } from '../../core/types';
import { useDevices } from '../../features/queries';
import { t } from '../../i18n';
import { displayName, sortDevices } from '../../lib/format';
import { ErrorBanner } from '../../ui/ErrorBanner';

function DeviceRow({ d }: { d: Device }) {
  const theme = useTheme();
  const detail = `${d.IP || '—'} · ${d.MAC}${d.Connected ? '' : ` · ${t('devices.offline')}`}`;
  return (
    <List.Item
      title={displayName(d)}
      // Badges sit under the address: on the right they squeezed the name.
      description={() => (
        <View style={styles.detail}>
          <Text variant="bodyMedium" style={{ color: theme.colors.onSurfaceVariant }}>
            {detail}
          </Text>
          {d.NewlySeen || d.Blocked ? (
            <View style={styles.badges}>
              {d.NewlySeen ? (
                <Chip compact icon="new-box">
                  {t('devices.new')}
                </Chip>
              ) : null}
              {d.Blocked ? (
                <Chip compact icon="block-helper">
                  {t('devices.blocked')}
                </Chip>
              ) : null}
            </View>
          ) : null}
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
  detail: { gap: 6 },
  badges: { flexDirection: 'row', gap: 6, alignItems: 'center' },
  empty: { textAlign: 'center', marginTop: 32, opacity: 0.6 },
});
