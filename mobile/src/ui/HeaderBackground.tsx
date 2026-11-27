import { View } from 'react-native';

import { useChrome } from './theme';

/**
 * The header's background (#128): the site's header, the page's background over a thin
 * line, in place of the solid green block. React Navigation draws no line of
 * its own on Android (it casts a shadow), hence this view.
 */
export function HeaderBackground() {
  const bars = useChrome();
  return <View style={{ flex: 1, backgroundColor: bars.background, borderBottomWidth: 1, borderBottomColor: bars.border }} />;
}
