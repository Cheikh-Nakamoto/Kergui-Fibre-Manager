import { router, Stack } from 'expo-router';
import { useEffect, useState } from 'react';
import { FlatList, StyleSheet, View, type StyleProp, type ViewStyle } from 'react-native';
import { Button, Divider, HelperText, IconButton, List, Snackbar, Text, TextInput, useTheme } from 'react-native-paper';

import { useInbox, type InboxEntry } from '../alerts/inbox';
import { COMPLAINT_MAX_MESSAGE, ComplaintInputError, complaints } from '../features/complaints';
import { sameMac, useApprove, useDevices } from '../features/queries';
import { errorMessage, t } from '../i18n';
import { clockTime, formatBytes, formatDay } from '../lib/format';
import { Muted } from '../ui/Muted';
import { StateBadge, StateChip } from '../ui/StateMarks';
import { buttonSlop, iconButtonSlop } from '../ui/targets';
import { interFamilies, useChrome } from '../ui/theme';
import { useUpdate } from '../update/update';

/** « 14:02 » today, « 21/11/2026 · 14:02 » before. */
function when(at: number): string {
  const d = new Date(at);
  const iso = d.toISOString();
  const today = new Date().toDateString() === d.toDateString();
  return today ? clockTime(iso) : `${formatDay(iso)} · ${clockTime(iso)}`;
}

/** Deletes one notification; the screen offers « Annuler » after (#125). */
type OnRemove = (id: string) => void;

/**
 * The « × » of a row (#125, audit M7): Paper's default size (a 24 dp icon in
 * 40 dp), grown to a 48 × 48 dp target by its hitSlop.
 */
function RemoveButton({ id, onRemove, style }: { id: string; onRemove: OnRemove; style?: StyleProp<ViewStyle> }) {
  return (
    <IconButton
      style={style}
      icon="close"
      hitSlop={iconButtonSlop}
      accessibilityLabel={t('inbox.remove')}
      onPress={() => onRemove(id)}
      testID={`inbox-remove-${id}`}
    />
  );
}

type RowProps = { e: InboxEntry; unread: boolean; onRemove: OnRemove };

function Row({ e, unread, onRemove, canApprove, approved }: RowProps & { canApprove: boolean; approved: boolean }) {
  const theme = useTheme();
  const approve = useApprove();
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
                icon="check"
                hitSlop={buttonSlop}
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
              <StateChip role="connected" icon="check-decagram" testID={`inbox-approved-${e.mac}`}>
                {t('inbox.approved')}
              </StateChip>
            </View>
          ) : null}
        </View>
      )}
      descriptionNumberOfLines={0}
      style={unread ? { backgroundColor: theme.colors.elevation.level2 } : undefined}
      // Amber: someone new to look at; red: a device the Wi-Fi refused (#75).
      left={(p) => (
        <StateBadge
          style={p.style}
          role={arrival ? 'attention' : 'blocked'}
          icon={arrival ? 'account-alert-outline' : 'wifi-cancel'}
          accessibilityLabel={t(arrival ? 'device.notApproved' : 'inbox.badgeRefused')}
          testID={`inbox-badge-${e.id}`}
        />
      )}
      right={(p) => <RemoveButton style={p.style} id={e.id} onRemove={onRemove} />}
      onPress={() => router.push({ pathname: '/device/[mac]', params: { mac: e.mac } })}
    />
  );
}

/** A new version of the website APK (#74): « Mettre à jour » while it is still offered. */
function UpdateRow({ e, unread, onRemove }: RowProps) {
  const theme = useTheme();
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
              <Button mode="contained-tonal" icon="update" hitSlop={buttonSlop} onPress={() => void update()} testID="inbox-update-install">
                {ready ? t('update.install') : t('update.installSized', { size: formatBytes(release.size) })}
              </Button>
            </View>
          ) : null}
        </View>
      )}
      descriptionNumberOfLines={0}
      style={unread ? { backgroundColor: theme.colors.elevation.level2 } : undefined}
      // Neutral: news from Kergui, not the green of « connecté » (#125).
      left={(p) => (
        <StateBadge style={p.style} role="neutral" icon="update" accessibilityLabel={t('inbox.badgeUpdate')} testID={`inbox-badge-${e.id}`} />
      )}
      right={(p) => <RemoveButton style={p.style} id={e.id} onRemove={onRemove} />}
    />
  );
}

/** An announcement from the Kergui team (#80): its title and text, no device to open. */
function MessageRow({ e, unread, onRemove }: RowProps) {
  const theme = useTheme();
  return (
    <List.Item
      testID={`inbox-${e.id}`}
      title={e.name || t('inbox.message')}
      titleStyle={unread ? styles.unreadTitle : undefined}
      titleNumberOfLines={0}
      description={() => (
        <View style={styles.body}>
          {e.text ? (
            <Text variant="bodyMedium" style={{ color: theme.colors.onSurfaceVariant }}>
              {e.text}
            </Text>
          ) : null}
          <Text variant="labelSmall" style={{ color: theme.colors.onSurfaceVariant }}>
            {t('inbox.message')} · {when(e.at)}
          </Text>
        </View>
      )}
      descriptionNumberOfLines={0}
      style={unread ? { backgroundColor: theme.colors.elevation.level2 } : undefined}
      // Neutral: news from Kergui, nothing to fix on the Wi-Fi (#125).
      left={(p) => (
        <StateBadge style={p.style} role="neutral" icon="bullhorn-outline" accessibilityLabel={t('inbox.message')} testID={`inbox-badge-${e.id}`} />
      )}
      right={(p) => <RemoveButton style={p.style} id={e.id} onRemove={onRemove} />}
    />
  );
}

/**
 * The Kergui team's answer to a complaint (#84): its text, and « Répondre »
 * to write again on that complaint's thread.
 */
function ReplyRow({ e, unread, onRemove }: RowProps) {
  const theme = useTheme();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [sent, setSent] = useState(false);
  const number = e.complaint ?? 0;

  const send = async () => {
    setError('');
    if (!draft.trim()) return;
    setBusy(true);
    try {
      await complaints.reply(number, draft);
      setDraft('');
      setOpen(false);
      setSent(true);
    } catch (err) {
      setError(err instanceof ComplaintInputError ? t('report.tooLong') : errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <List.Item
      testID={`inbox-${e.id}`}
      title={e.name || t('inbox.replyTitle', { number })}
      titleStyle={unread ? styles.unreadTitle : undefined}
      titleNumberOfLines={0}
      description={() => (
        <View style={styles.body}>
          {e.text ? (
            <Text variant="bodyMedium" style={{ color: theme.colors.onSurfaceVariant }} selectable>
              {e.text}
            </Text>
          ) : null}
          <Text variant="labelSmall" style={{ color: theme.colors.onSurfaceVariant }}>
            {t('inbox.reply')} · {when(e.at)}
          </Text>
          {sent ? (
            <HelperText type="info" padding="none" testID={`inbox-answer-sent-${e.id}`}>
              {t('inbox.answerSent', { number })}
            </HelperText>
          ) : null}
          {open ? (
            <View style={styles.answer}>
              <TextInput
                mode="outlined"
                dense
                multiline
                label={t('inbox.answerLabel', { number })}
                value={draft}
                onChangeText={setDraft}
                maxLength={COMPLAINT_MAX_MESSAGE}
                testID={`inbox-answer-input-${e.id}`}
              />
              {error ? (
                <HelperText type="error" padding="none" testID={`inbox-answer-error-${e.id}`}>
                  {error}
                </HelperText>
              ) : null}
              <View style={styles.actions}>
                <Button
                  mode="contained-tonal"
                  icon="send"
                  hitSlop={buttonSlop}
                  onPress={() => void send()}
                  loading={busy}
                  disabled={busy}
                  testID={`inbox-answer-send-${e.id}`}
                >
                  {t('inbox.answerSend')}
                </Button>
                <Button hitSlop={buttonSlop} onPress={() => setOpen(false)} disabled={busy} testID={`inbox-answer-cancel-${e.id}`}>
                  {t('inbox.answerCancel')}
                </Button>
              </View>
            </View>
          ) : number > 0 ? (
            <View style={styles.actions}>
              <Button mode="contained-tonal" icon="reply" hitSlop={buttonSlop} onPress={() => setOpen(true)} testID={`inbox-answer-${e.id}`}>
                {t('inbox.answer')}
              </Button>
            </View>
          ) : null}
        </View>
      )}
      descriptionNumberOfLines={0}
      style={unread ? { backgroundColor: theme.colors.elevation.level2 } : undefined}
      // Neutral: Kergui answering, nothing to fix on the Wi-Fi (#125).
      left={(p) => (
        <StateBadge
          style={p.style}
          role="neutral"
          icon="message-reply-text-outline"
          accessibilityLabel={t('inbox.reply')}
          testID={`inbox-badge-${e.id}`}
        />
      )}
      right={(p) => <RemoveButton style={p.style} id={e.id} onRemove={onRemove} />}
    />
  );
}

/** The notifications' own space, opened from the bell at the top right. */
export default function Notifications() {
  const items = useInbox((s) => s.items);
  const remove = useInbox((s) => s.remove);
  const clear = useInbox((s) => s.clear);
  const restore = useInbox((s) => s.restore);
  const markAllRead = useInbox((s) => s.markAllRead);
  // What « Annuler » puts back (#125, audit M7): the last deletion only, for
  // a few seconds. Undone rather than confirmed: nothing on the box changes.
  const [undo, setUndo] = useState<{ n: number; entries: InboxEntry[]; message: string } | null>(null);
  const bars = useChrome();
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

  const offerUndo = (entries: InboxEntry[], message: string) => {
    if (entries.length) setUndo((u) => ({ n: (u?.n ?? 0) + 1, entries, message }));
  };
  const removeOne = (id: string) => {
    const gone = useInbox.getState().items.filter((e) => e.id === id);
    remove(id);
    offerUndo(gone, t('inbox.removed'));
  };
  const clearAll = () => {
    const gone = useInbox.getState().items;
    clear();
    offerUndo(gone, t('inbox.cleared'));
  };

  return (
    <View style={{ flex: 1 }}>
      <Stack.Screen
        options={{
          headerShown: true,
          title: t('inbox.title'),
          headerRight: () =>
            items.length ? (
              <Button textColor={bars.onBackground} hitSlop={buttonSlop} onPress={clearAll} testID="inbox-clear">
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
            <UpdateRow e={item} unread={unread.has(item.id)} onRemove={removeOne} />
          ) : item.kind === 'message' ? (
            <MessageRow e={item} unread={unread.has(item.id)} onRemove={removeOne} />
          ) : item.kind === 'reply' ? (
            <ReplyRow e={item} unread={unread.has(item.id)} onRemove={removeOne} />
          ) : (
            <Row
              e={item}
              unread={unread.has(item.id)}
              onRemove={removeOne}
              canApprove={canApprove(item.mac)}
              approved={!!find(item.mac)?.Approved}
            />
          )
        }
        ItemSeparatorComponent={Divider}
        ListEmptyComponent={<Muted style={styles.empty}>{t('inbox.empty')}</Muted>}
      />
      <Snackbar
        // A new deletion starts its own few seconds.
        key={undo?.n ?? 0}
        visible={!!undo}
        onDismiss={() => setUndo(null)}
        duration={6000}
        action={{
          label: t('inbox.undo'),
          onPress: () => undo && restore(undo.entries),
          hitSlop: buttonSlop,
          testID: 'inbox-undo',
        }}
        testID="inbox-undo-bar"
      >
        {undo?.message}
      </Snackbar>
    </View>
  );
}

const styles = StyleSheet.create({
  body: { gap: 4, marginTop: 2 },
  actions: { flexDirection: 'row', flexWrap: 'wrap', gap: 8, marginTop: 4 },
  answer: { gap: 4, marginTop: 4 },
  unreadTitle: { fontFamily: interFamilies.semibold },
  empty: { textAlign: 'center', marginTop: 48, marginHorizontal: 24 },
});
