import type { ReactNode } from 'react';
import { StyleSheet, View, type StyleProp, type ViewStyle } from 'react-native';

import { useSemantic } from './theme';

// The site's « HUD » corners (.hud in landing/app/globals.css): a green
// corner top left, an amber one bottom right, just outside the block. Kept
// for one or two strong places (the welcome screen, the box's state), not
// every card (#128). Decorative: hidden from screen readers.

const ARM = 18;
const OUT = -8;

export function Hud({ children, style, testID }: { children: ReactNode; style?: StyleProp<ViewStyle>; testID?: string }) {
  const tones = useSemantic();
  return (
    <View style={[styles.root, style]} testID={testID}>
      {children}
      <View
        pointerEvents="none"
        accessibilityElementsHidden
        importantForAccessibility="no-hide-descendants"
        style={[styles.corner, styles.topLeft, { borderColor: tones.connected.color }]}
      />
      <View
        pointerEvents="none"
        accessibilityElementsHidden
        importantForAccessibility="no-hide-descendants"
        style={[styles.corner, styles.bottomRight, { borderColor: tones.attention.color }]}
      />
    </View>
  );
}

const styles = StyleSheet.create({
  root: { position: 'relative' },
  corner: { position: 'absolute', width: ARM, height: ARM },
  topLeft: { top: OUT, left: OUT, borderTopWidth: 2, borderLeftWidth: 2 },
  bottomRight: { bottom: OUT, right: OUT, borderBottomWidth: 2, borderRightWidth: 2 },
});
