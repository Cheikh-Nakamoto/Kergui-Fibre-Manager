import { useState } from 'react';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Button, Modal, Portal, Text, useTheme } from 'react-native-paper';

import type { Device } from '../core/types';
import { useTrustContext, useTrustState } from '../features/wifiLock';
import { t } from '../i18n';
import { useNow } from '../lib/useNow';
import { trustPill } from '../lib/trust';
import { StateChip } from './StateMarks';
import { radius } from './theme';
import { TrustChoices } from './TrustChoices';

/**
 * A device's state under its name (#108): « Autorisé », « À vérifier » or
 * « Bloqué », each with colour, icon and word (R13), and under « Bloqué »
 * what it means for it (« Pas d'internet », « Ne peut plus se connecter »,
 * R7). « En cours… » while the box is written and read again. Touching it
 * opens the device's three choices at the bottom of the screen.
 */
export function TrustPill({ d, name }: { d: Device; name: string }) {
  const ctx = useTrustContext();
  const now = useNow();
  const pending = useTrustState((s) => !!s.pending[d.MAC.toLowerCase()]);
  const [open, setOpen] = useState(false);
  const from = ctx.from(d);
  const pill = trustPill(d, { capable: ctx.capable, reserved: ctx.reserved, cut: from.cut, now });
  if (!pill && !pending) return null;
  const label = pending ? t('trust.busy') : (pill?.label ?? '');
  return (
    <>
      <View style={styles.pill}>
        <StateChip
          role={pending ? 'neutral' : (pill?.role ?? 'neutral')}
          icon={pending ? 'progress-clock' : (pill?.icon ?? 'help-circle-outline')}
          onPress={() => setOpen(true)}
          accessibilityLabel={t('trust.change', { state: label })}
          testID={`device-trust-${d.MAC}`}
        >
          {label}
        </StateChip>
        {!pending && pill?.sub ? (
          <Text variant="bodySmall" testID={from.cut ? `device-cut-badge-${d.MAC}` : `device-blocked-sub-${d.MAC}`}>
            {pill.sub}
          </Text>
        ) : null}
      </View>
      {open ? <TrustPanel d={d} name={name} onClose={() => setOpen(false)} /> : null}
    </>
  );
}

/** The device's choices, at the bottom of the screen; « Annuler » closes it. */
function TrustPanel({ d, name, onClose }: { d: Device; name: string; onClose: () => void }) {
  const { colors } = useTheme();
  return (
    <Portal>
      <Modal
        visible
        onDismiss={onClose}
        style={styles.modal}
        contentContainerStyle={[styles.sheet, { backgroundColor: colors.elevation.level2, borderColor: colors.outlineVariant }]}
        testID="trust-panel"
      >
        <ScrollView contentContainerStyle={styles.body}>
          <Text variant="titleMedium" testID="trust-panel-title">
            {name}
          </Text>
          <TrustChoices device={d} name={name} testID="trust-panel" onChosen={onClose} />
          <Button mode="text" onPress={onClose} style={styles.cancel} testID="trust-panel-cancel">
            {t('common.cancel')}
          </Button>
        </ScrollView>
      </Modal>
    </Portal>
  );
}

const styles = StyleSheet.create({
  pill: { flexDirection: 'row', alignItems: 'center', gap: 6, flexWrap: 'wrap' },
  modal: { justifyContent: 'flex-end', margin: 0 },
  sheet: { borderTopLeftRadius: radius.lg, borderTopRightRadius: radius.lg, borderWidth: 1, maxHeight: '85%' },
  body: { padding: 16, gap: 12 },
  cancel: { alignSelf: 'flex-end' },
});
