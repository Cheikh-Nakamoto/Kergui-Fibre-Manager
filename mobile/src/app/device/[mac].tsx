import { Stack, useLocalSearchParams } from 'expo-router';
import { useState } from 'react';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Button, Text, TextInput } from 'react-native-paper';

import type { Device } from '../../core/types';
import { sameMac, useAutoReject, useDevices, useRename, useRemoteMode, useWiFiDenied } from '../../features/queries';
import { useTrustContext } from '../../features/wifiLock';
import { t } from '../../i18n';
import { deviceIcon, deviceSubline, isPrivateMac } from '../../lib/deviceName';
import { displayName } from '../../lib/format';
import { trustPill } from '../../lib/trust';
import { useNow } from '../../lib/useNow';
import { ActionError } from '../../ui/ActionError';
import { Fold } from '../../ui/Fold';
import { withMono } from '../../ui/Mono';
import { StateBadge, StateChip } from '../../ui/StateMarks';
import { TrustChoices } from '../../ui/TrustChoices';
import { TrustUndo } from '../../ui/TrustUndo';
import { UsageCurve } from '../../ui/UsageCurve';

/**
 * The top of the sheet (#121, audit H5): the device's type, its name, and
 * « Adresse privée : peut changer » under it when its address is locally
 * administered (RFC-007 D10).
 */
function DeviceHead({
  title,
  mac,
  icon,
  role,
  sub,
}: {
  title: string;
  mac: string;
  icon: { icon: string; label: string };
  role?: 'cut' | 'blocked' | 'connected';
  sub?: string | null;
}) {
  return (
    <View style={styles.head} testID="device-head">
      <StateBadge role={role} icon={icon.icon} accessibilityLabel={icon.label} size={48} />
      <View style={styles.headText}>
        <Text variant="titleLarge" testID="device-title">
          {title}
        </Text>
        {isPrivateMac(mac) ? (
          <Text variant="bodySmall" testID="device-private-address">
            {t('device.privateAddress')}
          </Text>
        ) : null}
        {sub ? <Text variant="bodySmall">{sub}</Text> : null}
      </View>
    </View>
  );
}

/**
 * MAC, IP, maker and Wi-Fi (#121, audit H5): for a technician, closed at the
 * bottom of the sheet. The household reads the name above.
 */
function TechDetails({ mac, ip, vendor, ssid }: { mac: string; ip?: string; vendor?: string; ssid?: string }) {
  return (
    <Fold title={t('device.tech')} icon="information-outline" style={styles.tech} testID="device-tech">
      <Text variant="bodyMedium" selectable style={styles.details} testID="device-details">
        {withMono(t('device.details', { mac, ip: ip || '—', vendor: vendor || '—', ssid: ssid || '—' }), [mac, ip])}
      </Text>
    </Fold>
  );
}

/**
 * The device's state (#108): the chip of its pill, with « Pas d'internet » /
 * « Ne peut plus se connecter » under « Bloqué » (R7). The ids are the ones
 * the state had before (#58): approved, not approved, blocked.
 */
function TrustState({ d, extra }: { d: Device; extra?: string | null }) {
  const ctx = useTrustContext();
  const now = useNow();
  const from = ctx.from(d);
  // The sheet always says « Autorisé » (#58): only the list's badge hides
  // it on a box that cannot reserve its Wi-Fi (D11).
  const pill = trustPill(d, { capable: true, reserved: ctx.reserved, cut: from.cut, now });
  if (!pill) return null;
  const id = pill.trust === 'blocked' ? 'device-blocked-badge' : pill.trust === 'authorised' ? 'device-approved-badge' : 'device-not-approved-badge';
  return (
    <View style={styles.state} testID="device-trust-state">
      <StateChip role={pill.role} icon={pill.icon} testID={id}>
        {pill.label}
      </StateChip>
      {pill.sub ? (
        <Text variant="bodySmall" style={styles.stateText} testID={from.cut ? 'device-cut-badge' : 'device-blocked-sub'}>
          {pill.sub}
        </Text>
      ) : null}
      {extra ? (
        <Text variant="bodySmall" style={styles.stateText} testID="device-pending-state">
          {extra}
        </Text>
      ) : null}
    </View>
  );
}

export default function DeviceScreen() {
  const { mac } = useLocalSearchParams<{ mac: string }>();
  const { data } = useDevices();
  const device = data?.find((d) => sameMac(d.MAC, mac));
  const denied = useWiFiDenied();
  const rename = useRename();
  const autoReject = useAutoReject();
  // Through the household's relay (#46): no box session from this phone.
  const remote = !!useRemoteMode().data;
  // null until the user edits: shows the stored name, including after a refetch.
  const [draft, setDraft] = useState<string | null>(null);

  // A device the Wi-Fi refuses that the box no longer lists.
  const cut = !!denied.data?.some((m) => sameMac(m, mac));
  // This phone and the owner's phones (#113): never blocked; what blocked
  // them before stays undoable, for free (the choices say it).
  const neverBlocked = device?.Mark === 'Self' || device?.Mark === 'Owner';
  // Through the relay, on a model whose Wi-Fi access control is not yet
  // validated on hardware (#50): the switch is not offered, and said why.
  const cutUnverified =
    remote && denied.isSuccess && !Array.isArray(denied.data) ? (
      <Text variant="bodySmall" testID="device-wifi-cut-unverified">
        {t('remote.wifiCutDisabled')}
      </Text>
    ) : null;

  if (!device && cut) {
    // The box no longer lists a device its Wi-Fi refuses: the sheet still
    // offers to let it back, from the deny list alone.
    const bare = { ...unlisted(mac), Blocked: false };
    return (
      <View style={styles.page}>
        <ScrollView contentContainerStyle={styles.body}>
          {/* Never its MAC as the title (#121): the MAC is in the details. */}
          <Stack.Screen options={{ headerShown: true, title: t('device.unnamed') }} />
          <DeviceHead title={t('device.unnamed')} mac={mac} icon={deviceIcon({})} role="cut" />
          <TrustState d={bare} />
          {/* The undo bar names it by its MAC, as its row does: it is all the app knows of it. */}
          <TrustChoices device={bare} name={mac} testID="device-trust" />
          <TechDetails mac={mac} />
        </ScrollView>
        <TrustUndo />
      </View>
    );
  }

  if (!device) {
    return (
      <>
        <Stack.Screen options={{ headerShown: true, title: t('device.unnamed') }} />
        <Text variant="bodyMedium" style={styles.body}>
          {t('device.notFound')}
        </Text>
      </>
    );
  }
  const name = draft ?? device.CustomName;
  const label = displayName(device);

  // Auto-reject is on and nobody authorised this device yet: its deadline.
  const tracked = autoReject.data?.enabled ? autoReject.data.devices.find((d) => sameMac(d.mac, device.MAC)) : undefined;
  const deadline = !device.Approved && !device.Blocked && tracked?.state === 'pending' ? formatClock(tracked.deadline) : null;

  // A name authorises the device (#58): said once it is done (#118).
  const renamed =
    rename.isSuccess && rename.variables.name && device.Approved ? (
      <Text variant="bodyMedium" testID="device-renamed">
        {t('device.renamed')}
      </Text>
    ) : null;

  // A device the household never named is asked for a name first, at the
  // top (#121, audit H5); a named one keeps its field below.
  const unnamed = !device.CustomName.trim();
  const renameBlock = (
    <>
      <TextInput
        label={t(unnamed ? 'device.nameIt' : 'device.rename')}
        value={name}
        onChangeText={setDraft}
        placeholder={label}
        testID="device-rename-input"
      />
      <Button
        mode={unnamed ? 'contained-tonal' : 'outlined'}
        testID="device-rename-save"
        onPress={() => rename.mutate({ mac: device.MAC, name: name.trim() }, { onSuccess: () => setDraft(null) })}
        loading={rename.isPending}
        disabled={rename.isPending || name.trim() === device.CustomName}
      >
        {t('common.save')}
      </Button>
      {renamed}
    </>
  );

  return (
    <View style={styles.page}>
      <ScrollView contentContainerStyle={styles.body}>
        <Stack.Screen options={{ headerShown: true, title: label }} />
        <DeviceHead
          title={label}
          mac={device.MAC}
          icon={deviceIcon(device)}
          role={cut ? 'cut' : device.Blocked ? 'blocked' : device.Connected ? 'connected' : undefined}
          sub={deviceSubline(device, cut)}
        />
        {unnamed ? renameBlock : null}
        <TrustState d={device} extra={deadline ? t('device.pendingState', { time: deadline }) : null} />
        {neverBlocked ? (
          <Text variant="bodyMedium" testID="device-never-blocked">
            {t(device.Mark === 'Self' ? 'device.thisPhone' : 'device.ownerPhone')}
          </Text>
        ) : null}
        {/* Autorisé | À vérifier | Bloqué, and « Ne plus le laisser se
            connecter du tout » under « Plus d'options » (#108): « Couper
            l'accès » is gone, it became that switch. */}
        <TrustChoices device={device} name={label} testID="device-trust" />
        {cutUnverified}
        <UsageCurve mac={device.MAC} />
        {unnamed ? null : renameBlock}
        <ActionError error={rename.error} />
        <TechDetails mac={device.MAC} ip={device.IP} vendor={device.Vendor} ssid={device.SSID} />
      </ScrollView>
      {/* « Bloqué · Annuler » until the next action (#111 R6). */}
      <TrustUndo />
    </View>
  );
}

/** A device the box no longer lists: its MAC alone. */
function unlisted(mac: string): Device {
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

/** "14:32": when a pending device will be refused, in the phone's time. */
function formatClock(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '—';
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

const styles = StyleSheet.create({
  page: { flex: 1 },
  body: { padding: 24, gap: 16 },
  state: { flexDirection: 'row', alignItems: 'center', gap: 8, flexWrap: 'wrap' },
  stateText: { flexShrink: 1 },
  head: { flexDirection: 'row', alignItems: 'center', gap: 16 },
  headText: { flex: 1, gap: 2 },
  tech: { paddingHorizontal: 0 },
  details: { paddingVertical: 8 },
});
