import { router, Stack } from 'expo-router';
import { useEffect, useState } from 'react';
import { FlatList, StyleSheet, View } from 'react-native';
import { Button, Chip, Divider, IconButton, List, Text, useTheme } from 'react-native-paper';

import { useInbox, type InboxEntry } from '../alerts/inbox';
import { sameMac, useApprove, useDevices } from '../features/queries';
import { t } from '../i18n';
import { clockTime, formatBytes, formatDay } from '../lib/format';
import { confirm } from '../ui/confirm';
import { interFamilies } from '../ui/theme';
import { useUpdate } from '../update/update';

/** « 14:02 » today, « 21/11/2026 · 14:02 » before. */
function when(at: number): string {
  const d = new Date(at);
  const iso = d.toISOString();
  const today = new Date().toDateString() === d.toDateString();
  return today ? clockTime(iso) : `${formatDay(iso)} · ${clockTime(iso)}`;
}

function Row({ e, unread, canApprove, approved }: { e: InboxEntry; unread: boolean; canApprove: boolean; approved: boolean }) {
  const theme = useTheme();
  const approve = useApprove();
  const remove = useInbox((s) => s.remove);
  const arrival = e.kind === 'arrival';
  return (
    <List.Item
      testID={`inbox-${e.id}`}
      title={t(arrival ? 'inbox.arrival' : 'inbox.refused')}
      titleStyle={unread ? styles.unreadTitle : undefined}
      description={() => (
        <View style={styles.body}>
          <Text variant="bodyMedium" style={{ color: theme.colors.onSurfaceVariant }}>
            {e.text || t(arrival ? 'inbox.arrivalBody' : 'inbox.refusedBody', { name: e.name })}
          </Text>
          <Text variant="labelSmall" style={{ color: theme.colors.onSurfaceVariant }}>
            {when(e.at)}
          </Text>
          {arrival && canApprove ? (
            <View style={styles.actions}>
              <Button
                mode="contained-tonal"
                compact
                icon="check"
                onPress={() => approve.mutate(e.mac)}
                loading={approve.isPending}
                disabled={approve.isPending}
                testID={`inbox-approve-${e.mac}`}
              >
                {t('arrivals.approve')}
              </Button>
            </View>
          ) : arrival && approved ? (
            <View style={styles.actions}>
              <Chip compact icon="check-decagram" testID={`inbox-approved-${e.mac}`}>
                {t('inbox.approved')}
              </Chip>
            </View>
          ) : null}
        </View>
      )}
      descriptionNumberOfLines={0}
      style={unread ? { backgroundColor: theme.colors.elevation.level2 } : undefined}
      left={(p) => (
        <List.Icon
          {...p}
          icon={arrival ? 'account-alert-outline' : 'wifi-cancel'}
          color={arrival ? theme.colors.tertiary : theme.colors.error}
        />
      )}
      right={(p) => (
        <IconButton
          {...p}
          icon="close"
          size={18}
          accessibilityLabel={t('inbox.remove')}
          onPress={() => remove(e.id)}
          testID={`inbox-remove-${e.id}`}
        />
      )}
      onPress={() => router.push({ pathname: '/device/[mac]', params: { mac: e.mac } })}
    />
  );
}

/** A new version of the website APK (#74): « Mettre à jour » while it is still offered. */
function UpdateRow({ e, unread }: { e: InboxEntry; unread: boolean }) {
  const theme = useTheme();
  const remove = useInbox((s) => s.remove);
  const release = useUpdate((s) => s.release);
  const phase = useUpdate((s) => s.phase);
  const update = useUpdate((s) => s.update);
  const current = !!release && e.id === `update:${release.build}`;
  const ready = current && phase === 'ready';
  return (
    <List.Item
      testID={`inbox-${e.id}`}
      title={t(ready ? 'update.ready' : 'update.available')}
      titleStyle={unread ? styles.unreadTitle : undefined}
      description={() => (
        <View style={styles.body}>
          <Text variant="bodyMedium" style={{ color: theme.colors.onSurfaceVariant }}>
            {t('update.version', { version: e.name })}
          </Text>
          {e.text ? (
            <Text variant="bodyMedium" style={{ color: theme.colors.onSurfaceVariant }}>
              {e.text}
            </Text>
          ) : null}
          <Text variant="labelSmall" style={{ color: theme.colors.onSurfaceVariant }}>
            {when(e.at)}
          </Text>
          {current && phase !== 'downloading' ? (
            <View style={styles.actions}>
              <Button mode="contained-tonal" compact icon="update" onPress={() => void update()} testID="inbox-update-install">
                {ready ? t('update.install') : t('update.installSized', { size: formatBytes(release.size) })}
              </Button>
            </View>
          ) : null}
        </View>
      )}
      descriptionNumberOfLines={0}
      style={unread ? { backgroundColor: theme.colors.elevation.level2 } : undefined}
      left={(p) => <List.Icon {...p} icon="update" color={theme.colors.primary} />}
      right={(p) => (
        <IconButton
          {...p}
          icon="close"
          size={18}
          accessibilityLabel={t('inbox.remove')}
          onPress={() => remove(e.id)}
          testID={`inbox-remove-${e.id}`}
        />
      )}
    />
  );
}

/** The notifications' own space, opened from the bell at the top right. */
export default function Notifications() {
  const items = useInbox((s) => s.items);
  const clear = useInbox((s) => s.clear);
  const markAllRead = useInbox((s) => s.markAllRead);
  const devices = useDevices();
  // What was unread when the screen opened stays highlighted while it is open.
  const [unread] = useState(() => new Set(useInbox.getState().items.filter((e) => !e.read).map((e) => e.id)));
  useEffect(() => {
    markAllRead();
  }, [items, markAllRead]);

  const find = (mac: string) => devices.data?.find((x) => sameMac(x.MAC, mac));
  const canApprove = (mac: string) => {
    const d = find(mac);
    return !!d && d.Connected !== false && !d.Approved;
  };

  const clearAll = async () => {
    if (await confirm(t('inbox.clearConfirm'), t('inbox.clear'), true)) clear();
  };

  return (
    <View style={{ flex: 1 }}>
      <Stack.Screen
        options={{
          headerShown: true,
          title: t('inbox.title'),
          headerRight: () =>
            items.length ? (
              <Button compact onPress={() => void clearAll()} testID="inbox-clear">
                {t('inbox.clear')}
              </Button>
            ) : null,
        }}
      />
      <FlatList
        testID="inbox-list"
        data={items}
        keyExtractor={(e) => e.id}
        renderItem={({ item }) =>
          item.kind === 'update' ? (
            <UpdateRow e={item} unread={unread.has(item.id)} />
          ) : (
            <Row e={item} unread={unread.has(item.id)} canApprove={canApprove(item.mac)} approved={!!find(item.mac)?.Approved} />
          )
        }
        ItemSeparatorComponent={Divider}
        ListEmptyComponent={<Text style={styles.empty}>{t('inbox.empty')}</Text>}
      />
    </View>
  );
}

const styles = StyleSheet.create({
  body: { gap: 4, marginTop: 2 },
  actions: { flexDirection: 'row', flexWrap: 'wrap', gap: 8, marginTop: 4 },
  unreadTitle: { fontFamily: interFamilies.semibold },
  empty: { textAlign: 'center', marginTop: 48, marginHorizontal: 24, opacity: 0.6 },
});
