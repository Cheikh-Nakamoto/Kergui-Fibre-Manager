import { useMemo } from 'react';
import { StyleSheet, View } from 'react-native';
import { Button, HelperText, Text } from 'react-native-paper';

import type { BlockedDevice, Device } from '../core/types';
import { sameMac, useBlockedDevices, useDevices, useUnblockAll } from '../features/queries';
import { t } from '../i18n';
import { isHidden } from '../lib/format';
import { useGate } from '../licensing/useGate';
import { ActionError } from './ActionError';
import { confirm } from './confirm';

/** A blocked device as the household knows it: its name, else the box's, else its MAC. */
function nameOf(b: BlockedDevice, devices: Device[]): string {
  const d = devices.find((x) => sameMac(x.MAC, b.mac));
  return d?.CustomName || d?.Hostname || b.name || b.mac;
}

/**
 * « Débloquer tous les appareils bloqués » (#115, audit C3), where the MAC
 * filter switch was: Kergui keeps the box's filter on itself, and this is
 * the explicit way to undo every block at once. It lists the devices before
 * confirming, and gives each its internet back and, when the Wi-Fi refused
 * it, its Wi-Fi access too (RFC-007 D5 « Débloquer »). Free, like every
 * action that gives access back (RFC-006).
 *
 * Only the devices whose every rule kergui wrote: a rule the household typed
 * in the box's page is never touched (#102), and the line under the button
 * names those. This phone and the owner's phones are left out (#113). The
 * list is read from the box itself: the Wi-Fi tab hides this away from home.
 */
export function UnblockAll() {
  const held = useBlockedDevices();
  const devices = useDevices().data;
  const unblock = useUnblockAll();
  const { guard } = useGate();

  const { targets, kept } = useMemo(() => {
    const known = devices ?? [];
    const hidden = (m: string) => known.some((d) => isHidden(d) && sameMac(d.MAC, m));
    const blocked = (held.data ?? []).filter((b) => (b.firewall || b.wifi) && !hidden(b.mac));
    return { targets: blocked.filter((b) => b.by_kergui), kept: blocked.filter((b) => !b.by_kergui) };
  }, [held.data, devices]);

  const list = devices ?? [];
  const n = targets.length;
  const run = () =>
    guard('unblock', async () => {
      const head = n === 1 ? t('wifi.unblockAllConfirmOne') : t('wifi.unblockAllConfirm', { n });
      const lines = targets.map((b) => `• ${[nameOf(b, list), b.wifi ? t('devices.absentWiFi') : ''].filter(Boolean).join(' · ')}`);
      const refused = targets.filter((b) => b.wifi).length;
      const message = [head, lines.join('\n'), refused ? t('wifi.unblockAllWiFi', { n: refused }) : ''].filter(Boolean).join('\n\n');
      if (await confirm(message, t('device.unblockYes'), false)) unblock.mutate(targets);
    });

  if (!n && !kept.length && !unblock.data) return null;
  return (
    <View style={styles.box} testID="wifi-unblock-all-section">
      {n ? (
        <Button
          mode="outlined"
          icon="lock-open-variant"
          onPress={run}
          loading={unblock.isPending}
          disabled={unblock.isPending}
          testID="wifi-unblock-all"
        >
          {t('wifi.unblockAll', { n })}
        </Button>
      ) : null}
      {unblock.data && !unblock.isPending ? (
        <Text variant="bodyMedium" testID="wifi-unblock-all-done">
          {t('wifi.unblockAllDone', { n: unblock.data.done })}
        </Text>
      ) : null}
      {kept.length ? (
        <HelperText type="info" testID="wifi-unblock-all-kept">
          {t('wifi.unblockAllKept', { names: kept.map((b) => nameOf(b, list)).join(', ') })}
        </HelperText>
      ) : null}
      <ActionError error={unblock.error} />
    </View>
  );
}

const styles = StyleSheet.create({
  box: { paddingHorizontal: 12, paddingTop: 12, gap: 4 },
});
