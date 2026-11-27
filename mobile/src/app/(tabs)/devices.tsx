import { router, useLocalSearchParams } from 'expo-router';
import { useMemo, useState } from 'react';
import { FlatList, RefreshControl, StyleSheet, View } from 'react-native';
import { Button, Divider, List, Searchbar, SegmentedButtons, Text, useTheme } from 'react-native-paper';

import type { BlockedDevice, Device } from '../../core/types';
import { useDemoRemote } from '../../features/demoRemote';
import {
  sameMac,
  useBlockedDevices,
  useDemoListFull,
  useDemoVisitor,
  useDevicesView,
  useSetBlocked,
  useWiFiDenied,
} from '../../features/queries';
import { t } from '../../i18n';
import { displayName, formatDay, isHidden, matchesDevice, sortDevices } from '../../lib/format';
import { useGate } from '../../licensing/useGate';
import { ActionError } from '../../ui/ActionError';
import { confirm } from '../../ui/confirm';
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

/**
 * A blocked device that is not on the network (RFC-007 D6): the box still
 * holds its rule. « Débloquer » frees the slot, for free.
 */
function AbsentRow({ b }: { b: BlockedDevice }) {
  const unblock = useSetBlocked();
  const { guard } = useGate();
  const label = b.name || b.mac;
  const free = () =>
    guard('unblock', async () => {
      if (await confirm(t('device.unblockConfirm', { name: label }), t('device.unblockYes'), false)) {
        unblock.mutate({ mac: b.mac, blocked: false });
      }
    });
  return (
    <View>
      <List.Item
        title={label}
        description={`${b.mac} · ${b.last_seen ? t('devices.absentSeen', { date: formatDay(b.last_seen) }) : t('devices.absentNever')}`}
        left={(p) => <StateBadge style={p.style} role="blocked" icon="cancel" />}
        right={() => (
          <Button
            mode="text"
            compact
            onPress={free}
            loading={unblock.isPending}
            disabled={unblock.isPending}
            testID={`absent-unblock-${b.mac}`}
          >
            {t('device.unblock')}
          </Button>
        )}
        testID={`absent-${b.mac}`}
      />
      <ActionError error={unblock.error} />
    </View>
  );
}

/** The demo only: fill its box's list, or free it, to show « liste pleine ». */
function DemoListFull() {
  const fill = useDemoListFull();
  const [full, setFull] = useState(false);
  return (
    <Button
      mode="text"
      compact
      icon={full ? 'tray-remove' : 'tray-full'}
      onPress={() => fill.mutate(!full, { onSuccess: () => setFull(!full) })}
      loading={fill.isPending}
      disabled={fill.isPending}
      testID={full ? 'demo-list-full-off' : 'demo-list-full-on'}
    >
      {t(full ? 'demo.listFullOff' : 'demo.listFullOn')}
    </Button>
  );
}

/**
 * The line under the list (#69): « Ce téléphone », and how many of the
 * owner's phones, are hidden. null when nothing is.
 */
function hiddenWords(devices: Device[]): string | null {
  const self = devices.some((d) => d.Mark === 'Self');
  const owners = devices.filter((d) => d.Mark === 'Owner').length;
  if (self && owners) return t('devices.hiddenSelfAndOwners', { count: owners });
  if (self) return t('devices.hiddenSelf');
  if (owners) return t('devices.hiddenOwners', { count: owners });
  return null;
}

export default function Devices() {
  const view = useDevicesView();
  const { error, isFetching, refetch, dataUpdatedAt } = view;
  // This phone (#86) and the owner's phones (#87) are not devices to watch:
  // out of the list, the counts and the filter tabs, with one line under the
  // list saying so.
  const listed = view.data?.devices;
  const data = useMemo(() => listed?.filter((d) => !isHidden(d)), [listed]);
  const hiddenLine = hiddenWords(listed ?? []);
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
  // « Voir les appareils bloqués absents » under a « liste pleine » (RFC-007
  // D6) opens the « Bloqués » screen.
  const { show } = useLocalSearchParams<{ show?: string }>();
  const [shownFor, setShownFor] = useState<string | undefined>(undefined);
  if (show !== shownFor) {
    // Adjusted while rendering, as React advises for state that follows a prop.
    setShownFor(show);
    if (show === 'absent') setFilter('blocked');
  }
  // Everything the box's lists hold; null in remote mode or when unknown.
  const held = useBlockedDevices();

  const { list, cut, blocked, absent } = useMemo(() => {
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
    // Blocked, and not on the network: the box holds their rule all the
    // same. Those cut off the Wi-Fi are in their own section already.
    const shown = (m: string) => all.some((d) => sameMac(d.MAC, m)) || deny.some((c) => sameMac(c, m));
    const absent = (held.data ?? [])
      .filter((b) => b.firewall && !b.present && !shown(b.mac) && !hidden(b.mac))
      .filter((b) => !query || matchesDevice({ CustomName: b.name, Hostname: '', MAC: b.mac, IP: '', Vendor: '' }, query));
    return {
      list: sortDevices(rest.filter((d) => !d.Blocked)),
      blocked: sortDevices(rest.filter((d) => d.Blocked)),
      cut: sortDevices(cutDevices.filter(matches)),
      absent,
    };
  }, [data, listed, denied.data, held.data, query]);

  const refresh = () => {
    void refetch();
    void denied.refetch();
    void held.refetch();
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
            label: t('devices.filterBlocked', { count: blocked.length + cut.length + absent.length }),
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
            hiddenLine ? (
              <Text variant="bodySmall" style={styles.hidden} testID="devices-hidden">
                {hiddenLine}
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
                  <Divider />
                </View>
              ) : null}
              {absent.length ? (
                <View testID="devices-absent-section">
                  <List.Subheader>{t('devices.absentSection', { count: absent.length })}</List.Subheader>
                  {absent.map((b) => (
                    <AbsentRow key={b.mac} b={b} />
                  ))}
                </View>
              ) : null}
              {demo && !remote ? <DemoListFull /> : null}
              {!blocked.length && !cut.length && !absent.length && !isFetching ? (
                <Text style={styles.empty}>{t('devices.noneBlocked')}</Text>
              ) : null}
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
