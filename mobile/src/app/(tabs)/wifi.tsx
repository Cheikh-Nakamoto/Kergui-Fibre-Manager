import { useState } from 'react';
import { RefreshControl, ScrollView, StyleSheet, View } from 'react-native';
import { Button, Card, HelperText, List, Switch, Text, TextInput } from 'react-native-paper';

import { ApiError } from '../../core/client';
import type { WiFiSSID } from '../../core/types';
import { useFilter, useSetFilter, useSSIDs, useUpdateSSID } from '../../features/queries';
import { errorMessage, t } from '../../i18n';
import type { Feature } from '../../licensing/gate';
import { useGate } from '../../licensing/useGate';
import { confirm } from '../../ui/confirm';
import { ErrorBanner } from '../../ui/ErrorBanner';

function SSIDCard({ ssid }: { ssid: WiFiSSID }) {
  const update = useUpdateSSID();
  const { guard, can } = useGate();
  const [name, setName] = useState(ssid.name);
  const [password, setPassword] = useState('');
  const locked = !!ssid.locked;

  // Turning a network off is Max (SSID management); turning it back on is
  // free forever.
  const toggle = () =>
    guard(ssid.enabled ? 'ssid_manage' : 'network_on', async () => {
      const action = t(ssid.enabled ? 'wifi.disable' : 'wifi.enable');
      if (await confirm(t('wifi.toggleConfirm', { action, name: ssid.name }), action, ssid.enabled)) {
        update.mutate({ id: ssid.id, change: { enabled: !ssid.enabled } });
      }
    });

  const save = () => {
    const renaming = !!name.trim() && name.trim() !== ssid.name;
    const change = {
      ...(renaming ? { name: name.trim() } : {}),
      ...(password ? { password } : {}),
    };
    if (Object.keys(change).length === 0) return;
    // Renaming is SSID management, the password its own feature: both Max.
    const needs: Feature[] = [...(renaming ? (['ssid_manage'] as const) : []), ...(password ? (['wifi_password'] as const) : [])];
    const blocked = needs.find((f) => !can(f));
    return guard(blocked ?? needs[0], async () => {
      if (await confirm(t('wifi.toggleConfirm', { action: t('common.save'), name: ssid.name }))) {
        update.mutate({ id: ssid.id, change }, { onSuccess: () => setPassword('') });
      }
    });
  };

  return (
    <Card style={styles.card}>
      <Card.Title
        title={ssid.name}
        subtitle={[ssid.band, ssid.security, ssid.hidden && t('wifi.hidden'), !ssid.enabled && t('wifi.disabled')]
          .filter(Boolean)
          .join(' · ')}
        right={() => (
          <Switch
            style={styles.switch}
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
            <TextInput dense label={t('wifi.name')} value={name} onChangeText={setName} />
            <TextInput
              dense
              label={t('wifi.newPassword')}
              value={password}
              onChangeText={setPassword}
              secureTextEntry
              autoCapitalize="none"
            />
            <Button
              mode="outlined"
              onPress={save}
              loading={update.isPending}
              disabled={update.isPending || (password !== '' && password.length < 8)}
            >
              {t('common.save')}
            </Button>
          </>
        )}
        {update.error ? <HelperText type="error">{errorMessage(update.error)}</HelperText> : null}
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
      {setFilter.error ? <HelperText type="error">{errorMessage(setFilter.error)}</HelperText> : null}
      <View style={styles.list}>
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
  switch: { marginRight: 16 },
  empty: { textAlign: 'center', marginTop: 32, opacity: 0.6 },
});
