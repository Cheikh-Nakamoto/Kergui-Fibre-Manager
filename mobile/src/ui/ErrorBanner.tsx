import { Banner, useTheme } from 'react-native-paper';

import { errorMessage, t } from '../i18n';
import { isLocked } from './ActionError';
import { StateBadge } from './StateMarks';

export function ErrorBanner({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const { colors } = useTheme();
  return (
    <Banner
      visible={!!error}
      // Flat, over the site's thin line (#128): no shadow.
      elevation={0}
      style={{ borderBottomWidth: 1, borderBottomColor: colors.outlineVariant }}
      // Amber, « look at this »: red stays for what blocks (#75).
      icon={({ size }) => <StateBadge role="attention" icon={isLocked(error) ? 'lock' : 'alert-circle-outline'} size={size} />}
      actions={onRetry ? [{ label: t('common.retry'), onPress: onRetry }] : []}
    >
      {error ? errorMessage(error) : ''}
    </Banner>
  );
}
