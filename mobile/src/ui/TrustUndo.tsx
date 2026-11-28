import { Snackbar } from 'react-native-paper';

import { useSetTrust, useTrustState } from '../features/wifiLock';
import { t } from '../i18n';
import { doneKey } from '../lib/trust';
import { buttonSlop } from './targets';

/**
 * « Bloqué · Annuler » (#108), as the notifications' undo (#125): after a
 * block, « À vérifier » or « Retirer de la maison ». It stays until the next
 * action or its cross (#111 R6: no 5 seconds, WCAG 2.2.1). « Annuler » sets
 * the device back to where it was, and the core converges the box to it.
 */
export function TrustUndo() {
  const last = useTrustState((s) => s.last);
  const dismiss = useTrustState((s) => s.dismiss);
  const set = useSetTrust();
  const undo = () => {
    if (!last) return;
    dismiss();
    set.mutate({
      mac: last.mac,
      name: last.name,
      to: last.from.trust,
      cutWiFi: last.from.cut,
      from: { trust: last.to, cut: last.to === 'blocked' && last.cutWiFi, legacyCut: false },
      reserved: last.reserved,
      undo: true,
    });
  };
  return (
    <Snackbar
      key={last?.n ?? 0}
      visible={!!last}
      onDismiss={dismiss}
      duration={Number.POSITIVE_INFINITY}
      onIconPress={dismiss}
      iconAccessibilityLabel={t('trust.undoClose')}
      action={{ label: t('trust.undo'), onPress: undo, hitSlop: buttonSlop, testID: 'trust-undo' }}
      testID="trust-undo-bar"
    >
      {last ? t(doneKey(last.to, last.reserved), { name: last.name }) : ''}
    </Snackbar>
  );
}
