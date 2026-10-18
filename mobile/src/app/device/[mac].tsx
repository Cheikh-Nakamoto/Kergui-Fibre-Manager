import { Stack, useLocalSearchParams } from 'expo-router';
import { useState } from 'react';
import { ScrollView, StyleSheet } from 'react-native';
import { Button, HelperText, Text, TextInput } from 'react-native-paper';

import { useDevices, useRename, useSetBlocked } from '../../features/queries';
import { errorMessage, t } from '../../i18n';
import { displayName } from '../../lib/format';
import { confirm } from '../../ui/confirm';

export default function DeviceScreen() {
  const { mac } = useLocalSearchParams<{ mac: string }>();
  const { data } = useDevices();
  const device = data?.find((d) => d.MAC === mac);
  const setBlocked = useSetBlocked();
  const rename = useRename();
  // null until the user edits: shows the stored name, including after a refetch.
  const [draft, setDraft] = useState<string | null>(null);

  if (!device) return <Stack.Screen options={{ headerShown: true, title: mac }} />;
  const name = draft ?? device.CustomName;

  const label = displayName(device);
  const toggle = async () => {
    const blocking = !device.Blocked;
    const ok = await confirm(
      t(blocking ? 'device.blockConfirm' : 'device.unblockConfirm', { name: label }),
      t(blocking ? 'device.blockYes' : 'device.unblockYes'),
      blocking,
    );
    if (ok) setBlocked.mutate({ mac: device.MAC, blocked: blocking });
  };

  const error = setBlocked.error ?? rename.error;

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
        buttonColor={device.Blocked ? undefined : '#B3261E'}
        icon={device.Blocked ? 'lock-open-variant' : 'block-helper'}
        onPress={toggle}
        loading={setBlocked.isPending}
        disabled={setBlocked.isPending}
        testID="device-toggle-block"
      >
        {t(device.Blocked ? 'device.unblock' : 'device.block')}
      </Button>
      {error ? <HelperText type="error">{errorMessage(error)}</HelperText> : null}
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  body: { padding: 24, gap: 16 },
});
