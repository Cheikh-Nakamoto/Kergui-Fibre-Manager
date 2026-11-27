import type { ReactNode } from 'react';
import type { StyleProp, ViewStyle } from 'react-native';
import { Card, useTheme } from 'react-native-paper';

import { radius } from './theme';

// The site's card (.k-card in landing/app/globals.css, #128): the card
// colour, a thin green line around it, a 10 px radius, no shadow. A Paper
// Card underneath, so Card.Title / Card.Content / Card.Actions work as usual.

type Props = { children: ReactNode; style?: StyleProp<ViewStyle>; onPress?: () => void; testID?: string };

export function KCard({ children, style, onPress, testID }: Props) {
  const { colors } = useTheme();
  return (
    <Card
      mode="contained"
      onPress={onPress}
      testID={testID}
      style={[{ backgroundColor: colors.elevation.level1, borderWidth: 1, borderColor: colors.outlineVariant, borderRadius: radius.md }, style]}
    >
      {children}
    </Card>
  );
}
