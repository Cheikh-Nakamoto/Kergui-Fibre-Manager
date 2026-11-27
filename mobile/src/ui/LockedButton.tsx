import { StyleSheet, View, type StyleProp, type ViewStyle } from 'react-native';
import { Button, Text, useTheme } from 'react-native-paper';

import { t } from '../i18n';

/**
 * A paid action the account does not include, shown as such before the tap
 * (#120, audit H4), as the Conso arrow does: an outlined button with a lock
 * and the action's own label, and « Pas disponible pour votre compte » under
 * it, which the screen reader hears as the button's hint. A tap still
 * explains, through useGate's titled alert. Neutral: no price, no link, no
 * level to buy (RFC-006, the Play app has no payment surface). The line
 * keeps the button's id with « -locked » for the tests and Maestro flows.
 */
export function LockedButton({
  label,
  onPress,
  testID,
  style,
}: {
  label: string;
  onPress: () => void;
  testID: string;
  style?: StyleProp<ViewStyle>;
}) {
  const { colors } = useTheme();
  const why = t('pro.lockedShort');
  return (
    <View style={[styles.wrap, style]}>
      <Button
        mode="outlined"
        icon="lock"
        textColor={colors.onSurfaceVariant}
        onPress={onPress}
        accessibilityHint={why}
        testID={testID}
      >
        {label}
      </Button>
      {/* Read once, with the button: the hint already says it. */}
      <Text
        variant="bodySmall"
        style={[styles.line, { color: colors.onSurfaceVariant }]}
        importantForAccessibility="no"
        accessibilityElementsHidden
        testID={`${testID}-locked`}
      >
        {why}
      </Text>
    </View>
  );
}

const styles = StyleSheet.create({
  wrap: { gap: 4 },
  line: { paddingHorizontal: 4 },
});
