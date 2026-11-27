import { useEffect, useState } from 'react';
import { StyleSheet } from 'react-native';
import { Button, Dialog, Portal, Text, useTheme } from 'react-native-paper';

import { t } from '../i18n';
import { answerConfirm, registerConfirmHost, useConfirm } from './confirm';
import { radius, useSemantic } from './theme';

/**
 * The confirmation window (#119, audit H7), mounted once in the root layout:
 * `await confirm(...)` anywhere shows it. A Paper Dialog, because Android
 * ignores the system alert's « destructive » style: the title names the
 * action, one line says what follows, the action is a filled button (red,
 * the « blocked » role, when it cuts or erases) and « Annuler » a text
 * button. Back, or a tap outside, is « Annuler ». The buttons keep stable
 * ids (confirm-ok, confirm-cancel) for the Maestro flows.
 */
export function ConfirmHost() {
  const shown = useConfirm((s) => s.shown);
  const { colors } = useTheme();
  const tones = useSemantic();
  useEffect(() => registerConfirmHost(), []);

  // The last question stays drawn while the dialog fades out.
  const [last, setLast] = useState(shown);
  if (shown && shown !== last) setLast(shown);
  const req = shown ?? last;
  const red = req?.destructive ? tones.blocked : null;

  return (
    <Portal>
      <Dialog
        visible={!!shown}
        onDismiss={() => answerConfirm(false)}
        style={[styles.dialog, { backgroundColor: colors.elevation.level1, borderColor: colors.outlineVariant }]}
        testID="confirm-dialog"
      >
        {req?.title ? (
          <Dialog.Title testID="confirm-title">
            {req.title}
          </Dialog.Title>
        ) : null}
        <Dialog.Content>
          <Text variant="bodyMedium" testID="confirm-message">
            {req?.message ?? ''}
          </Text>
        </Dialog.Content>
        <Dialog.Actions style={styles.actions}>
          <Button mode="text" onPress={() => answerConfirm(false)} testID="confirm-cancel">
            {t('common.cancel')}
          </Button>
          <Button
            mode="contained"
            icon={req?.icon}
            buttonColor={red?.color}
            textColor={red?.onColor}
            onPress={() => answerConfirm(true)}
            testID="confirm-ok"
          >
            {req?.confirmLabel ?? ''}
          </Button>
        </Dialog.Actions>
      </Dialog>
    </Portal>
  );
}

const styles = StyleSheet.create({
  // The site's card (#128): a thin line around it, no shadow.
  dialog: { borderWidth: 1, borderRadius: radius.lg },
  actions: { flexWrap: 'wrap', gap: 8 },
});
