import { useQueryClient } from '@tanstack/react-query';
import { router, Stack, useLocalSearchParams } from 'expo-router';
import { useMemo, useState } from 'react';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Button, Checkbox, List, Text } from 'react-native-paper';

import type { Device } from '../core/types';
import { approvals } from '../features/approvals';
import { hubBackup } from '../features/hubBackup';
import { keys, sameMac, useDevices } from '../features/queries';
import { useBoxKey, useLockPanel, useLockPrefs, useRelayDevices, useWiFiLock, lockKeys } from '../features/wifiLock';
import { t } from '../i18n';
import { deviceIcon, seenLine } from '../lib/deviceName';
import { displayName } from '../lib/format';
import { houseDevices } from '../lib/trust';
import { useNow } from '../lib/useNow';
import { ActionError } from '../ui/ActionError';
import { Muted } from '../ui/Muted';
import { StateBadge } from '../ui/StateMarks';
import { fontStyles } from '../ui/theme';

/**
 * « Voici les appareils de la maison » (#108, RFC-007 D12, #111 R5, R8):
 * offered once when the feature arrives, and before the first reservation
 * when fewer than 2 devices are authorised. Two groups: the named or
 * authorised ones ticked, the unnamed ones seen lately to tick by hand.
 * This phone, the relay and the owner's phones are ticked for good. Capped
 * at the box's places. « Autoriser ces N appareils », then back to the
 * panel (or the list).
 */
export default function HouseDevices() {
  const { then } = useLocalSearchParams<{ then?: string }>();
  const qc = useQueryClient();
  const { data: devices } = useDevices();
  const lock = useWiFiLock();
  const relays = useRelayDevices();
  const box = useBoxKey();
  const prefs = useLockPrefs();
  const panel = useLockPanel();
  const now = useNow();
  const groups = useMemo(
    () =>
      houseDevices(devices ?? [], {
        relays: relays.data ?? [],
        places: lock.data?.places ?? 0,
        placesKnown: !!lock.data?.places_known,
        now,
      }),
    [devices, relays.data, lock.data, now],
  );
  // null until the household ticks or unticks: the groups' choice.
  const [picked, setPicked] = useState<Set<string> | null>(null);
  const checked = picked ?? groups.checked;
  const [error, setError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);

  const toggle = (mac: string) => {
    const next = new Set(checked);
    if (next.has(mac)) next.delete(mac);
    else next.add(mac);
    setPicked(next);
  };

  const submit = async () => {
    setSaving(true);
    setError(null);
    try {
      const listed = [...groups.named, ...groups.recent];
      const add = listed.filter((d) => checked.has(d.MAC) && !d.Approved).map((d) => d.MAC);
      // Unticked though authorised: not of the house.
      const drop = listed.filter((d) => !checked.has(d.MAC) && d.Approved).map((d) => d.MAC);
      await approvals.approveMany(add);
      for (const m of drop) await approvals.withdraw(m);
      hubBackup.changed();
      await prefs.update(box, { reviewed: true });
      await Promise.all([qc.invalidateQueries({ queryKey: keys.devices }), qc.invalidateQueries({ queryKey: lockKeys.preview })]);
      router.back();
      if (then === 'reserve') panel.show();
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  };

  const row = (d: Device, locked: boolean, sub: string | null) => {
    const kind = deviceIcon(d);
    const on = locked || checked.has(d.MAC);
    return (
      <List.Item
        key={d.MAC}
        title={locked ? alwaysName(d, relays.data ?? []) : displayName(d)}
        description={sub ?? undefined}
        left={(p) => <StateBadge style={p.style} icon={kind.icon} accessibilityLabel={kind.label} role={on ? 'connected' : undefined} />}
        right={() => (
          <Checkbox.Android
            status={on ? 'checked' : 'unchecked'}
            disabled={locked}
            onPress={() => toggle(d.MAC)}
            testID={`house-device-check-${d.MAC}`}
          />
        )}
        onPress={locked ? undefined : () => toggle(d.MAC)}
        disabled={locked}
        accessibilityRole="checkbox"
        accessibilityState={{ checked: on, disabled: locked }}
        testID={`house-device-${d.MAC}`}
      />
    );
  };

  const n = [...groups.named, ...groups.recent].filter((d) => checked.has(d.MAC)).length;
  const label = n === 1 ? t('houseDevices.submitOne') : t('houseDevices.submit', { count: n });

  return (
    <ScrollView contentContainerStyle={styles.body} testID="house-devices">
      <Stack.Screen options={{ headerShown: true, title: t('houseDevices.title') }} />
      <Text variant="bodyLarge">{t('houseDevices.intro')}</Text>
      {groups.capped && lock.data?.places ? (
        <Text variant="bodyMedium" testID="house-devices-capped">
          {t('houseDevices.capped', { places: lock.data.places })}
        </Text>
      ) : null}
      {groups.always.length ? (
        <View>
          <List.Subheader style={fontStyles.display}>{t('houseDevices.always')}</List.Subheader>
          {groups.always.map((d) => row(d, true, null))}
        </View>
      ) : null}
      <View testID="house-devices-named">
        <List.Subheader style={fontStyles.display}>{t('houseDevices.named')}</List.Subheader>
        {groups.named.map((d) => row(d, false, null))}
      </View>
      <View testID="house-devices-recent">
        <List.Subheader style={fontStyles.display}>{t('houseDevices.recent')}</List.Subheader>
        {groups.recent.length ? <Muted variant="bodySmall">{t('houseDevices.recentHint')}</Muted> : null}
        {groups.recent.map((d) => row(d, false, seenLine(d.LastSeen || d.FirstSeen)))}
      </View>
      {!groups.named.length && !groups.recent.length ? <Muted>{t('houseDevices.empty')}</Muted> : null}
      <ActionError error={error} />
      <Button mode="contained" icon="check" onPress={() => void submit()} loading={saving} disabled={saving} testID="house-devices-submit">
        {label}
      </Button>
    </ScrollView>
  );
}

/** « Ce téléphone », « Téléphone du propriétaire », « Relais de la maison ». */
function alwaysName(d: Device, relays: string[]): string {
  if (d.Mark === 'Self') return t('houseDevices.thisPhone');
  if (d.Mark === 'Owner') return t('houseDevices.ownerPhone');
  if (relays.some((m) => sameMac(m, d.MAC))) return t('houseDevices.relay');
  return displayName(d);
}

const styles = StyleSheet.create({
  body: { padding: 16, gap: 8 },
});
