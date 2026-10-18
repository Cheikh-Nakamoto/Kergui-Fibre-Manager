import { Banner } from 'react-native-paper';

import { errorMessage, t } from '../i18n';

export function ErrorBanner({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  return (
    <Banner
      visible={!!error}
      icon="alert-circle-outline"
      actions={onRetry ? [{ label: t('common.retry'), onPress: onRetry }] : []}
    >
      {error ? errorMessage(error) : ''}
    </Banner>
  );
}
