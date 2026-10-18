import { useState } from 'react';
import { RefreshControl, ScrollView, StyleSheet, View } from 'react-native';
import { Button, Card, HelperText, List, Switch, Text, TextInput } from 'react-native-paper';

import { ApiError } from '../../core/client';
import type { WiFiSSID } from '../../core/types';
import { useFilter, useSetFilter, useSSIDs, useUpdateSSID } from '../../features/queries';
import { errorMessage, t } from '../../i18n';
import { confirm } from '../../ui/confirm';
import { ErrorBanner } from '../../ui/ErrorBanner';

function SSIDCard({ ssid }: { ssid: WiFiSSID }) {
  const update = useUpdateSSID();
  const [name, setName] = useState(ssid.name);
  const [password, setPassword] = useState('');
  const locked = !!ssid.locked;

  const toggle = async () => {
    const action = t(ssid.enabled ? 'wifi.disable' : 'wifi.enable');
    if (await confirm(t('wifi.toggleConfirm', { action, name: ssid.name }), action, ssid.enabled)) {
      update.mutate({ id: ssid.id, change: { enabled: !ssid.enabled } });
    }
  };

  const save = async () => {
    const change = {
      ...(name.trim() && name.trim() !== ssid.name ? { name: name.trim() } : {}),
      ...(password ? { password } : {}),
    };
    if (Object.keys(change).length === 0) return;
    if (await confirm(t('wifi.toggleConfirm', { action: t('common.save'), name: ssid.name }))) {
      update.mutate({ id: ssid.id, change }, { onSuccess: () => setPassword('') });
    }
  };

  return (
    <Card style={styles.card}>
      <Card.Title
        title={ssid.name}
        subtitle={[ssid.band, ssid.security, ssid.hidden && t('wifi.hidden'), !ssid.enabled && t('wifi.disabled')]
          .filter(Boolean)
          .join(' · ')}
        right={() => <Switch value={ssid.enabled} onValueChange={toggle} disabled={locked || update.isPending} />}
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
  const filterUnsupported = filter.error instanceof ApiError && filter.error.notSupported;

  const toggleFilter = async (enabled: boolean) => {
    if (await confirm(t(enabled ? 'wifi.filterOn' : 'wifi.filterOff'), undefined, enabled)) setFilter.mutate(enabled);
  };

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
              value={!!filter.data?.enabled}
              onValueChange={toggleFilter}
              disabled={!filter.data || setFilter.isPending}
            />
          )}
        />
      ) : null}
      {setFilter.error ? <HelperText type="error">{errorMessage(setFilter.error)}</HelperText> : null}
      <View style={styles.list}>
        {(ssids.data ?? []).map((s) => (
          <SSIDCard key={s.id} ssid={s} />
        ))}
      </View>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  list: { padding: 12, gap: 12 },
  card: {},
  content: { gap: 8 },
});
