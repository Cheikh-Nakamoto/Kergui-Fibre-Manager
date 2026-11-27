import { Stack, useLocalSearchParams } from 'expo-router';
import { useState } from 'react';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Button, Text, TextInput } from 'react-native-paper';

import {
  sameMac,
  useApprove,
  useAutoReject,
  useDevices,
  useRename,
  useSetBlocked,
  useSetWiFiCut,
  useRemoteMode,
  useWiFiDenied,
  useWithdrawApproval,
} from '../../features/queries';
import { t } from '../../i18n';
import { deviceIcon, deviceSubline, isPrivateMac } from '../../lib/deviceName';
import { displayName } from '../../lib/format';
import { useGate } from '../../licensing/useGate';
import { ActionError } from '../../ui/ActionError';
import { confirm } from '../../ui/confirm';
import { Fold } from '../../ui/Fold';
import { LockedButton } from '../../ui/LockedButton';
import { withMono } from '../../ui/Mono';
import { StateBadge, StateChip } from '../../ui/StateMarks';
import { useSemantic } from '../../ui/theme';
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

export default function DeviceScreen() {
  // Red for what blocks or cuts, green for what gives access back (#75).
  const tones = useSemantic();
  const { mac } = useLocalSearchParams<{ mac: string }>();
  const { data } = useDevices();
  const device = data?.find((d) => sameMac(d.MAC, mac));
  const denied = useWiFiDenied();
  const setBlocked = useSetBlocked();
  const setCut = useSetWiFiCut();
  const rename = useRename();
  const autoReject = useAutoReject();
  const approve = useApprove();
  const withdraw = useWithdrawApproval();
  const { guard, can } = useGate();
  // Through the household's relay (#46): no box session from this phone.
  const remote = !!useRemoteMode().data;
  // null until the user edits: shows the stored name, including after a refetch.
  const [draft, setDraft] = useState<string | null>(null);

  // null: the box model has no Wi-Fi access control, so no "Couper l'accès".
  const canCut = Array.isArray(denied.data);
  const cut = !!denied.data?.some((m) => sameMac(m, mac));
  // This phone and the owner's phones (#113): Kergui never blocks them, so
  // the sheet offers neither « Bloquer » nor « Couper l'accès » (the core
  // refuses them anyway). What blocked them before stays undoable, for free.
  const neverBlocked = device?.Mark === 'Self' || device?.Mark === 'Owner';

  const toggleCut = (label: string) => {
    const cutting = !cut;
    // Cutting Wi-Fi access is Basic; giving it back is free forever.
    return guard(
      cutting ? 'cut_wifi_access' : 'restore_wifi_access',
      async () => {
        const ok = await confirm(
          t(cutting ? 'device.cutConfirm' : 'device.restoreConfirm', { name: label }),
          t(cutting ? 'device.cutYes' : 'device.restoreYes'),
          cutting,
        );
        if (ok) setCut.mutate({ mac: device?.MAC ?? mac, cut: cutting });
      },
      t(cutting ? 'device.cut' : 'device.restore'),
    );
  };

  const cutButton = (label: string) =>
    neverBlocked && !cut ? null : !canCut && remote && denied.isSuccess ? (
      // Through the relay, on a model whose Wi-Fi access control is not yet
      // validated on hardware (#50): shown, disabled, and said why.
      <View>
        <Button mode="outlined" icon="wifi-off" disabled testID="device-toggle-wifi-cut">
          {t('device.cut')}
        </Button>
        <Text variant="bodySmall" testID="device-wifi-cut-unverified">
          {t('remote.wifiCutDisabled')}
        </Text>
      </View>
    ) : canCut && !cut && !can('cut_wifi_access') ? (
      // Locked before the tap (#120): the lock and the line under it.
      <LockedButton label={t('device.cut')} onPress={() => toggleCut(label)} testID="device-toggle-wifi-cut" />
    ) : canCut ? (
      <Button
        mode={cut ? 'contained' : 'outlined'}
        buttonColor={cut ? tones.restore.color : undefined}
        textColor={cut ? tones.restore.onColor : tones.blocked.color}
        style={cut ? undefined : { borderColor: tones.blocked.color }}
        icon={cut ? 'wifi' : 'wifi-off'}
        onPress={() => toggleCut(label)}
        loading={setCut.isPending}
        disabled={setCut.isPending}
        testID="device-toggle-wifi-cut"
      >
        {t(cut ? 'device.restore' : 'device.cut')}
      </Button>
    ) : null;

  const cutBadge = cut ? (
    <View style={styles.state}>
      <StateChip role="cut" icon="wifi-off" testID="device-cut-badge">
        {t('devices.cut')}
      </StateChip>
      <Text variant="bodySmall" style={styles.stateText}>
        {t('device.cutState')}
      </Text>
    </View>
  ) : null;

  if (!device && cut) {
    // The box no longer lists a device its Wi-Fi refuses: the sheet still
    // offers to give access back, from the deny list alone.
    return (
      <ScrollView contentContainerStyle={styles.body}>
        {/* Never its MAC as the title (#121): the MAC is in the details. */}
        <Stack.Screen options={{ headerShown: true, title: t('device.unnamed') }} />
        <DeviceHead title={t('device.unnamed')} mac={mac} icon={deviceIcon({})} role="cut" />
        {cutBadge}
        {/* The confirmation names it by its MAC, as its row does: it is all the app knows of it. */}
        {cutButton(mac)}
        <ActionError error={setCut.error} />
        <TechDetails mac={mac} />
      </ScrollView>
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
  const toggle = () => {
    const blocking = !device.Blocked;
    // Blocking is Basic; unblocking is free forever.
    return guard(
      blocking ? 'block' : 'unblock',
      async () => {
        const ok = await confirm(
          t(blocking ? 'device.blockConfirm' : 'device.unblockConfirm', { name: label }),
          t(blocking ? 'device.blockYes' : 'device.unblockYes'),
          blocking,
        );
        if (ok) setBlocked.mutate({ mac: device.MAC, blocked: blocking });
      },
      t(blocking ? 'device.block' : 'device.unblock'),
    );
  };

  const error = setBlocked.error ?? setCut.error ?? rename.error ?? approve.error ?? withdraw.error;

  // Auto-reject is on and nobody authorised this device yet: its deadline.
  const tracked = autoReject.data?.enabled ? autoReject.data.devices.find((d) => sameMac(d.mac, device.MAC)) : undefined;
  const deadline = !device.Approved && tracked?.state === 'pending' ? formatClock(tracked.deadline) : null;

  // One block, one word (#118, audit H2): « Autorisé », or « À vérifier »
  // with one « Autoriser », which also accepts it under auto-reject. Free.
  const approval = device.Approved ? (
    <View style={styles.state}>
      <StateChip role="connected" icon="check-decagram" testID="device-approved-badge">
        {t('device.approved')}
      </StateChip>
      <Text variant="bodySmall" style={styles.stateText}>
        {t('device.approvedState')}
      </Text>
      <Button
        mode="text"
        onPress={() => withdraw.mutate(device.MAC)}
        loading={withdraw.isPending}
        disabled={withdraw.isPending}
        testID="device-unapprove"
      >
        {t('device.unapprove')}
      </Button>
    </View>
  ) : (
    <View style={styles.state}>
      <StateChip
        role="attention"
        icon={deadline ? 'timer-sand' : 'help-circle-outline'}
        testID="device-not-approved-badge"
      >
        {t('device.notApproved')}
      </StateChip>
      <Text variant="bodySmall" style={styles.stateText} testID={deadline ? 'device-pending-state' : undefined}>
        {deadline ? t('device.pendingState', { time: deadline }) : t('device.notApprovedState')}
      </Text>
      <Button
        mode="contained"
        icon="check"
        onPress={() => approve.mutate(device.MAC)}
        loading={approve.isPending}
        disabled={approve.isPending}
        testID="device-approve"
      >
        {t('device.approve')}
      </Button>
    </View>
  );

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
      {cutBadge}
      {approval}
      <UsageCurve mac={device.MAC} />
      {unnamed ? null : renameBlock}
      {neverBlocked ? (
        <Text variant="bodyMedium" testID="device-never-blocked">
          {t(device.Mark === 'Self' ? 'device.thisPhone' : 'device.ownerPhone')}
        </Text>
      ) : null}
      {neverBlocked && !device.Blocked ? null : !device.Blocked && !can('block') ? (
        // Locked before the tap (#120, audit H4): not a full red button.
        <LockedButton label={t('device.block')} onPress={toggle} testID="device-toggle-block" />
      ) : (
        <Button
          mode="contained"
          buttonColor={device.Blocked ? tones.restore.color : tones.blocked.color}
          textColor={device.Blocked ? tones.restore.onColor : tones.blocked.onColor}
          icon={device.Blocked ? 'lock-open-variant' : 'block-helper'}
          onPress={toggle}
          loading={setBlocked.isPending}
          disabled={setBlocked.isPending}
          testID="device-toggle-block"
        >
          {t(device.Blocked ? 'device.unblock' : 'device.block')}
        </Button>
      )}
      {setBlocked.isPending && setBlocked.variables && remote ? (
        <Text variant="bodySmall" testID="device-remote-sending">
          {t('remote.sending')}
        </Text>
      ) : null}
      {setBlocked.isSuccess && setBlocked.data.remote ? (
        // Shown only once the core checked the relay's signature over the
        // answer (#46): the hub alone cannot make this appear.
        <Text variant="bodyMedium" testID="device-remote-done">
          {t(setBlocked.variables.blocked ? 'remote.doneBlock' : 'remote.doneUnblock')}
        </Text>
      ) : null}
      {cutButton(label)}
      <ActionError error={error} />
      <TechDetails mac={device.MAC} ip={device.IP} vendor={device.Vendor} ssid={device.SSID} />
    </ScrollView>
  );
}

/** "14:32": when a pending device will be refused, in the phone's time. */
function formatClock(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '—';
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

const styles = StyleSheet.create({
  body: { padding: 24, gap: 16 },
  state: { flexDirection: 'row', alignItems: 'center', gap: 8, flexWrap: 'wrap' },
  stateText: { flexShrink: 1 },
  head: { flexDirection: 'row', alignItems: 'center', gap: 16 },
  headText: { flex: 1, gap: 2 },
  tech: { paddingHorizontal: 0 },
  details: { paddingVertical: 8 },
});
