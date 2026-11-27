import type { ReactNode } from 'react';
import { StyleSheet, View, type StyleProp, type ViewStyle } from 'react-native';
import { Chip, Icon, useTheme } from 'react-native-paper';

import { radius, useSemantic, type SemanticRole } from './theme';

// The marks of a state (#75): a round coloured badge with its icon, and a
// chip with an icon and a word. Saturated colour on these small areas only;
// the colour is never the only signal.

/** A round badge in the role's colour (neutral when no role: e.g. offline). */
export function StateBadge({ role, icon, size = 40, style }: { role?: SemanticRole; icon: string; size?: number; style?: StyleProp<ViewStyle> }) {
  const tones = useSemantic();
  const theme = useTheme();
  const tone = role ? tones[role] : { color: theme.colors.surfaceVariant, onColor: theme.colors.onSurfaceVariant };
  return (
    <View style={[styles.badge, { width: size, height: size, borderRadius: size / 2, backgroundColor: tone.color }, style]}>
      <Icon source={icon} size={Math.round(size * 0.55)} color={tone.onColor} />
    </View>
  );
}

/** A compact chip in the role's colour, its icon and word in the on-colour. */
export function StateChip({ role, icon, testID, children }: { role: SemanticRole; icon: string; testID?: string; children: ReactNode }) {
  const tone = useSemantic()[role];
  return (
    // The site's small radius (6), not MD3's 8.
    <Chip compact icon={icon} style={{ backgroundColor: tone.color, borderRadius: radius.sm }} selectedColor={tone.onColor} testID={testID}>
      {children}
    </Chip>
  );
}

const styles = StyleSheet.create({
  badge: { alignItems: 'center', justifyContent: 'center', alignSelf: 'center' },
});
