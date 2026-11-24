import { Linking, ScrollView, StyleSheet, View } from 'react-native';
import { ActivityIndicator, Button, Icon, Surface, Text, useTheme } from 'react-native-paper';
import { SafeAreaView } from 'react-native-safe-area-context';

import { t } from '../i18n';
import { formatBytes } from '../lib/format';
import { DOWNLOAD_PAGE, useUpdate } from '../update/update';

// Where the self-update (#74) shows: a discreet banner at the bottom for an
// optional version, a screen over everything below the minimum version.
// The notifications screen carries the same offer (inbox kind « update »).

function openDownloadPage() {
  void Linking.openURL(DOWNLOAD_PAGE).catch(() => undefined);
}

/** « Installer des applications inconnues »: explained before Android asks, then the way out. */
function Permission() {
  const theme = useTheme();
  const permission = useUpdate((s) => s.permission);
  const openSettings = useUpdate((s) => s.openSettings);
  if (!permission) return null;
  const refused = permission === 'refused';
  return (
    <View style={styles.permission} testID={`update-permission-${permission}`}>
      <Text variant="titleSmall">{t('update.permissionTitle')}</Text>
      <Text variant="bodyMedium" style={refused ? { color: theme.colors.error } : undefined}>
        {t(refused ? 'update.permissionRefused' : 'update.permissionBody')}
      </Text>
      <View style={styles.actions}>
        <Button mode="contained-tonal" compact onPress={() => void openSettings()} testID="update-permission-settings">
          {t('update.permissionAllow')}
        </Button>
        {refused ? (
          <Button compact onPress={openDownloadPage} testID="update-download-page">
            {t('update.openDownloadPage')}
          </Button>
        ) : null}
      </View>
    </View>
  );
}

function Failure() {
  const theme = useTheme();
  const failure = useUpdate((s) => s.failure);
  if (!failure) return null;
  return (
    <Text variant="bodySmall" style={{ color: theme.colors.error }} testID={`update-failed-${failure}`}>
      {t(failure === 'corrupt' ? 'update.failedCorrupt' : 'update.failedNetwork')}
    </Text>
  );
}

/** « Mettre à jour », with the size while nothing is downloaded yet. */
function UpdateButton({ mode }: { mode: 'contained' | 'contained-tonal' }) {
  const phase = useUpdate((s) => s.phase);
  const size = useUpdate((s) => s.release?.size ?? 0);
  const update = useUpdate((s) => s.update);
  if (phase === 'downloading') {
    return (
      <View style={styles.row} testID="update-downloading">
        <ActivityIndicator size="small" />
        <Text variant="bodyMedium">{t('update.downloading')}</Text>
      </View>
    );
  }
  return (
    <Button mode={mode} compact onPress={() => void update()} testID="update-install">
      {phase === 'ready' ? t('update.install') : t('update.installSized', { size: formatBytes(size) })}
    </Button>
  );
}

function Required() {
  const theme = useTheme();
  const release = useUpdate((s) => s.release);
  const phase = useUpdate((s) => s.phase);
  return (
    <SafeAreaView style={[StyleSheet.absoluteFill, { backgroundColor: theme.colors.background }]} testID="update-required">
      <ScrollView contentContainerStyle={styles.required}>
        <Icon source="update" size={48} color={theme.colors.primary} />
        <Text variant="headlineSmall" style={styles.center}>
          {t('update.requiredTitle')}
        </Text>
        <Text variant="bodyLarge" style={styles.center}>
          {t('update.requiredBody')}
        </Text>
        {release ? (
          <>
            <Text variant="titleMedium">{t('update.version', { version: release.versionName })}</Text>
            {release.notes ? <Text variant="bodyMedium">{release.notes}</Text> : null}
            {phase !== 'ready' ? <Text variant="bodyMedium">{t('update.size', { size: formatBytes(release.size) })}</Text> : null}
            <UpdateButton mode="contained" />
            <Failure />
            <Permission />
          </>
        ) : (
          <>
            <Text variant="bodyMedium" style={styles.center}>
              {t('update.requiredNoFile')}
            </Text>
            <Button mode="contained" onPress={openDownloadPage} testID="update-download-page">
              {t('update.openDownloadPage')}
            </Button>
          </>
        )}
      </ScrollView>
    </SafeAreaView>
  );
}

function Banner() {
  const theme = useTheme();
  const release = useUpdate((s) => s.release);
  const phase = useUpdate((s) => s.phase);
  const later = useUpdate((s) => s.later);
  if (!release) return null;
  return (
    <SafeAreaView edges={['bottom']} style={{ backgroundColor: theme.colors.elevation.level2 }}>
      <Surface elevation={0} style={styles.banner} testID="update-banner">
        <View style={styles.row}>
          <Icon source={phase === 'ready' ? 'download-circle-outline' : 'update'} size={20} color={theme.colors.primary} />
          <Text variant="titleSmall" testID={phase === 'ready' ? 'update-title-ready' : 'update-title-available'}>
            {t(phase === 'ready' ? 'update.ready' : 'update.available')}
          </Text>
          <Text variant="bodySmall" style={{ color: theme.colors.onSurfaceVariant }}>
            {t('update.version', { version: release.versionName })}
          </Text>
        </View>
        {release.notes ? (
          <Text variant="bodySmall" numberOfLines={3}>
            {release.notes}
          </Text>
        ) : null}
        <Failure />
        <Permission />
        <View style={styles.actions}>
          <UpdateButton mode="contained-tonal" />
          <Button compact onPress={later} testID="update-later">
            {t('update.later')}
          </Button>
        </View>
      </Surface>
    </SafeAreaView>
  );
}

/** Mounted once by the root layout, after the screens. */
export function UpdateHost() {
  const required = useUpdate((s) => s.required);
  const visible = useUpdate((s) => {
    if (!s.release || s.dismissed) return false;
    if (s.phase === 'downloading') return s.byTap;
    return s.phase === 'offer' || s.phase === 'ready' || s.phase === 'failed';
  });
  if (required) return <Required />;
  return visible ? <Banner /> : null;
}

const styles = StyleSheet.create({
  banner: { paddingHorizontal: 16, paddingVertical: 10, gap: 6, backgroundColor: 'transparent' },
  row: { flexDirection: 'row', alignItems: 'center', gap: 8, flexWrap: 'wrap' },
  actions: { flexDirection: 'row', flexWrap: 'wrap', gap: 8, alignItems: 'center' },
  permission: { gap: 6, paddingVertical: 4 },
  required: { flexGrow: 1, justifyContent: 'center', alignItems: 'center', padding: 24, gap: 12 },
  center: { textAlign: 'center' },
});
