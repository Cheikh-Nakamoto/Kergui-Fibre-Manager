import { useQuery } from '@tanstack/react-query';
import { router, useLocalSearchParams } from 'expo-router';
import { useMemo, useState } from 'react';
import { FlatList, RefreshControl, ScrollView, StyleSheet, View } from 'react-native';
import { Button, Divider, List, Searchbar, SegmentedButtons, Text, useTheme } from 'react-native-paper';

import type { BlockedDevice, Device } from '../../core/types';
import { useDemoRemote } from '../../features/demoRemote';
import { lastList } from '../../features/lastList';
import {
  boxNotThere,
  sameMac,
  useBlockedDevices,
  useDemoListFull,
  useDemoListFullState,
  useDemoVisitor,
  useDevicesView,
  useFreeBlocked,
  useWiFiDenied,
} from '../../features/queries';
import { useRelayDevices, useWiFiLock } from '../../features/wifiLock';
import { api } from '../../core/client';
import { t } from '../../i18n';
import { deviceIcon, deviceSubline, listLine, seenLine } from '../../lib/deviceName';
import { lockView, placesLine } from '../../lib/trust';
import { displayName, isHidden, matchesDevice, sortDevices } from '../../lib/format';
import { useGate } from '../../licensing/useGate';
import { ActionError } from '../../ui/ActionError';
import { confirm } from '../../ui/confirm';
import { useSession } from '../../session/store';
import { ErrorBanner } from '../../ui/ErrorBanner';
import { GoTo } from '../../ui/GoTo';
import { KCard } from '../../ui/KCard';
import { HouseInternet } from '../../ui/HouseInternet';
import { withMono } from '../../ui/Mono';
import { Muted } from '../../ui/Muted';
import { DemoRemoteBanner, RemoteBanner } from '../../ui/RemoteBanner';
import { StateBadge, StateChip } from '../../ui/StateMarks';
import { fontStyles, useSemantic } from '../../ui/theme';
import { TrustPill } from '../../ui/TrustPill';
import { TrustUndo } from '../../ui/TrustUndo';
import { WiFiLockCard } from '../../ui/WiFiLockCard';

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
  // In words (#121, audit H5): the type of device, a readable title, and
  // « Hors ligne » under it; MAC and IP wait in the sheet's « Détails
  // techniques ». Only a cut device the box no longer lists, of which the
  // app knows nothing else, still shows its MAC: it is all that tells it
  // apart.
  const kind = deviceIcon(d);
  const sub = deviceSubline(d, cut);
  const bare = cut && !d.IP && !d.Hostname && !d.CustomName;
  return (
    <List.Item
      title={displayName(d)}
      // The device's type in a round badge coloured by its state (#75):
      // orange-red cut off the Wi-Fi, red blocked, green connected, neutral
      // offline. The state is also said in words (chips, « Hors ligne »).
      left={(p) => (
        <StateBadge
          style={p.style}
          role={cut ? 'cut' : d.Blocked ? 'blocked' : d.Connected ? 'connected' : undefined}
          icon={kind.icon}
          accessibilityLabel={kind.label}
          testID={`device-kind-${d.MAC}`}
        />
      )}
      // Badges sit under the title: on the right they squeezed the name. The
      // device's state (#108): « Autorisé », « À vérifier » or « Bloqué »,
      // with « Pas d'internet » / « Ne peut plus se connecter » (R7); touched,
      // it opens its three choices.
      description={() => (
        <View style={styles.detail}>
          {sub || bare ? (
            <Text variant="bodyMedium" style={{ color: theme.colors.onSurfaceVariant }}>
              {bare ? withMono(d.MAC, [d.MAC]) : sub}
            </Text>
          ) : null}
          <View style={styles.badges}>
            <TrustPill d={d} name={displayName(d)} />
            {d.NewlySeen ? (
              <StateChip role="attention" icon="new-box">
                {t('devices.new')}
              </StateChip>
            ) : null}
          </View>
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
 * holds its rules, internet cut or refused at the Wi-Fi. « Débloquer » frees
 * both, for free, even for an address the app never saw (#103).
 */
function AbsentRow({ b }: { b: BlockedDevice }) {
  const unblock = useFreeBlocked();
  const { guard } = useGate();
  // Its name, never its MAC as a title (#121); the MAC stays under it, the
  // only thing that tells apart devices the app never named.
  const label = b.name || t('device.unnamed');
  const free = () =>
    guard(
      'unblock',
      async () => {
        if (await confirm(t('device.unblockConfirm', { name: label }), t('device.unblockYes'), false)) {
          unblock.mutate(b);
        }
      },
      t('device.unblock'),
    );
  // « Vu hier à 21 h 05 » (#121): the time the app last saw it on the box.
  const seen = seenLine(b.last_seen) ?? t('devices.absentNever');
  return (
    <View>
      <List.Item
        title={label}
        description={withMono([b.mac, seen, b.wifi ? t('devices.absentWiFi') : ''].filter(Boolean).join(' · '), [b.mac])}
        left={(p) => <StateBadge style={p.style} role="blocked" icon="cancel" accessibilityLabel={t('devices.blocked')} />}
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
  // The demo core's state, not this screen's: it survives a new mount (#129).
  const state = useDemoListFullState();
  if (!state.data) return null;
  const full = state.data.full;
  return (
    <Button
      mode="text"
      compact
      icon={full ? 'tray-remove' : 'tray-full'}
      onPress={() => fill.mutate(!full)}
      loading={fill.isPending}
      disabled={fill.isPending}
      testID={full ? 'demo-list-full-off' : 'demo-list-full-on'}
    >
      {t(full ? 'demo.listFullOff' : 'demo.listFullOn')}
    </Button>
  );
}

/**
 * Away from home, and no relay to reach the box from there (#124, audit
 * M1): where the phone is, what a relay is for, the button to install one,
 * and the last list read on this phone, dated. Never a date made up: no list
 * kept, no list shown.
 */
function AwayNoRelay({ onRetry }: { onRetry: () => void }) {
  const theme = useTheme();
  const last = useQuery({ queryKey: ['devices', 'last'], queryFn: lastList.load, gcTime: 0 });
  const kept = last.data ?? null;
  const when = kept ? listLine(kept.at) : null;
  return (
    <View testID="devices-away" style={styles.away}>
      <KCard>
        <View style={styles.awayBody}>
          <StateBadge role="attention" icon="home-export-outline" />
          <Text variant="bodyLarge">{t('devices.awayNoRelay')}</Text>
          <GoTo to="/relay" icon="access-point-network" label={t('go.installRelay')} testID="devices-away-install-relay" />
          <Button mode="text" compact onPress={onRetry} style={styles.awayRetry} testID="devices-away-retry">
            {t('common.retry')}
          </Button>
        </View>
      </KCard>
      {kept && when ? (
        <View testID="devices-away-last">
          <List.Subheader style={fontStyles.display}>{when}</List.Subheader>
          {kept.devices.map((d, i) => (
            <List.Item
              key={`${d.name}-${i}`}
              title={d.name}
              description={t(d.blocked ? 'devices.blocked' : d.connected ? 'devices.awayConnected' : 'devices.offline')}
              descriptionStyle={{ color: theme.colors.onSurfaceVariant }}
              left={(p) => (
                <StateBadge
                  style={p.style}
                  role={d.blocked ? 'blocked' : d.connected ? 'connected' : undefined}
                  icon="devices"
                  accessibilityLabel={t(d.blocked ? 'devices.blocked' : d.connected ? 'devices.awayConnected' : 'devices.offline')}
                />
              )}
            />
          ))}
        </View>
      ) : null}
    </View>
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
  // Away, and no relay to go through (#124): a page of its own, not a banner.
  const away = !demo && !view.remote && !view.household && boxNotThere(error);
  const demoRelay = useDemoRemote((s) => s.relay);
  // The Wi-Fi deny list; null when the box cannot cut Wi-Fi access.
  const denied = useWiFiDenied();
  const [query, setQuery] = useState('');
  // Two screens under the search bar (owner request, 2026-11-23): the devices
  // on the network, and those that are not (blocked by the filter, or cut
  // off the Wi-Fi).
  const [filter, setFilter] = useState<'connected' | 'allowed' | 'blocked'>('connected');
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
  // The closed Wi-Fi (#108): null on a box that cannot (D11), then the
  // card, the « Autorisés » tab and the « Autorisé » state are hidden.
  const lock = useWiFiLock();
  const capable = !!lock.data;
  const tones = useSemantic();
  const relays = useRelayDevices(capable);
  // Authorised devices the box no longer lists: still in the list, still a place.
  const approvedMacs = useQuery({
    queryKey: ['approvals', 'list'],
    enabled: capable,
    queryFn: async () => (await api.approvals()).macs,
  });
  const shownFilter = filter === 'allowed' && !capable ? 'connected' : filter;

  const { list, cut, blocked, allowed, absent } = useMemo(() => {
    const matches = (d: Device) => matchesDevice(d, query);
    const all = data ?? [];
    const deny = denied.data ?? [];
    const isCut = (d: Device) => deny.some((m) => sameMac(m, d.MAC));
    // A cut device leaves the main list for the "Accès coupé" section, known
    // ones by name, the others (the box stopped listing them) by MAC, so it
    // can always be given back.
    const hidden = (m: string) => !!listed?.some((d) => isHidden(d) && sameMac(d.MAC, m));
    // Blocked, and not on the network nor in the list: the box holds their
    // rules all the same, internet cut or refused at the Wi-Fi. Freed from
    // here, both lists at once (« liste pleine », RFC-007 D6).
    const known = (m: string) => all.some((d) => sameMac(d.MAC, m));
    const absent = (held.data ?? [])
      .filter((b) => (b.firewall || b.wifi) && !b.present && !known(b.mac) && !hidden(b.mac))
      .filter((b) => !query || matchesDevice({ CustomName: b.name, Hostname: '', MAC: b.mac, IP: '', Vendor: '' }, query));
    const isAbsent = (m: string) => absent.some((b) => sameMac(b.mac, m));
    const cutDevices = deny
      .filter((m) => !hidden(m) && !isAbsent(m))
      .map((m) => all.find((d) => sameMac(d.MAC, m)) ?? cutOnly(m));
    // Three groups, so it is clear at a glance who is on the network: the
    // devices whose Wi-Fi access is cut, those the box's MAC filter blocks,
    // and those with access.
    const rest = all.filter((d) => !isCut(d) && matches(d));
    // « Autorisés » (#108): switched off or away included.
    const away = (approvedMacs.data ?? [])
      .filter((m) => !listed?.some((d) => sameMac(d.MAC, m)))
      .map((m) => ({ ...cutOnly(m), Approved: true }));
    const allowed = [...rest.filter((d) => d.Approved && !d.Blocked), ...away.filter(matches)];
    return {
      list: sortDevices(rest.filter((d) => !d.Blocked)),
      blocked: sortDevices(rest.filter((d) => d.Blocked)),
      cut: sortDevices(cutDevices.filter(matches)),
      allowed: sortDevices(allowed),
      absent,
    };
  }, [data, listed, denied.data, held.data, query, approvedMacs.data]);

  // « 18 / 32 places » (D3): what the list will hold, this phone, the
  // owner's phones and the relays included; the box's count once reserved.
  const places = useMemo(() => {
    if (!lock.data) return null;
    const macs = new Set<string>();
    for (const d of listed ?? []) if (d.Approved || d.Mark) macs.add(d.MAC.toLowerCase());
    for (const m of approvedMacs.data ?? []) macs.add(m.toLowerCase());
    for (const m of relays.data ?? []) macs.add(m.toLowerCase());
    const count = lockView(lock.data)?.reserved ? Math.max(lock.data.allowed, macs.size) : macs.size;
    return placesLine(count, lock.data.places, lock.data.places_known);
  }, [lock.data, listed, approvedMacs.data, relays.data]);

  const refresh = () => {
    void refetch();
    void denied.refetch();
    void held.refetch();
    void lock.refetch();
    if (capable) void approvedMacs.refetch();
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
      <WiFiLockCard />
      {demo && !remote && data ? <DemoVisitor devices={data} /> : null}
      <Searchbar placeholder={t('devices.search')} accessibilityLabel={t('devices.search')} value={query} onChangeText={setQuery} style={styles.search} />
      <Muted variant="labelMedium" style={styles.summary}>
        {t('devices.summary', counts)}
      </Muted>
      <SegmentedButtons
        value={shownFilter}
        onValueChange={(v) => setFilter(v as 'connected' | 'allowed' | 'blocked')}
        style={styles.filter}
        buttons={[
          { value: 'connected', label: t('devices.filterConnected', { count: list.length }), icon: 'lan-connect', testID: 'devices-filter-connected' },
          // Between the two (#108): only on a box that can reserve its Wi-Fi.
          ...(capable
            ? [{ value: 'allowed', label: t('devices.filterAllowed', { count: allowed.length }), icon: 'check-decagram', testID: 'devices-filter-allowed' }]
            : []),
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

  if (away) {
    return (
      <ScrollView refreshControl={<RefreshControl refreshing={isFetching} onRefresh={refresh} />}>
        <AwayNoRelay onRetry={refresh} />
      </ScrollView>
    );
  }

  return (
    <View style={{ flex: 1 }}>
      {shownFilter === 'allowed' ? (
        <FlatList
          testID="devices-allowed-list"
          data={allowed}
          keyExtractor={(d) => d.MAC}
          renderItem={({ item }) => <DeviceRow d={item} />}
          ListHeaderComponent={
            <>
              {top}
              {places ? (
                <View style={styles.places} testID="devices-places">
                  <Text
                    variant="titleSmall"
                    style={places.warn ? { color: tones.attention.color } : undefined}
                    testID={places.warn ? 'devices-places-warn' : 'devices-places-count'}
                  >
                    {places.text}
                  </Text>
                  {places.full && lock.data ? (
                    <Text variant="bodyMedium" testID="devices-places-full">
                      {t('error.listFull', { count: lock.data.places })}
                    </Text>
                  ) : null}
                  <Muted variant="bodySmall">{t('devices.allowedAlways')}</Muted>
                </View>
              ) : null}
            </>
          }
          keyboardShouldPersistTaps="handled"
          refreshControl={<RefreshControl refreshing={isFetching} onRefresh={refresh} />}
          ListEmptyComponent={!isFetching ? <Muted style={styles.empty}>{t('devices.allowedEmpty')}</Muted> : undefined}
        />
      ) : shownFilter === 'connected' ? (
        <FlatList
          testID="devices-connected-list"
          data={list}
          keyExtractor={(d) => d.MAC}
          renderItem={({ item }) => <DeviceRow d={item} />}
          ListHeaderComponent={top}
          ListFooterComponent={
            hiddenLine ? (
              <Muted variant="bodySmall" style={styles.hidden} testID="devices-hidden">
                {hiddenLine}
              </Muted>
            ) : undefined
          }
          keyboardShouldPersistTaps="handled"
          refreshControl={<RefreshControl refreshing={isFetching} onRefresh={refresh} />}
          ListEmptyComponent={!isFetching ? <Muted style={styles.empty}>{t('devices.empty')}</Muted> : undefined}
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
                  <List.Subheader style={fontStyles.display}>{t('devices.blockedSection', { count: blocked.length })}</List.Subheader>
                  {blocked.map((d) => (
                    <DeviceRow key={d.MAC} d={d} />
                  ))}
                  <Divider />
                </View>
              ) : null}
              {cut.length ? (
                <View testID="devices-cut-section">
                  <List.Subheader style={fontStyles.display}>{t('devices.cutSection', { count: cut.length })}</List.Subheader>
                  {cut.map((d) => (
                    <DeviceRow key={d.MAC} d={d} cut />
                  ))}
                  <Divider />
                </View>
              ) : null}
              {absent.length ? (
                <View testID="devices-absent-section">
                  <List.Subheader style={fontStyles.display}>{t('devices.absentSection', { count: absent.length })}</List.Subheader>
                  {absent.map((b) => (
                    <AbsentRow key={b.mac} b={b} />
                  ))}
                </View>
              ) : null}
              {demo && !remote ? <DemoListFull /> : null}
              {!blocked.length && !cut.length && !absent.length && !isFetching ? (
                <Muted style={styles.empty}>{t('devices.noneBlocked')}</Muted>
              ) : null}
            </>
          }
          refreshControl={<RefreshControl refreshing={isFetching} onRefresh={refresh} />}
        />
      )}
      {/* « Bloqué · Annuler » until the next action (#111 R6). */}
      <TrustUndo />
    </View>
  );
}

const styles = StyleSheet.create({
  topMark: { height: 1 },
  away: { padding: 12, gap: 8 },
  awayBody: { padding: 16, gap: 12, alignItems: 'flex-start' },
  awayRetry: { alignSelf: 'flex-start' },
  search: { margin: 12, marginBottom: 8 },
  filter: { marginHorizontal: 12, marginBottom: 8 },
  summary: { marginHorizontal: 16, marginBottom: 4 },
  detail: { gap: 6 },
  badges: { flexDirection: 'row', gap: 6, alignItems: 'center' },
  empty: { textAlign: 'center', marginTop: 32 },
  hidden: { textAlign: 'center', marginVertical: 16 },
  places: { marginHorizontal: 16, marginBottom: 8, gap: 4 },
});
