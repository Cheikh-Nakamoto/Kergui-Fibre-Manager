import { router } from 'expo-router';
import { StyleSheet, View } from 'react-native';
import { Button, HelperText, Icon, Text } from 'react-native-paper';

import { ApiError } from '../core/client';
import { errorMessage, isUnknownError, t } from '../i18n';
import { ReportProblem } from './ReportProblem';

/** Whether the core refused the action as not covered by the account. */
export function isLocked(error: unknown): boolean {
  return error instanceof ApiError && error.notEntitled;
}

/**
 * The outcome of a failed write under its button. A paid action the core
 * refused (403 not_entitled) is not an error: it shows a lock and one neutral
 * sentence, and offers nothing else to tap (RFC-006, no payment surface).
 * An error the app has no words for says « Ça n'a pas marché » with a
 * « Signaler un problème » button (#119).
 */
export function ActionError({ error }: { error: unknown }) {
  if (!error) return null;
  if (isLocked(error)) return <Locked />;
  if (error instanceof ApiError && error.accessListFull) return <ListFull error={error} />;
  if (isUnknownError(error)) {
    return (
      <View testID="action-error-unknown">
        <HelperText type="error">{errorMessage(error)}</HelperText>
        <ReportProblem />
      </View>
    );
  }
  return <HelperText type="error">{errorMessage(error)}</HelperText>;
}

/**
 * The box's list is full (RFC-007 D6): how many devices it takes, and the
 * way to the blocked devices that are away, which can be freed.
 */
function ListFull({ error }: { error: ApiError }) {
  return (
    <View testID="list-full">
      <HelperText type="error">{errorMessage(error)}</HelperText>
      <Button
        mode="text"
        compact
        icon="account-off"
        onPress={() => router.navigate({ pathname: '/devices', params: { show: 'absent' } })}
        testID="list-full-link"
      >
        {t('error.listFullLink')}
      </Button>
    </View>
  );
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
