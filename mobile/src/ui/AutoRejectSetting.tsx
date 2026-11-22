import { useState } from 'react';
import { StyleSheet, View } from 'react-native';
import { Button, Card, List, Switch, Text } from 'react-native-paper';

import { useAutoReject, useDevices, useSetAutoReject } from '../features/queries';
import { t } from '../i18n';
import { displayName } from '../lib/format';
import { useGate } from '../licensing/useGate';
import { ActionError, Locked } from './ActionError';

/**
 * Auto-reject in Settings (Max). Turning it on first shows the devices on
 * the box, which "Tout accepter et activer" accepts in one tap; afterwards
 * only new devices have to be accepted (from their notification, or by
 * naming them). Without Max the switch is off and shows the neutral lock;
 * turning it off is always possible. The setting lives in the core, where
 * the watch service reads it.
 */
export function AutoRejectSetting({ onEnabled }: { onEnabled?: () => void | Promise<void> }) {
  const { can } = useGate();
  const status = useAutoReject();
  const devices = useDevices();
  const set = useSetAutoReject();
  const [offering, setOffering] = useState(false);

  // The box model has no Wi-Fi access control: nothing to refuse with.
  if (status.data === null) return null;

  const enabled = !!status.data?.enabled;
  const allowed = can('auto_reject');
  const accepted = status.data?.devices.filter((d) => d.state === 'accepted').length ?? 0;
  const pending = status.data?.devices.filter((d) => d.state === 'pending').length ?? 0;
  const present = devices.data ?? [];

  const toggle = (on: boolean) => {
    if (!on) {
      if (offering) setOffering(false);
      else if (enabled) set.mutate(false);
      return;
    }
    if (allowed) setOffering(true);
  };

  const acceptAll = () =>
    set.mutate(true, {
      onSuccess: () => {
        setOffering(false);
        void onEnabled?.();
      },
    });

  return (
    <>
      <List.Item
        title={t('settings.autoReject')}
        description={t('settings.autoRejectHint')}
        descriptionNumberOfLines={5}
        left={(p) => <List.Icon {...p} icon="shield-account-outline" />}
        right={() => (
          <Switch
            value={enabled || offering}
            onValueChange={toggle}
            disabled={status.isLoading || set.isPending || (!allowed && !enabled)}
            accessibilityLabel={t('settings.autoReject')}
            testID="settings-auto-reject"
          />
        )}
      />
      {!allowed && !enabled ? <Locked testID="settings-auto-reject-locked" /> : null}
      {enabled ? (
        <View style={styles.state}>
          <Text variant="bodySmall" testID="settings-auto-reject-state">
            {t('settings.autoRejectOn', { count: accepted })}
          </Text>
          {pending > 0 ? (
            <Text variant="bodySmall" testID="settings-auto-reject-pending">
              {t('settings.autoRejectPending', { count: pending })}
            </Text>
          ) : null}
          {!allowed ? (
            <>
              <Text variant="bodySmall">{t('settings.autoRejectStopped')}</Text>
              <Locked testID="settings-auto-reject-locked" />
            </>
          ) : null}
        </View>
      ) : null}
      {offering && !enabled ? (
        <Card mode="outlined" style={styles.offer} testID="settings-auto-reject-offer">
          <Card.Title title={t('settings.autoRejectPresent', { count: present.length })} titleNumberOfLines={2} />
          <Card.Content style={styles.offerBody}>
            <Text variant="bodySmall">{t('settings.autoRejectPresentHint')}</Text>
            {present.length === 0 ? <Text variant="bodySmall">{t('settings.autoRejectNoDevice')}</Text> : null}
            {present.map((d) => (
              <List.Item
                key={d.MAC}
                title={displayName(d)}
                description={d.MAC}
                left={(p) => <List.Icon {...p} icon="check-circle-outline" />}
                testID={`settings-auto-reject-present-${d.MAC.toLowerCase()}`}
              />
            ))}
          </Card.Content>
          <Card.Actions>
            <Button onPress={() => setOffering(false)} disabled={set.isPending} testID="settings-auto-reject-cancel">
              {t('common.cancel')}
            </Button>
            <Button
              mode="contained"
              onPress={acceptAll}
              loading={set.isPending}
              disabled={set.isPending}
              testID="settings-auto-reject-accept-all"
            >
              {t('settings.autoRejectAcceptAll')}
            </Button>
          </Card.Actions>
        </Card>
      ) : null}
      <ActionError error={set.error} />
    </>
  );
}

const styles = StyleSheet.create({
  state: { paddingHorizontal: 16, paddingBottom: 8, gap: 2 },
  offer: { marginHorizontal: 8, marginBottom: 8 },
  offerBody: { gap: 4 },
});
