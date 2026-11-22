import { router } from 'expo-router';
import { useMemo, useState } from 'react';
import { FlatList, RefreshControl, StyleSheet, View } from 'react-native';
import { Chip, Divider, List, Searchbar, Text, useTheme } from 'react-native-paper';

import type { Device } from '../../core/types';
import { useDemoRemote } from '../../features/demoRemote';
import { sameMac, useDevicesView, useWiFiDenied } from '../../features/queries';
import { t } from '../../i18n';
import { displayName, matchesDevice, sortDevices } from '../../lib/format';
import { useSession } from '../../session/store';
import { ErrorBanner } from '../../ui/ErrorBanner';
import { HouseInternet } from '../../ui/HouseInternet';
import { DemoRemoteBanner, RemoteBanner } from '../../ui/RemoteBanner';

/** A device the Wi-Fi refuses that the box no longer lists: its MAC alone. */
function cutOnly(mac: string): Device {
  return {
    ID: mac,
    MAC: mac,
    IP: '',
    Hostname: '',
    CustomName: '',
    Vendor: '',
    SSID: '',
    FirstSeen: '',
    LastSeen: '',
    Connected: false,
    Blocked: false,
    NewlySeen: false,
  };
}

function DeviceRow({ d, cut = false }: { d: Device; cut?: boolean }) {
  const theme = useTheme();
  const detail = `${d.IP || '—'} · ${d.MAC}${d.Connected || cut ? '' : ` · ${t('devices.offline')}`}`;
  return (
    <List.Item
      title={displayName(d)}
      left={(p) => (
        <List.Icon {...p} icon={cut ? 'wifi-off' : d.Blocked ? 'cancel' : d.Connected ? 'lan-connect' : 'lan-disconnect'} />
      )}
      // Badges sit under the address: on the right they squeezed the name.
      description={() => (
        <View style={styles.detail}>
          <Text variant="bodyMedium" style={{ color: theme.colors.onSurfaceVariant }}>
            {detail}
          </Text>
          {d.NewlySeen || d.Blocked || cut ? (
            <View style={styles.badges}>
              {cut ? (
                <Chip compact icon="wifi-off" testID={`device-cut-badge-${d.MAC}`}>
                  {t('devices.cut')}
                </Chip>
              ) : null}
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
  const view = useDevicesView();
  const { error, isFetching, refetch, dataUpdatedAt } = view;
  const data = view.data?.devices;
  const remote = view.data?.remote ?? null;
  const demo = useSession((s) => s.session?.mode === 'demo');
  const demoRelay = useDemoRemote((s) => s.relay);
  // The Wi-Fi deny list; null when the box cannot cut Wi-Fi access.
  const denied = useWiFiDenied();
  const [query, setQuery] = useState('');

  const { list, cut } = useMemo(() => {
    const matches = (d: Device) => matchesDevice(d, query);
    const all = data ?? [];
    const deny = denied.data ?? [];
    const isCut = (d: Device) => deny.some((m) => sameMac(m, d.MAC));
    // A cut device leaves the main list for the "Accès coupé" section, known
    // ones by name, the others (the box stopped listing them) by MAC, so it
    // can always be given back.
    const cutDevices = deny.map((m) => all.find((d) => sameMac(d.MAC, m)) ?? cutOnly(m));
    return {
      list: sortDevices(all.filter((d) => !isCut(d) && matches(d))),
      cut: sortDevices(cutDevices.filter(matches)),
    };
  }, [data, denied.data, query]);

  const refresh = () => {
    void refetch();
    void denied.refetch();
  };

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
      {remote ? <RemoteBanner remote={remote} /> : null}
      {!remote && demo && demoRelay ? <DemoRemoteBanner relay={demoRelay} updatedAt={dataUpdatedAt} /> : null}
      <HouseInternet />
      <Searchbar placeholder={t('devices.search')} accessibilityLabel={t('devices.search')} value={query} onChangeText={setQuery} style={styles.search} />
      <Text variant="labelMedium" style={styles.summary}>
        {t('devices.summary', counts)}
      </Text>
      <FlatList
        data={list}
        keyExtractor={(d) => d.MAC}
        renderItem={({ item }) => <DeviceRow d={item} />}
        ListHeaderComponent={
          cut.length ? (
            <View testID="devices-cut-section">
              <List.Subheader>{t('devices.cutSection')}</List.Subheader>
              {cut.map((d) => (
                <DeviceRow key={d.MAC} d={d} cut />
              ))}
              <Divider />
            </View>
          ) : undefined
        }
        refreshControl={<RefreshControl refreshing={isFetching} onRefresh={refresh} />}
        ListEmptyComponent={!isFetching && !cut.length ? <Text style={styles.empty}>{t('devices.empty')}</Text> : undefined}
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
