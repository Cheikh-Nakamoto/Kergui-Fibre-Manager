import { Fragment, type ReactNode } from 'react';
import { Text } from 'react-native';

import { fontStyles } from './theme';

// The technical in the site's monospace (#128): MAC, IP, codes. A plain
// React Native Text nested in a Paper Text: it keeps the parent's size and
// colour and only changes the family.

/** A MAC, an IP or a code, inside a sentence. */
export function Mono({ children }: { children: ReactNode }) {
  return <Text style={fontStyles.mono}>{children}</Text>;
}

/**
 * The sentence with each of the given values (a MAC, an IP) in monospace:
 * the words stay in Inter, the sentence stays one text (one copy, one
 * accessibility label).
 */
export function withMono(text: string, values: (string | null | undefined)[]): ReactNode[] {
  const marks = values.filter((v): v is string => !!v && v.length >= 3);
  if (!marks.length) return [text];
  const escaped = marks.map((v) => v.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'));
  return text.split(new RegExp(`(${escaped.join('|')})`)).map((part, i) =>
    marks.includes(part) ? <Mono key={i}>{part}</Mono> : <Fragment key={i}>{part}</Fragment>,
  );
}
