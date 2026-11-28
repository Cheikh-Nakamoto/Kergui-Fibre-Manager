import { router } from 'expo-router';
import { useEffect, useMemo, useState } from 'react';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Button, Card, Modal, Portal, Switch, Text, useTheme } from 'react-native-paper';

import type { Device, WiFiLockCloseRequest, WiFiLockStatus } from '../core/types';
import { useDevices, useRemoteMode } from '../features/queries';
import {
  useBoxKey,
  useLockAction,
  useLockPanel,
  useLockPrefs,
  useReservePreview,
  useWiFiLock,
  type VisitorAnswer,
} from '../features/wifiLock';
import { t } from '../i18n';
import { lockView, reserveCheck, visitorCandidates, type LockView, type VisitorCandidate } from '../lib/trust';
import { useNow } from '../lib/useNow';
import { wifiQrValue } from '../lib/wifiQr';
import { useGate } from '../licensing/useGate';
import { ActionError } from './ActionError';
import { confirm } from './confirm';
import { Fold } from './Fold';
import { GoTo } from './GoTo';
import { KCard } from './KCard';
import { LockedButton } from './LockedButton';
import { Muted } from './Muted';
import { PasswordInput } from './PasswordInput';
import { QrCode } from './QrCode';
import { StateBadge } from './StateMarks';
import { radius, useSemantic } from './theme';

/** Minutes left in the add window, from its deadline, rounded up (R12). */
function minutesLeft(s: WiFiLockStatus, now: number): number {
  const until = Date.parse(s.window.until);
  if (Number.isNaN(until)) return s.window.minutes_left;
  return Math.max(1, Math.ceil((until - now) / 60_000));
}

/**
 * The box's Wi-Fi at the top of the Wi-Fi tab (#108; #134 moved it there
 * from the top of the device list, #111 R3): a full-width card that always
 * shows the box's state with an icon and a word, never the action, and
 * « Gérer », which opens the panel. Nothing changes without a second gesture. Hidden
 * on a box that cannot reserve its Wi-Fi (D11) and through the relay (#109).
 */
export function WiFiLockCard() {
  const lock = useWiFiLock();
  const now = useNow(15_000);
  const box = useBoxKey();
  const prefs = useLockPrefs();
  const panel = useLockPanel();
  const tones = useSemantic();
  useEffect(() => {
    void prefs.load();
  }, [prefs]);
  const s = lock.data;
  if (!s) return null;
  const v = lockView(s.state === 'window' ? { ...s, window: { ...s.window, minutes_left: minutesLeft(s, now) } } : s)!;
  const reviewed = !!prefs.byBox[box]?.reviewed;
  return (
    <View style={styles.cardWrap}>
      <KCard testID="lock-card">
        <Card.Content style={styles.content}>
          <View style={styles.head}>
            <StateBadge role={v.role} icon={v.icon} accessibilityLabel={v.title} testID="lock-badge" />
            <View style={styles.headText}>
              <Muted variant="labelMedium">{t('lock.title')}</Muted>
              <Text variant="titleMedium" style={v.role === 'connected' ? { color: tones.connected.color } : undefined} testID="lock-state">
                {v.title}
              </Text>
            </View>
          </View>
          <Text variant="bodyMedium" testID="lock-line">
            {v.line}
          </Text>
          {v.ended ? (
            <Text variant="bodyMedium" testID="lock-ended">
              {t('lock.ended')}
            </Text>
          ) : null}
          <View style={styles.row}>
            <Button mode="contained" icon="tune-variant" onPress={panel.show} testID="lock-manage">
              {t('lock.manage')}
            </Button>
            {prefs.loaded && !reviewed ? (
              <Button mode="text" icon="home-account" onPress={() => router.push('/house-devices')} testID="lock-review">
                {t('lock.review')}
              </Button>
            ) : null}
          </View>
          {prefs.loaded && !reviewed ? <Muted variant="bodySmall">{t('lock.reviewHint')}</Muted> : null}
        </Card.Content>
      </KCard>
      {panel.open ? <LockPanel status={s} view={v} onClose={panel.hide} /> : null}
    </View>
  );
}

/** The panel at the bottom of the screen: the box's state, and what can be done from it. */
function LockPanel({ status, view, onClose }: { status: WiFiLockStatus; view: LockView; onClose: () => void }) {
  const { colors } = useTheme();
  return (
    <Portal>
      <Modal
        visible
        onDismiss={onClose}
        style={styles.modal}
        contentContainerStyle={[styles.sheet, { backgroundColor: colors.elevation.level2, borderColor: colors.outlineVariant }]}
        testID="lock-panel"
      >
        <ScrollView contentContainerStyle={styles.body} testID="lock-panel-scroll">
          <Text variant="titleMedium" testID="lock-panel-state">
            {view.title}
          </Text>
          <Text variant="bodyMedium">{view.line}</Text>
          {view.state === 'closed' ? <Reserved onClose={onClose} /> : null}
          {view.state === 'window' ? <AddWindow status={status} /> : null}
          {view.state === 'open' || view.state === 'busy' ? <Open status={status} view={view} onClose={onClose} /> : null}
          <Muted variant="bodySmall" testID="lock-cable">
            {t('lock.cable')}
          </Muted>
          <Button mode="text" onPress={onClose} style={styles.end} testID="lock-panel-cancel">
            {t('common.cancel')}
          </Button>
        </ScrollView>
      </Modal>
    </Portal>
  );
}

/** Reserved: reopen (free, even with a lapsed pass), or let one device in for 5 minutes (Max). */
function Reserved({ onClose }: { onClose: () => void }) {
  const act = useLockAction();
  const { guard, can } = useGate();
  const add = () => guard('wifi_lock', () => act.mutate({ kind: 'window', action: 'open' }), t('lock.add'));
  return (
    <View style={styles.actions}>
      <Button
        mode="contained"
        icon="lock-open-variant-outline"
        onPress={() => act.mutate({ kind: 'reopen' }, { onSuccess: onClose })}
        loading={act.isPending && act.variables?.kind === 'reopen'}
        disabled={act.isPending}
        testID="lock-reopen"
      >
        {t('lock.reopen')}
      </Button>
      {can('wifi_lock') ? (
        <Button
          mode="outlined"
          icon="account-plus"
          onPress={add}
          loading={act.isPending && act.variables?.kind === 'window'}
          disabled={act.isPending}
          testID="lock-add"
        >
          {t('lock.add')}
        </Button>
      ) : (
        <LockedButton label={t('lock.add')} onPress={add} testID="lock-add" />
      )}
      <ActionError error={act.error} />
    </View>
  );
}

/** The household's Wi-Fi network, for the visitor's QR code. */
function homeNetwork(status: WiFiLockStatus, devices: Device[] | undefined): string {
  const mine = devices?.find((d) => d.Mark === 'Self')?.SSID;
  if (mine) return mine;
  const ap = status.aps.find((a) => status.household_aps.includes(a.id)) ?? status.aps[0];
  return ap?.name ?? '';
}

/** The add window (D4, R12): the countdown, « Encore 5 min », « Refermer maintenant », a QR code. */
function AddWindow({ status }: { status: WiFiLockStatus }) {
  const act = useLockAction();
  const { guard, can } = useGate();
  const { data: devices } = useDevices();
  const now = useNow(15_000);
  const [password, setPassword] = useState('');
  const name = homeNetwork(status, devices);
  const extend = () => guard('wifi_lock', () => act.mutate({ kind: 'window', action: 'extend' }), t('lock.extend'));
  return (
    <View style={styles.actions}>
      <Text variant="headlineSmall" testID="lock-countdown">
        {t('lock.state.window', { n: minutesLeft(status, now) })}
      </Text>
      {can('wifi_lock') ? (
        <Button
          mode="contained"
          icon="timer-plus-outline"
          onPress={extend}
          disabled={act.isPending || !status.window.can_extend}
          loading={act.isPending && act.variables?.kind === 'window' && act.variables.action === 'extend'}
          testID="lock-extend"
        >
          {t('lock.extend')}
        </Button>
      ) : (
        <LockedButton label={t('lock.extend')} onPress={extend} testID="lock-extend" />
      )}
      <Button
        mode="outlined"
        icon="home-lock"
        onPress={() => act.mutate({ kind: 'window', action: 'close' })}
        disabled={act.isPending}
        loading={act.isPending && act.variables?.kind === 'window' && act.variables.action === 'close'}
        testID="lock-close-now"
      >
        {t('lock.closeNow')}
      </Button>
      <ActionError error={act.error} />
      {name ? (
        <View style={styles.qr} testID="lock-qr-block">
          <Text variant="titleSmall">{t('lock.qrTitle')}</Text>
          <Muted variant="bodySmall">{t('lock.qrHint')}</Muted>
          {/* Typed here, drawn on screen only: never kept nor sent (#123). */}
          <PasswordInput label={t('lock.qrPassword')} value={password} onChangeText={setPassword} testID="lock-qr-password" />
          {password.length >= 8 ? (
            <QrCode value={wifiQrValue({ name, password })} size={200} label={t('lock.qrLabel', { name })} testID="lock-qr" />
          ) : null}
        </View>
      ) : null}
    </View>
  );
}

/** Open: the visitors' question, what reserving will do, and its one button. */
function Open({ status, view, onClose }: { status: WiFiLockStatus; view: LockView; onClose: () => void }) {
  const { data: devices } = useDevices();
  const mode = useRemoteMode();
  const box = useBoxKey();
  const prefs = useLockPrefs();
  const act = useLockAction();
  const { guard, can } = useGate();
  const answers = useMemo(() => prefs.byBox[box]?.visitors ?? {}, [prefs.byBox, box]);
  const candidates = useMemo(() => visitorCandidates(status.aps, devices ?? []), [status.aps, devices]);
  const [visitorsClosed, setVisitorsClosed] = useState(status.visitors_closed);
  // Asked once (D2); « Je ne sais pas » keeps it open and asks again (R9).
  const ask = candidates.filter((c) => !answers[c.name] || answers[c.name] === 'unsure');
  const unanswered = candidates.some((c) => !answers[c.name]);
  const visitorIds = candidates.filter((c) => answers[c.name] === 'yes' || answers[c.name] === 'unsure').flatMap((c) => c.ids);
  const req: WiFiLockCloseRequest = candidates.length
    ? { visitor_aps: visitorIds, visitors_closed: visitorIds.length > 0 && visitorsClosed }
    : {};
  const preview = useReservePreview(req, view.state === 'open');
  const check = preview.data ? reserveCheck(preview.data, { viaRelay: false, hasRelay: !!mode.household }) : null;
  const approved = (devices ?? []).filter((d) => d.Approved && !d.Mark).length;

  const reserve = () =>
    guard(
      'wifi_lock',
      async () => {
        // The first time (fewer than 2 authorised, D12): who is of the house.
        if (approved < 2 && !prefs.byBox[box]?.reviewed) {
          onClose();
          router.push({ pathname: '/house-devices', params: { then: 'reserve' } });
          return;
        }
        const n = preview.data?.devices.length ?? 0;
        const yes = await confirm({
          title: t('lock.confirmTitle'),
          message: t('lock.confirmBody', { n }),
          confirmLabel: t('lock.confirmYes'),
          icon: 'home-lock',
        });
        if (yes) act.mutate({ kind: 'reserve', req });
      },
      t('lock.reserve'),
    );

  const answer = (c: VisitorCandidate, a: VisitorAnswer) => void prefs.update(box, { visitors: { [c.name]: a } });
  const off = view.state === 'busy' || !check?.ok || unanswered || act.isPending;

  return (
    <View style={styles.actions}>
      {ask.map((c, i) => (
        <View key={c.name} style={styles.question} testID={`lock-visitors-${i}`}>
          <Text variant="titleSmall">{t('lock.visitorsQuestion')}</Text>
          <Text variant="bodyMedium">{t('lock.visitorsNet', { name: c.name, bands: c.bands, n: c.connected })}</Text>
          <Muted variant="bodySmall">{t('lock.visitorsHint')}</Muted>
          <View style={styles.row}>
            {(['yes', 'no', 'unsure'] as const).map((a) => (
              <Button
                key={a}
                compact
                mode={answers[c.name] === a ? 'contained' : 'outlined'}
                onPress={() => answer(c, a)}
                testID={`lock-visitors-${i}-${a}`}
              >
                {t(a === 'yes' ? 'lock.visitorsYes' : a === 'no' ? 'lock.visitorsNo' : 'lock.visitorsUnsure')}
              </Button>
            ))}
          </View>
        </View>
      ))}
      {visitorIds.length ? (
        <View style={styles.switchRow}>
          <Text variant="bodyMedium" style={styles.grow}>
            {t('lock.visitorsClose')}
          </Text>
          <Switch value={visitorsClosed} onValueChange={setVisitorsClosed} testID="lock-visitors-close" />
        </View>
      ) : null}
      {preview.data ? (
        <Text variant="bodyMedium" testID="lock-count">
          {t('lock.count', { n: preview.data.devices.length })}
        </Text>
      ) : null}
      {check?.warnings.map((w) => (
        <Text key={w} variant="bodyMedium" testID="lock-warning">
          {w}
        </Text>
      ))}
      {check?.privateAddress ? (
        <View testID="lock-private">
          <Text variant="bodyMedium">{t('lock.private')}</Text>
          <Fold title={t('lock.privateHow')} icon="help-circle-outline" style={styles.fold} testID="lock-private-how">
            <Text variant="bodySmall">{t('lock.privateHowBody')}</Text>
          </Fold>
        </View>
      ) : null}
      {can('wifi_lock') ? (
        <Button
          mode="contained"
          icon="home-lock"
          onPress={reserve}
          disabled={off}
          loading={act.isPending || preview.isFetching}
          testID="lock-reserve"
        >
          {t('lock.reserve')}
        </Button>
      ) : (
        // R1: the lock and the neutral sentence; it opens nothing.
        <LockedButton label={t('lock.reserve')} onPress={reserve} testID="lock-reserve" />
      )}
      {check && !check.ok && check.why ? (
        <Text variant="bodySmall" testID="lock-reserve-why">
          {check.why}
        </Text>
      ) : null}
      {unanswered ? (
        <Muted variant="bodySmall" testID="lock-visitors-first">
          {t('lock.visitorsFirst')}
        </Muted>
      ) : null}
      <ActionError error={act.error ?? preview.error} />
      <Muted variant="bodySmall">{t('lock.middle')}</Muted>
      <GoTo to="/auto-reject" icon="account-clock" label={t('lock.middleGo')} testID="lock-middle" />
    </View>
  );
}

const styles = StyleSheet.create({
  cardWrap: { marginHorizontal: 12, marginTop: 12 },
  content: { gap: 8, paddingTop: 12 },
  head: { flexDirection: 'row', alignItems: 'center', gap: 12 },
  headText: { flex: 1 },
  row: { flexDirection: 'row', gap: 8, flexWrap: 'wrap', alignItems: 'center' },
  modal: { justifyContent: 'flex-end', margin: 0 },
  sheet: { borderTopLeftRadius: radius.lg, borderTopRightRadius: radius.lg, borderWidth: 1, maxHeight: '90%' },
  body: { padding: 16, gap: 12 },
  actions: { gap: 10 },
  question: { gap: 4 },
  switchRow: { flexDirection: 'row', alignItems: 'center', gap: 8 },
  grow: { flex: 1 },
  qr: { gap: 8, alignItems: 'stretch' },
  fold: { paddingHorizontal: 0 },
  end: { alignSelf: 'flex-end' },
});
