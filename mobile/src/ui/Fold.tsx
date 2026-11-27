import { useState, type ReactNode } from 'react';
import type { StyleProp, ViewStyle } from 'react-native';
import { List } from 'react-native-paper';

/**
 * What most people never need, closed under one line (#121, #122): the
 * device's « Détails techniques », the connection's « Paramètres avancés »,
 * a certificate's « Détails ». Closed by default; `expanded` and `onToggle`
 * let the screen open it itself.
 */
export function Fold({
  title,
  icon,
  testID,
  style,
  expanded,
  onToggle,
  children,
}: {
  title: string;
  icon?: string;
  testID: string;
  style?: StyleProp<ViewStyle>;
  expanded?: boolean;
  onToggle?: (open: boolean) => void;
  children: ReactNode;
}) {
  const [own, setOwn] = useState(false);
  const open = expanded ?? own;
  const toggle = () => {
    setOwn(!open);
    onToggle?.(!open);
  };
  return (
    <List.Accordion
      title={title}
      expanded={open}
      onPress={toggle}
      left={icon ? (p) => <List.Icon {...p} icon={icon} /> : undefined}
      style={style}
      testID={testID}
    >
      {children}
    </List.Accordion>
  );
}
