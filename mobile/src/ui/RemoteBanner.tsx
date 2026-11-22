import { StyleSheet, View } from 'react-native';
import { Icon, Text, useTheme } from 'react-native-paper';

import type { DevicesView } from '../features/queries';
import { ageWords, freshness } from '../features/remote';
import { t } from '../i18n';
import { clockTime } from '../lib/format';
import { useNow } from '../lib/useNow';

// Where the Devices tab says it is in remote mode (#46): the age of the list
// the relay signed, a warning when it is old, and the relay's state —
// active, standby, offline since …, or never heard. A household reading a
// list from twenty minutes ago is told so, rather than shown it as live.

type Remote = NonNullable<DevicesView['remote']>;

export function RemoteBanner({ remote }: { remote: Remote }) {
  const theme = useTheme();
  const now = useNow(15_000);
  const { ageS, stale } = freshness(remote.asOf, now);
  const relay = remote.relay;

  let relayLine: string;
  switch (relay.state) {
    case 'active':
      relayLine = t('remote.active');
      break;
    case 'standby':
      relayLine = t('remote.standby');
      break;
    case 'offline':
      relayLine = t('remote.offline', { time: clockTime(relay.lastSeenAt), age: ageWords(relay.ageSeconds ?? 0) });
      break;
    default:
      relayLine = t('remote.none');
  }
  const warn = relay.state !== 'active';

  return (
    <View style={[styles.box, { backgroundColor: theme.colors.surfaceVariant }]} testID="remote-banner">
      <View style={styles.row}>
        <Icon source="access-point-network" size={18} />
        <Text variant="labelLarge" style={styles.text} testID="remote-as-of">
          {ageS === null ? t('remote.noList') : t('remote.via', { age: ageWords(ageS) })}
        </Text>
      </View>
      {stale && ageS !== null ? (
        <Text variant="bodySmall" style={{ color: theme.colors.error }} testID="remote-stale">
          {t('remote.stale', { age: ageWords(ageS) })}
        </Text>
      ) : null}
      <View style={styles.row}>
        <Icon source={warn ? 'alert-circle-outline' : 'check-circle-outline'} size={16} color={warn ? theme.colors.error : undefined} />
        <Text variant="bodySmall" style={styles.text} testID={`remote-relay-${relay.state}`}>
          {relayLine}
        </Text>
      </View>
    </View>
  );
}

/**
 * The demo's version (no hub): the same banner, simulated and said to be,
 * once the demo relay has been paired. updatedAt is when the demo list was
 * last read (0: not yet, said as just now).
 */
export function DemoRemoteBanner({ relay, updatedAt }: { relay: string; updatedAt: number }) {
  const theme = useTheme();
  const now = useNow(15_000);
  const ageS = updatedAt ? Math.max(0, Math.round((now - updatedAt) / 1000)) : 0;
  return (
    <View style={[styles.box, { backgroundColor: theme.colors.surfaceVariant }]} testID="remote-banner">
      <View style={styles.row}>
        <Icon source="access-point-network" size={18} />
        <Text variant="labelLarge" style={styles.text} testID="remote-demo">
          {t('remote.demo', { name: relay, age: ageWords(ageS) })}
        </Text>
      </View>
      <Text variant="bodySmall" style={styles.text}>
        {t('remote.demoNote')}
      </Text>
    </View>
  );
}

/**
 * The Conso and Wi-Fi tabs in remote mode (#50): the same screens as at
 * home, with when the relay read the box (as_of) and a warning past three
 * minutes. In the demo (no hub) the demo banner stands in, marked as such.
 */
export function RemoteAsOf({ asOf, kind, testID }: { asOf: string | null; kind: 'usage' | 'ssids'; testID: string }) {
  const theme = useTheme();
  const now = useNow(15_000);
  const { ageS, stale } = freshness(asOf, now);
  return (
    <View style={[styles.box, { backgroundColor: theme.colors.surfaceVariant }]} testID={testID}>
      <View style={styles.row}>
        <Icon source="access-point-network" size={18} />
        <Text variant="labelLarge" style={styles.text} testID={`${testID}-as-of`}>
          {t(kind === 'usage' ? 'remote.usageVia' : 'remote.ssidsVia', { age: ageWords(ageS ?? 0) })}
        </Text>
      </View>
      {stale && ageS !== null ? (
        <Text variant="bodySmall" style={{ color: theme.colors.error }}>
          {t('remote.stale', { age: ageWords(ageS) })}
        </Text>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  box: { marginHorizontal: 12, marginTop: 12, padding: 12, borderRadius: 12, gap: 6 },
  row: { flexDirection: 'row', alignItems: 'center', gap: 8 },
  text: { flexShrink: 1 },
});
