import { router } from 'expo-router';
import { Button } from 'react-native-paper';

import { t } from '../i18n';

/**
 * « Signaler un problème » under an error the app cannot name (#119, audit
 * H3, M3): a button, not a path written in text. It opens the request screen
 * (Réglages > Signaler un problème), where the journal can go with it.
 */
export function ReportProblem() {
  return (
    <Button mode="text" compact icon="message-alert-outline" onPress={() => router.push('/report')} testID="report-problem">
      {t('error.report')}
    </Button>
  );
}
