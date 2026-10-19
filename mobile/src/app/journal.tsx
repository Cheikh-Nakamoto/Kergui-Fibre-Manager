import { Stack } from 'expo-router';
import { useMemo, useState } from 'react';
import { FlatList, RefreshControl, Share, StyleSheet, View } from 'react-native';
import { Button, Chip, Snackbar, Text, useTheme } from 'react-native-paper';

import type { LogEntry } from '../core/types';
import { useJournal } from '../features/queries';
import { t } from '../i18n';
import { formatLogTime, logLine } from '../lib/format';
import { ErrorBanner } from '../ui/ErrorBanner';

/** Warnings and errors are what someone looking for a problem wants first. */
function isProblem(e: LogEntry) {
  const level = e.level.toLowerCase();
  return level === 'warn' || level === 'error';
}

function Row({ entry }: { entry: LogEntry }) {
  const theme = useTheme();
  const level = entry.level.toLowerCase();
  const colour =
    level === 'error' ? theme.colors.error : level === 'warn' ? theme.colors.tertiary : theme.colors.onSurfaceVariant;
  return (
    <View style={styles.row}>
      <Text variant="labelSmall" style={{ color: colour }}>
        {formatLogTime(entry.time)} · {level}
      </Text>
      <Text variant="bodySmall" style={styles.message}>
        {entry.msg}
      </Text>
    </View>
  );
}

/**
 * The journal: what the app asked the box and what came back, read from the
 * core's own ring buffer.
 *
 * It exists because the people this app is for cannot open Logcat or a Mac's
 * console, and because "ça ne marche pas" is not something anyone can act
 * on. Share hands the whole thing over in one tap.
 */
export default function Journal() {
  const { data, error, isFetching, refetch } = useJournal();
  const [problemsOnly, setProblemsOnly] = useState(false);
  const [notice, setNotice] = useState('');

  const entries = useMemo(() => {
    const all = data ?? [];
    return problemsOnly ? all.filter(isProblem) : all;
  }, [data, problemsOnly]);

  // Newest first on screen: the last thing that happened is what someone
  // came here to read.
  const shown = useMemo(() => [...entries].reverse(), [entries]);

  const share = async () => {
    const text = entries.map(logLine).join('\n');
    if (!text) return;
    try {
      await Share.share({ message: text });
    } catch {
      setNotice(t('journal.shareFailed'));
    }
  };

  return (
    <View style={{ flex: 1 }}>
      <Stack.Screen options={{ headerShown: true, title: t('journal.title') }} />
      <ErrorBanner error={error} onRetry={refetch} />
      <View style={styles.header}>
        <Text variant="bodySmall">{t('journal.hint')}</Text>
        <View style={styles.actions}>
          <Chip selected={!problemsOnly} onPress={() => setProblemsOnly(false)} compact>
            {t('journal.all')}
          </Chip>
          <Chip selected={problemsOnly} onPress={() => setProblemsOnly(true)} compact icon="alert-circle-outline">
            {t('journal.problemsOnly')}
          </Chip>
          <View style={{ flex: 1 }} />
          <Button mode="text" icon="share-variant" onPress={share} disabled={!entries.length} testID="journal-share">
            {t('journal.share')}
          </Button>
        </View>
        <Text variant="labelSmall" style={styles.count}>
          {t('journal.count', { count: entries.length })}
        </Text>
      </View>
      <FlatList
        data={shown}
        keyExtractor={(e) => String(e.seq)}
        renderItem={({ item }) => <Row entry={item} />}
        refreshControl={<RefreshControl refreshing={isFetching} onRefresh={() => void refetch()} />}
        ListEmptyComponent={!isFetching ? <Text style={styles.empty}>{t('journal.empty')}</Text> : undefined}
      />
      <Snackbar visible={!!notice} onDismiss={() => setNotice('')} duration={4000}>
        {notice}
      </Snackbar>
    </View>
  );
}

const styles = StyleSheet.create({
  header: { paddingHorizontal: 16, paddingTop: 12, gap: 8 },
  actions: { flexDirection: 'row', gap: 8, alignItems: 'center' },
  count: { opacity: 0.6 },
  row: { paddingHorizontal: 16, paddingVertical: 6, gap: 2 },
  // A journal is read, not skimmed: a monospaced line keeps a MAC or a
  // status code aligned from one line to the next.
  message: { fontFamily: 'monospace' },
  empty: { textAlign: 'center', marginTop: 32, opacity: 0.6 },
});
