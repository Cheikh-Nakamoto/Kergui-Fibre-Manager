import { router, type Href } from 'expo-router';
import type { StyleProp, ViewStyle } from 'react-native';
import { Button } from 'react-native-paper';

/**
 * A button to the screen a sentence names (#124, audit M3): « Installer un
 * relais », « Ouvrir le foyer », rather than « Réglages > Plus de
 * fonctionnalités > … » to read, remember and walk. Never to a payment
 * (RFC-006: the Play app has none).
 */
export function GoTo({
  to,
  label,
  icon,
  testID,
  style,
}: {
  to: Href;
  label: string;
  icon?: string;
  testID: string;
  style?: StyleProp<ViewStyle>;
}) {
  return (
    <Button mode="contained-tonal" icon={icon} onPress={() => router.push(to)} style={[{ alignSelf: 'flex-start' }, style]} testID={testID}>
      {label}
    </Button>
  );
}
