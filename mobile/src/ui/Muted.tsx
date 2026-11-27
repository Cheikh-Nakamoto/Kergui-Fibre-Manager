import type { ComponentProps } from 'react';
import { Text, useTheme } from 'react-native-paper';

/**
 * Secondary text (#125, audit L1): empty lists, hints, counts. Written in the
 * theme's onSurfaceVariant, held to AA on every surface by
 * themeContrast.test.ts, never dimmed with opacity (60 % of the ink over the
 * paper reads 4.2:1, under AA). A colour given in `style` still wins.
 */
export function Muted({ style, ...rest }: ComponentProps<typeof Text>) {
  const theme = useTheme();
  return <Text {...rest} style={[{ color: theme.colors.onSurfaceVariant }, style]} />;
}
