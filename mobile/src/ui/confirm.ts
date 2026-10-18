import { Alert } from 'react-native';

import { t } from '../i18n';

/** Every write to the box goes through an explicit confirmation (the CLI's --yes). */
export function confirm(message: string, confirmLabel = t('common.confirm'), destructive = false): Promise<boolean> {
  return new Promise((resolve) => {
    Alert.alert('', message, [
      { text: t('common.cancel'), style: 'cancel', onPress: () => resolve(false) },
      { text: confirmLabel, style: destructive ? 'destructive' : 'default', onPress: () => resolve(true) },
    ]);
  });
}
