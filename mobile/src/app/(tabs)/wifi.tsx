import { useState } from 'react';
import { Keyboard, RefreshControl, ScrollView, StyleSheet, View } from 'react-native';
import { Button, Card, HelperText, Switch, Text, TextInput } from 'react-native-paper';

import { RemoteCommandError } from '../../core/remote';
import type { WiFiSSID } from '../../core/types';
import { useDemoRemote } from '../../features/demoRemote';
import { useBlockedDevices, useDevices, useRemoteMode, useSSIDsView, useUpdateSSID } from '../../features/queries';
import { t } from '../../i18n';
import { onNetwork } from '../../lib/format';
import { useGate } from '../../licensing/useGate';
import { ActionError } from '../../ui/ActionError';
import { confirm } from '../../ui/confirm';
import { useSession } from '../../session/store';
import { ErrorBanner } from '../../ui/ErrorBanner';
import { DemoRemoteBanner, RemoteAsOf } from '../../ui/RemoteBanner';
import { UnblockAll } from '../../ui/UnblockAll';
import { useSemantic } from '../../ui/theme';
import { KCard } from '../../ui/KCard';

/**
 * One network. thisPhone: this phone is on it; last: it is the last one on.
 * Neither is turned off from here (#113): the app would lose the box, and
 * the core refuses it anyway.
 */
function SSIDCard({ ssid, remote, thisPhone, last }: { ssid: WiFiSSID; remote: boolean; thisPhone: boolean; last: boolean }) {
  const update = useUpdateSSID();
  // The relay refused it as not validated on this box model (#50): the
  // controls stay off and the sentence says why.
  const unverified = update.error instanceof RemoteCommandError && update.error.reason === 'unverified';
  const { guard } = useGate();
  const tones = useSemantic();
  const [name, setName] = useState(ssid.name);
  const [editingPassword, setEditingPassword] = useState(false);
  const [password, setPassword] = useState('');
  const [passwordChanged, setPasswordChanged] = useState(false);
  const locked = !!ssid.locked;
  const id = ssid.id;
  const keptOn = ssid.enabled && (thisPhone || last);

  // The box's name wins once it changes (a rename here or elsewhere).
  const [boxName, setBoxName] = useState(ssid.name);
  if (boxName !== ssid.name) {
    setBoxName(ssid.name);
    setName(ssid.name);
  }

  // Turning a network off is Max (SSID management); turning it back on is
  // free forever, so a lapsed pass never leaves the Wi-Fi off. Only turning
  // it off is asked first (#119): it cuts the devices on it.
  const toggle = () => {
    if (keptOn) return;
    if (!ssid.enabled) return guard('network_on', () => update.mutate({ id, change: { enabled: true } }));
    return guard('ssid_manage', async () => {
      const yes = await confirm({
        title: t('wifi.disableTitle', { name: ssid.name }),
        message: t('wifi.disableConfirm'),
        confirmLabel: t('wifi.disableYes'),
        destructive: true,
        icon: 'wifi-off',
      });
      if (yes) update.mutate({ id, change: { enabled: false } });
    });
  };

  const newName = name.trim();
  const renaming = !!newName && newName !== ssid.name;
  const rename = () =>
    guard('ssid_manage', async () => {
      // Leave the name field: the next tap on it selects the name again.
      Keyboard.dismiss();
      if (await confirm(t('wifi.renameConfirm', { name: ssid.name, newName }), t('wifi.renameYes'))) {
        update.mutate({ id, change: { name: newName } });
      }
    });

  // The password is its own Max feature; the editor opens only when it is
  // unlocked, and the box's password is never shown.
  const openPassword = () =>
    guard('wifi_password', () => {
      setPasswordChanged(false);
      setEditingPassword(true);
    });
  const closePassword = () => {
    setEditingPassword(false);
    setPassword('');
  };
  const savePassword = () =>
    guard('wifi_password', async () => {
      // This phone loses the Wi-Fi with the old password (#113): the
      // success line below is rarely seen, so the next step is said first.
      const message = t('wifi.passwordConfirm', { name: ssid.name });
      if (await confirm(thisPhone ? `${message} ${t('wifi.passwordThisPhone')}` : message, t('wifi.passwordYes'))) {
        update.mutate(
          { id, change: { password } },
          {
            onSuccess: () => {
              closePassword();
              setPasswordChanged(true);
            },
          },
        );
      }
    });

  return (
    <KCard style={styles.card} testID={`wifi-ssid-${id}`}>
      <Card.Title
        titleVariant="titleMedium"
        title={ssid.name}
        subtitle={[ssid.band, ssid.security, ssid.hidden && t('wifi.hidden'), !ssid.enabled && t('wifi.disabled')]
          .filter(Boolean)
          .join(' · ')}
        right={() => (
          <Switch
            style={styles.switch}
            testID={`wifi-ssid-switch-${id}`}
            value={ssid.enabled}
            // Green: the network is on (#75).
            color={tones.connected.color}
            onValueChange={toggle}
            disabled={locked || unverified || update.isPending || keptOn}
            accessibilityLabel={ssid.name}
          />
        )}
        subtitleNumberOfLines={2}
      />
      <Card.Content style={styles.content}>
        {thisPhone ? (
          <Text variant="bodySmall" testID={`wifi-ssid-this-phone-${id}`}>
            {t('wifi.thisPhone')}
          </Text>
        ) : last && ssid.enabled ? (
          <Text variant="bodySmall" testID={`wifi-ssid-last-${id}`}>
            {t('wifi.lastNetwork')}
          </Text>
        ) : null}
        {locked ? (
          <Text variant="bodySmall">{ssid.locked}</Text>
        ) : (
          <>
            <View style={styles.row}>
              <TextInput
                dense
                style={styles.grow}
                testID={`wifi-ssid-name-${id}`}
                label={t('wifi.name')}
                value={name}
                onChangeText={setName}
                autoCorrect={false}
                // Renaming usually replaces the whole name: select it.
                selectTextOnFocus
              />
              <Button
                mode="outlined"
                testID={`wifi-ssid-rename-${id}`}
                onPress={rename}
                disabled={!renaming || unverified || update.isPending}
              >
                {t('wifi.rename')}
              </Button>
            </View>
            {remote ? (
              <HelperText type="info" testID={`wifi-ssid-password-local-${id}`}>
                {t('remote.passwordLocal')}
              </HelperText>
            ) : editingPassword ? (
              <>
                <TextInput
                  dense
                  testID={`wifi-ssid-password-${id}`}
                  label={t('wifi.newPassword')}
                  value={password}
                  onChangeText={setPassword}
                  secureTextEntry
                  autoCapitalize="none"
                  autoCorrect={false}
                />
                <View style={styles.actions}>
                  <Button testID={`wifi-ssid-password-cancel-${id}`} onPress={closePassword} disabled={update.isPending}>
                    {t('common.cancel')}
                  </Button>
                  <Button
                    mode="contained"
                    testID={`wifi-ssid-password-save-${id}`}
                    onPress={savePassword}
                    loading={update.isPending}
                    disabled={update.isPending || password.length < 8}
                  >
                    {t('common.save')}
                  </Button>
                </View>
              </>
            ) : (
              <Button
                mode="text"
                icon="key-variant"
                testID={`wifi-ssid-password-open-${id}`}
                onPress={openPassword}
                style={styles.start}
              >
                {t('wifi.passwordChange')}
              </Button>
            )}
            {passwordChanged ? (
              <HelperText type="info" testID={`wifi-ssid-password-done-${id}`}>
                {t('wifi.passwordChanged')}
              </HelperText>
            ) : null}
          </>
        )}
        <ActionError error={update.error} />
      </Card.Content>
    </KCard>
  );
}

export default function WiFi() {
  const view = useSSIDsView();
  const ssids = { ...view, data: view.data?.ssids };
  // Through the household's relay (#50): the networks as the relay read
  // them; « Débloquer tous » and the Wi-Fi password stay at home.
  const remote = !!useRemoteMode().data;
  const demo = useSession((s) => s.session?.mode === 'demo');
  const demoRelay = useDemoRemote((s) => s.relay);
  // The network this phone is on, by the device list (#113): the device the
  // core marked Self. None through the relay: this phone is away.
  const devices = useDevices();
  const selfSSID = remote ? '' : (devices.data?.find((d) => d.Mark === 'Self')?.SSID ?? '');
  const enabledCount = (ssids.data ?? []).filter((s) => s.enabled).length;
  // The box's deny lists, for « Débloquer tous » (read at home only).
  const held = useBlockedDevices();
  const refresh = () => {
    void ssids.refetch();
    if (!remote) void held.refetch();
  };

  return (
    <ScrollView refreshControl={<RefreshControl refreshing={ssids.isFetching} onRefresh={refresh} />}>
      <ErrorBanner error={ssids.error} onRetry={ssids.refetch} />
      {remote ? <RemoteAsOf asOf={view.data?.asOf ?? null} kind="ssids" testID="wifi-remote" /> : null}
      {!remote && demo && demoRelay ? <DemoRemoteBanner relay={demoRelay} updatedAt={view.dataUpdatedAt} /> : null}
      {/* No switch for the box's filter (#115): Kergui keeps it on itself.
          The way to undo every block reads the box's lists, at home only. */}
      {remote ? (
        <HelperText type="info" testID="wifi-unblock-all-local">
          {t('remote.unblockAllLocal')}
        </HelperText>
      ) : (
        <UnblockAll />
      )}
      <View style={styles.list} testID="wifi-screen">
        {(ssids.data ?? []).map((s) => (
          <SSIDCard key={s.id} ssid={s} remote={remote} thisPhone={onNetwork(selfSSID, s)} last={s.enabled && enabledCount <= 1} />
        ))}
        {ssids.data && ssids.data.length === 0 ? <Text style={styles.empty}>{t('wifi.empty')}</Text> : null}
      </View>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  list: { padding: 12, gap: 12 },
  card: {},
  content: { gap: 8 },
  row: { flexDirection: 'row', alignItems: 'center', gap: 8 },
  grow: { flex: 1 },
  actions: { flexDirection: 'row', justifyContent: 'flex-end', gap: 8 },
  start: { alignSelf: 'flex-start' },
  switch: { marginRight: 16 },
  empty: { textAlign: 'center', marginTop: 32, opacity: 0.6 },
});
