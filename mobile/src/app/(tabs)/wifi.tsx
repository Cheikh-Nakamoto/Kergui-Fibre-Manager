import { useState } from 'react';
import { Keyboard, RefreshControl, ScrollView, StyleSheet, View } from 'react-native';
import { Button, Card, HelperText, List, Switch, Text, TextInput } from 'react-native-paper';

import { ApiError } from '../../core/client';
import type { WiFiSSID } from '../../core/types';
import { useFilter, useSetFilter, useSSIDs, useUpdateSSID } from '../../features/queries';
import { errorMessage, t } from '../../i18n';
import { useGate } from '../../licensing/useGate';
import { ActionError } from '../../ui/ActionError';
import { confirm } from '../../ui/confirm';
import { ErrorBanner } from '../../ui/ErrorBanner';

function SSIDCard({ ssid }: { ssid: WiFiSSID }) {
  const update = useUpdateSSID();
  const { guard } = useGate();
  const [name, setName] = useState(ssid.name);
  const [editingPassword, setEditingPassword] = useState(false);
  const [password, setPassword] = useState('');
  const [passwordChanged, setPasswordChanged] = useState(false);
  const locked = !!ssid.locked;
  const id = ssid.id;

  // The box's name wins once it changes (a rename here or elsewhere).
  const [boxName, setBoxName] = useState(ssid.name);
  if (boxName !== ssid.name) {
    setBoxName(ssid.name);
    setName(ssid.name);
  }

  // Turning a network off is Max (SSID management); turning it back on is
  // free forever, so a lapsed pass never leaves the Wi-Fi off.
  const toggle = () =>
    guard(ssid.enabled ? 'ssid_manage' : 'network_on', async () => {
      const action = t(ssid.enabled ? 'wifi.disable' : 'wifi.enable');
      const message = t(ssid.enabled ? 'wifi.toggleConfirm' : 'wifi.enableConfirm', { action, name: ssid.name });
      if (await confirm(message, action, ssid.enabled)) {
        update.mutate({ id, change: { enabled: !ssid.enabled } });
      }
    });

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
      if (await confirm(t('wifi.passwordConfirm', { name: ssid.name }), t('wifi.passwordYes'))) {
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
    <Card style={styles.card} testID={`wifi-ssid-${id}`}>
      <Card.Title
        title={ssid.name}
        subtitle={[ssid.band, ssid.security, ssid.hidden && t('wifi.hidden'), !ssid.enabled && t('wifi.disabled')]
          .filter(Boolean)
          .join(' · ')}
        right={() => (
          <Switch
            style={styles.switch}
            testID={`wifi-ssid-switch-${id}`}
            value={ssid.enabled}
            onValueChange={toggle}
            disabled={locked || update.isPending}
            accessibilityLabel={ssid.name}
          />
        )}
        subtitleNumberOfLines={2}
      />
      <Card.Content style={styles.content}>
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
                disabled={!renaming || update.isPending}
              >
                {t('wifi.rename')}
              </Button>
            </View>
            {editingPassword ? (
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
    </Card>
  );
}

export default function WiFi() {
  const ssids = useSSIDs();
  const filter = useFilter();
  const setFilter = useSetFilter();
  const { guard } = useGate();
  const filterUnsupported = filter.error instanceof ApiError && filter.error.notSupported;

  // The MAC filter keeps every device not on its list off the Wi-Fi: turning
  // it on cuts Wi-Fi access (Basic), turning it off gives it back (free).
  const toggleFilter = (enabled: boolean) =>
    guard(enabled ? 'cut_wifi_access' : 'restore_wifi_access', async () => {
      if (await confirm(t(enabled ? 'wifi.filterOn' : 'wifi.filterOff'), undefined, enabled)) setFilter.mutate(enabled);
    });

  return (
    <ScrollView
      refreshControl={
        <RefreshControl
          refreshing={ssids.isFetching || filter.isFetching}
          onRefresh={() => {
            void ssids.refetch();
            void filter.refetch();
          }}
        />
      }
    >
      <ErrorBanner error={ssids.error} onRetry={ssids.refetch} />
      {!filterUnsupported ? (
        <List.Item
          title={t('wifi.filter')}
          right={() => (
            <Switch
              accessibilityLabel={t('wifi.filter')}
              value={!!filter.data?.enabled}
              onValueChange={toggleFilter}
              disabled={!filter.data || setFilter.isPending}
            />
          )}
        />
      ) : null}
      {filter.error && !filterUnsupported ? <HelperText type="error">{errorMessage(filter.error)}</HelperText> : null}
      <ActionError error={setFilter.error} />
      <View style={styles.list} testID="wifi-screen">
        {(ssids.data ?? []).map((s) => (
          <SSIDCard key={s.id} ssid={s} />
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
