import { useState } from 'react';
import { StyleSheet, View } from 'react-native';
import { ActivityIndicator, Icon, List, Switch, Text, TouchableRipple, useTheme } from 'react-native-paper';

import type { Device } from '../core/types';
import { useRemoteMode } from '../features/queries';
import { useSetTrust, useTrustContext, useTrustState, type TrustFrom } from '../features/wifiLock';
import { t } from '../i18n';
import { trustChoices, type Choice, type Trust } from '../lib/trust';
import { useGate } from '../licensing/useGate';
import { ActionError } from './ActionError';
import { Fold } from './Fold';
import { useSemantic } from './theme';

/**
 * The three choices for a device (#108, RFC-007 D5): Autorisé, À vérifier
 * (« Retirer de la maison » while the Wi-Fi is reserved, #111 R4), Bloqué,
 * each with its sentence, the current one checked. Under « Bloqué », folded
 * in « Plus d'options » (R7), « Ne plus le laisser se connecter du tout
 * (même au Wi-Fi) ». One list on the device's sheet and in its pill's panel:
 * the household learns it once. No confirmation: an « Annuler » follows
 * (R6). A choice the account does not include shows its lock (#120) and
 * opens nothing (R1).
 */
export function TrustChoices({
  device,
  name,
  testID,
  onChosen,
}: {
  device: Pick<Device, 'MAC' | 'Blocked' | 'Approved' | 'Mark'>;
  name: string;
  testID: string;
  /** After a choice was sent (the pill's panel closes). */
  onChosen?: () => void;
}) {
  const ctx = useTrustContext();
  const set = useSetTrust();
  const remote = !!useRemoteMode().data;
  const { guard, can } = useGate();
  const { colors } = useTheme();
  const tones = useSemantic();
  const pending = useTrustState((s) => !!s.pending[device.MAC.toLowerCase()]);
  const from: TrustFrom = ctx.from(device);
  const current: Trust = from.trust;
  const choices = trustChoices({ capable: ctx.capable, reserved: ctx.reserved, current: from.trust, mark: device.Mark });
  // The switch's value for a device not blocked yet: sent with « Bloqué ».
  const [cutDraft, setCutDraft] = useState(false);
  const cutOn = from.trust === 'blocked' ? from.cut : cutDraft;
  const [more, setMore] = useState(from.cut);
  const busy = pending || set.isPending;

  const send = (to: Trust, cutWiFi: boolean) => {
    set.mutate({ mac: device.MAC, name, to, cutWiFi, from, reserved: ctx.reserved });
    onChosen?.();
  };

  const choose = (c: Choice) => {
    if (c.disabled || busy) return;
    if (c.trust === from.trust && c.trust !== 'blocked') return;
    if (c.trust === 'blocked' && from.trust === 'blocked') return;
    const go = () => send(c.trust, c.trust === 'blocked' ? cutOn : false);
    if (c.feature) guard(c.feature, go, c.label);
    else go();
  };

  const toggleCut = (on: boolean) => {
    if (busy) return;
    if (from.trust !== 'blocked') {
      if (on) guard('cut_wifi_access', () => setCutDraft(true), t('trust.cutWiFi'));
      else setCutDraft(false);
      return;
    }
    // Refusing at the Wi-Fi is Basic; letting it back is free.
    if (on) guard('cut_wifi_access', () => send('blocked', true), t('trust.cutWiFi'));
    else send('blocked', false);
  };

  return (
    <View testID={testID} accessibilityRole="radiogroup" style={styles.root}>
      {choices.map((c) => {
        const checked = c.trust === current;
        const locked = !!c.feature && !can(c.feature);
        const off = !!c.disabled || busy;
        return (
          <View key={c.trust}>
            <TouchableRipple
              onPress={() => choose(c)}
              disabled={off}
              accessibilityRole="radio"
              accessibilityState={{ checked, disabled: off }}
              accessibilityHint={locked ? t('pro.lockedShort') : (c.disabled ?? undefined)}
              testID={`${testID}-${c.trust}`}
              style={[styles.choice, { borderColor: checked ? tones[roleOf(c.trust)].color : colors.outlineVariant }]}
            >
              <View style={styles.row}>
                <Icon source={checked ? 'radiobox-marked' : 'radiobox-blank'} size={24} color={off ? colors.onSurfaceDisabled : colors.primary} />
                <View style={styles.words}>
                  <Text variant="titleSmall" style={off ? { color: colors.onSurfaceDisabled } : undefined}>
                    {c.label}
                  </Text>
                  <Text variant="bodySmall" style={{ color: colors.onSurfaceVariant }}>
                    {c.disabled ?? c.line}
                  </Text>
                  {locked && !c.disabled ? (
                    <Text variant="bodySmall" style={{ color: colors.onSurfaceVariant }} testID={`${testID}-${c.trust}-locked`}>
                      {t('pro.lockedShort')}
                    </Text>
                  ) : null}
                </View>
                {locked && !c.disabled ? <Icon source="lock" size={20} color={colors.onSurfaceVariant} /> : null}
              </View>
            </TouchableRipple>
            {c.trust === 'blocked' && ctx.wifiControl && !c.disabled ? (
              <Fold title={t('trust.more')} icon="tune-variant" expanded={more} onToggle={setMore} style={styles.fold} testID={`${testID}-more`}>
                <List.Item
                  title={t('trust.cutWiFi')}
                  titleNumberOfLines={3}
                  description={!can('cut_wifi_access') && !cutOn ? t('pro.lockedShort') : undefined}
                  left={(p) => <List.Icon {...p} icon={can('cut_wifi_access') || cutOn ? 'wifi-off' : 'lock'} />}
                  right={() => (
                    <Switch value={cutOn} onValueChange={toggleCut} disabled={busy} testID={`${testID}-cut`} />
                  )}
                  onPress={() => toggleCut(!cutOn)}
                  testID={`${testID}-cut-row`}
                />
              </Fold>
            ) : null}
          </View>
        );
      })}
      {busy ? (
        <View style={styles.busy} testID={`${testID}-busy`}>
          <ActivityIndicator size="small" />
          <Text variant="bodySmall">{t(set.isPending && remote ? 'remote.sending' : 'trust.busy')}</Text>
        </View>
      ) : null}
      {set.isSuccess && set.data.remote && (set.variables.to === 'blocked' || set.variables.from.trust === 'blocked') ? (
        // Shown only once the core checked the relay's signature over the
        // answer (#46): the hub alone cannot make this appear.
        <Text variant="bodyMedium" testID="device-remote-done">
          {t(set.variables.to === 'blocked' ? 'remote.doneBlock' : 'remote.doneUnblock')}
        </Text>
      ) : null}
      <ActionError error={set.error} />
    </View>
  );
}

function roleOf(trust: Trust) {
  return trust === 'blocked' ? 'blocked' : trust === 'authorised' ? 'connected' : 'attention';
}

const styles = StyleSheet.create({
  root: { gap: 8 },
  choice: { borderWidth: 1, borderRadius: 8, paddingVertical: 10, paddingHorizontal: 12, minHeight: 48 },
  row: { flexDirection: 'row', alignItems: 'center', gap: 12 },
  words: { flex: 1, gap: 2 },
  fold: { paddingHorizontal: 0 },
  busy: { flexDirection: 'row', alignItems: 'center', gap: 8 },
});
