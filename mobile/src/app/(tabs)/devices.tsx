import { router } from 'expo-router';
import { useMemo, useState } from 'react';
import { FlatList, RefreshControl, StyleSheet, View } from 'react-native';
import { Button, Divider, List, Searchbar, SegmentedButtons, Text, useTheme } from 'react-native-paper';

import type { Device } from '../../core/types';
import { useDemoRemote } from '../../features/demoRemote';
import { sameMac, useDemoVisitor, useDevicesView, useWiFiDenied } from '../../features/queries';
import { t } from '../../i18n';
import { displayName, isHidden, matchesDevice, sortDevices } from '../../lib/format';
import { useSession } from '../../session/store';
import { ErrorBanner } from '../../ui/ErrorBanner';
import { HouseInternet } from '../../ui/HouseInternet';
import { DemoRemoteBanner, RemoteBanner } from '../../ui/RemoteBanner';
import { StateBadge, StateChip } from '../../ui/StateMarks';

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
      // The state in a round coloured badge (#75): orange-red cut off the
      // Wi-Fi, red blocked, green connected, neutral offline.
      left={(p) => (
        <StateBadge
          style={p.style}
          role={cut ? 'cut' : d.Blocked ? 'blocked' : d.Connected ? 'connected' : undefined}
          icon={cut ? 'wifi-off' : d.Blocked ? 'cancel' : d.Connected ? 'lan-connect' : 'lan-disconnect'}
        />
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
                <StateChip role="cut" icon="wifi-off" testID={`device-cut-badge-${d.MAC}`}>
                  {t('devices.cut')}
                </StateChip>
              ) : null}
              {d.NewlySeen ? (
                <StateChip role="attention" icon="new-box">
                  {t('devices.new')}
                </StateChip>
              ) : null}
              {d.Blocked ? (
                <StateChip role="blocked" icon="block-helper">
                  {t('devices.blocked')}
                </StateChip>
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

/** The demo's visitor (mobilecore/approvals.go, DemoVisitorMAC). */
const DEMO_VISITOR = '02:4b:47:00:00:58';

/** The demo only: its visitor joins or leaves, to show an arrival (#58). */
function DemoVisitor({ devices }: { devices: Device[] }) {
  const visitor = useDemoVisitor();
  const present = devices.some((d) => sameMac(d.MAC, DEMO_VISITOR));
  return (
    <Button
      mode="text"
      compact
      icon={present ? 'account-arrow-right' : 'account-arrow-left'}
      onPress={() => visitor.mutate(!present)}
      loading={visitor.isPending}
      disabled={visitor.isPending}
      testID={present ? 'demo-visitor-leave' : 'demo-visitor-join'}
    >
      {t(present ? 'demo.visitorLeave' : 'demo.visitorJoin')}
    </Button>
  );
}

export default function Devices() {
  const view = useDevicesView();
  const { error, isFetching, refetch, dataUpdatedAt } = view;
  // This phone (#86) is not a device to watch: out of the list, the counts
  // and the filter tabs, with one line under the list saying so.
  const listed = view.data?.devices;
  const data = useMemo(() => listed?.filter((d) => !isHidden(d)), [listed]);
  const selfHidden = !!listed?.some((d) => d.Mark === 'Self');
  const remote = view.data?.remote ?? null;
  const demo = useSession((s) => s.session?.mode === 'demo');
  const demoRelay = useDemoRemote((s) => s.relay);
  // The Wi-Fi deny list; null when the box cannot cut Wi-Fi access.
  const denied = useWiFiDenied();
  const [query, setQuery] = useState('');
  // Two screens under the search bar (owner request, 2026-11-23): the devices
  // on the network, and those that are not (blocked by the filter, or cut
  // off the Wi-Fi).
  const [filter, setFilter] = useState<'connected' | 'blocked'>('connected');

  const { list, cut, blocked } = useMemo(() => {
    const matches = (d: Device) => matchesDevice(d, query);
    const all = data ?? [];
    const deny = denied.data ?? [];
    const isCut = (d: Device) => deny.some((m) => sameMac(m, d.MAC));
    // A cut device leaves the main list for the "Accès coupé" section, known
    // ones by name, the others (the box stopped listing them) by MAC, so it
    // can always be given back.
    const hidden = (m: string) => !!listed?.some((d) => isHidden(d) && sameMac(d.MAC, m));
    const cutDevices = deny.filter((m) => !hidden(m)).map((m) => all.find((d) => sameMac(d.MAC, m)) ?? cutOnly(m));
    // Three groups, so it is clear at a glance who is on the network: the
    // devices whose Wi-Fi access is cut, those the box's MAC filter blocks,
    // and those with access.
    const rest = all.filter((d) => !isCut(d) && matches(d));
    return {
      list: sortDevices(rest.filter((d) => !d.Blocked)),
      blocked: sortDevices(rest.filter((d) => d.Blocked)),
      cut: sortDevices(cutDevices.filter(matches)),
    };
  }, [data, listed, denied.data, query]);

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

  // Everything above the list scrolls with it: in remote mode the banners
  // alone filled a phone's screen, and the fixed header left no room for the
  // list, which could not be reached.
  const top = (
    <>
      {/* The top of the list, for the Maestro flows to scroll back to. */}
      <View testID="devices-top" style={styles.topMark} />
      <ErrorBanner error={error} onRetry={refetch} />
      {remote ? <RemoteBanner remote={remote} /> : null}
      {!remote && demo && demoRelay ? <DemoRemoteBanner relay={demoRelay} updatedAt={dataUpdatedAt} /> : null}
      <HouseInternet />
      {demo && !remote && data ? <DemoVisitor devices={data} /> : null}
      <Searchbar placeholder={t('devices.search')} accessibilityLabel={t('devices.search')} value={query} onChangeText={setQuery} style={styles.search} />
      <Text variant="labelMedium" style={styles.summary}>
        {t('devices.summary', counts)}
      </Text>
      <SegmentedButtons
        value={filter}
        onValueChange={(v) => setFilter(v as 'connected' | 'blocked')}
        style={styles.filter}
        buttons={[
          { value: 'connected', label: t('devices.filterConnected', { count: list.length }), icon: 'lan-connect', testID: 'devices-filter-connected' },
          {
            value: 'blocked',
            label: t('devices.filterBlocked', { count: blocked.length + cut.length }),
            icon: 'cancel',
            testID: 'devices-filter-blocked',
          },
        ]}
      />
    </>
  );

  return (
    <View style={{ flex: 1 }}>
      {filter === 'connected' ? (
        <FlatList
          testID="devices-connected-list"
          data={list}
          keyExtractor={(d) => d.MAC}
          renderItem={({ item }) => <DeviceRow d={item} />}
          ListHeaderComponent={top}
          ListFooterComponent={
            selfHidden ? (
              <Text variant="bodySmall" style={styles.hidden} testID="devices-hidden">
                {t('devices.hiddenSelf')}
              </Text>
            ) : undefined
          }
          keyboardShouldPersistTaps="handled"
          refreshControl={<RefreshControl refreshing={isFetching} onRefresh={refresh} />}
          ListEmptyComponent={!isFetching ? <Text style={styles.empty}>{t('devices.empty')}</Text> : undefined}
        />
      ) : (
        <FlatList
          testID="devices-blocked-list"
          data={[] as Device[]}
          renderItem={() => null}
          keyboardShouldPersistTaps="handled"
          ListHeaderComponent={
            <>
              {top}
              {blocked.length ? (
                <View testID="devices-blocked-section">
                  <List.Subheader>{t('devices.blockedSection', { count: blocked.length })}</List.Subheader>
                  {blocked.map((d) => (
                    <DeviceRow key={d.MAC} d={d} />
                  ))}
                  <Divider />
                </View>
              ) : null}
              {cut.length ? (
                <View testID="devices-cut-section">
                  <List.Subheader>{t('devices.cutSection', { count: cut.length })}</List.Subheader>
                  {cut.map((d) => (
                    <DeviceRow key={d.MAC} d={d} cut />
                  ))}
                </View>
              ) : null}
              {!blocked.length && !cut.length && !isFetching ? <Text style={styles.empty}>{t('devices.noneBlocked')}</Text> : null}
            </>
          }
          refreshControl={<RefreshControl refreshing={isFetching} onRefresh={refresh} />}
        />
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  topMark: { height: 1 },
  search: { margin: 12, marginBottom: 8 },
  filter: { marginHorizontal: 12, marginBottom: 8 },
  summary: { marginHorizontal: 16, marginBottom: 4, opacity: 0.7 },
  detail: { gap: 6 },
  badges: { flexDirection: 'row', gap: 6, alignItems: 'center' },
  empty: { textAlign: 'center', marginTop: 32, opacity: 0.6 },
  hidden: { textAlign: 'center', marginVertical: 16, opacity: 0.6 },
});
