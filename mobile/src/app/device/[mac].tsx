import { Stack, useLocalSearchParams } from 'expo-router';
import { useState } from 'react';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Button, Chip, Text, TextInput, useTheme } from 'react-native-paper';

import {
  sameMac,
  useAcceptDevice,
  useAutoReject,
  useDevices,
  useRename,
  useSetBlocked,
  useSetWiFiCut,
  useRemoteMode,
  useWiFiDenied,
} from '../../features/queries';
import { t } from '../../i18n';
import { displayName } from '../../lib/format';
import { useGate } from '../../licensing/useGate';
import { ActionError } from '../../ui/ActionError';
import { confirm } from '../../ui/confirm';
import { UsageCurve } from '../../ui/UsageCurve';

export default function DeviceScreen() {
  const theme = useTheme();
  const { mac } = useLocalSearchParams<{ mac: string }>();
  const { data } = useDevices();
  const device = data?.find((d) => sameMac(d.MAC, mac));
  const denied = useWiFiDenied();
  const setBlocked = useSetBlocked();
  const setCut = useSetWiFiCut();
  const rename = useRename();
  const autoReject = useAutoReject();
  const accept = useAcceptDevice();
  const { guard } = useGate();
  // Through the household's relay (#46): no box session from this phone.
  const remote = !!useRemoteMode().data;
  // null until the user edits: shows the stored name, including after a refetch.
  const [draft, setDraft] = useState<string | null>(null);

  // null: the box model has no Wi-Fi access control, so no "Couper l'accès".
  const canCut = Array.isArray(denied.data);
  const cut = !!denied.data?.some((m) => sameMac(m, mac));

  const toggleCut = (label: string) => {
    const cutting = !cut;
    // Cutting Wi-Fi access is Basic; giving it back is free forever.
    return guard(cutting ? 'cut_wifi_access' : 'restore_wifi_access', async () => {
      const ok = await confirm(
        t(cutting ? 'device.cutConfirm' : 'device.restoreConfirm', { name: label }),
        t(cutting ? 'device.cutYes' : 'device.restoreYes'),
        cutting,
      );
      if (ok) setCut.mutate({ mac: device?.MAC ?? mac, cut: cutting });
    });
  };

  const cutButton = (label: string) =>
    canCut ? (
      <Button
        mode={cut ? 'contained' : 'outlined'}
        textColor={cut ? undefined : theme.colors.error}
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
      <Chip compact icon="wifi-off" testID="device-cut-badge">
        {t('devices.cut')}
      </Chip>
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
        <Stack.Screen options={{ headerShown: true, title: mac }} />
        <Text variant="bodyMedium" selectable>
          {t('device.details', { mac, ip: '—', vendor: '—', ssid: '—' })}
        </Text>
        {cutBadge}
        {cutButton(mac)}
        <ActionError error={setCut.error} />
      </ScrollView>
    );
  }

  if (!device) {
    return (
      <>
        <Stack.Screen options={{ headerShown: true, title: mac }} />
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
    return guard(blocking ? 'block' : 'unblock', async () => {
      const ok = await confirm(
        t(blocking ? 'device.blockConfirm' : 'device.unblockConfirm', { name: label }),
        t(blocking ? 'device.blockYes' : 'device.unblockYes'),
        blocking,
      );
      if (ok) setBlocked.mutate({ mac: device.MAC, blocked: blocking });
    });
  };

  const error = setBlocked.error ?? setCut.error ?? rename.error ?? accept.error;

  // Auto-reject is on and nobody accepted this device yet.
  const tracked = autoReject.data?.enabled ? autoReject.data.devices.find((d) => sameMac(d.mac, device.MAC)) : undefined;
  const pending =
    tracked?.state === 'pending' ? (
      <View style={styles.state}>
        <Chip compact icon="timer-sand" testID="device-pending-badge">
          {t('device.pending')}
        </Chip>
        <Text variant="bodySmall" style={styles.stateText}>
          {t('device.pendingState', { time: formatClock(tracked.deadline) })}
        </Text>
        <Button
          mode="contained"
          icon="check"
          onPress={() => accept.mutate(device.MAC)}
          loading={accept.isPending}
          disabled={accept.isPending}
          testID="device-accept"
        >
          {t('device.accept')}
        </Button>
      </View>
    ) : null;

  return (
    <ScrollView contentContainerStyle={styles.body}>
      <Stack.Screen options={{ headerShown: true, title: label }} />
      <Text variant="bodyMedium" selectable>
        {t('device.details', {
          mac: device.MAC,
          ip: device.IP || '—',
          vendor: device.Vendor || '—',
          ssid: device.SSID || '—',
        })}
      </Text>
      {cutBadge}
      {pending}
      <UsageCurve mac={device.MAC} />
      <TextInput label={t('device.rename')} value={name} onChangeText={setDraft} placeholder={device.Hostname} />
      <Button
        mode="outlined"
        onPress={() => rename.mutate({ mac: device.MAC, name: name.trim() }, { onSuccess: () => setDraft(null) })}
        loading={rename.isPending}
        disabled={rename.isPending || name.trim() === device.CustomName}
      >
        {t('common.save')}
      </Button>
      <Button
        mode="contained"
        buttonColor={device.Blocked ? undefined : theme.colors.error}
        textColor={device.Blocked ? undefined : theme.colors.onError}
        icon={device.Blocked ? 'lock-open-variant' : 'block-helper'}
        onPress={toggle}
        loading={setBlocked.isPending}
        disabled={setBlocked.isPending}
        testID="device-toggle-block"
      >
        {t(device.Blocked ? 'device.unblock' : 'device.block')}
      </Button>
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
});
