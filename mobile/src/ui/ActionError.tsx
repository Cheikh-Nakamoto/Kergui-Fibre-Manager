import { StyleSheet, View } from 'react-native';
import { HelperText, Icon, Text } from 'react-native-paper';

import { ApiError } from '../core/client';
import { errorMessage, t } from '../i18n';

/** Whether the core refused the action as not covered by the account. */
export function isLocked(error: unknown): boolean {
  return error instanceof ApiError && error.notEntitled;
}

/**
 * The outcome of a failed write under its button. A paid action the core
 * refused (403 not_entitled) is not an error: it shows a lock and one neutral
 * sentence, and offers nothing else to tap (RFC-006, no payment surface).
 */
export function ActionError({ error }: { error: unknown }) {
  if (!error) return null;
  if (isLocked(error)) return <Locked />;
  return <HelperText type="error">{errorMessage(error)}</HelperText>;
}

/** The lock and the one neutral sentence of a feature the account lacks. */
export function Locked({ testID = 'locked' }: { testID?: string }) {
  return (
    <View style={styles.locked} testID={testID} accessibilityRole="text">
      <Icon source="lock" size={16} />
      <Text variant="bodySmall" style={styles.text}>
        {t('pro.locked')}
      </Text>
    </View>
  );
}

const styles = StyleSheet.create({
  locked: { flexDirection: 'row', alignItems: 'center', gap: 6, paddingHorizontal: 12, paddingVertical: 4 },
  text: { flexShrink: 1 },
});
