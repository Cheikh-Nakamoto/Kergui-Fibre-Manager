import { router } from 'expo-router';
import { StyleSheet, View } from 'react-native';
import { Badge, IconButton } from 'react-native-paper';

import { unreadCount, useInbox } from '../alerts/inbox';
import { t } from '../i18n';

/** The bell at the top right of the tabs: the notifications, with the unread count. */
export function InboxBell({ color }: { color?: string }) {
  const unread = useInbox((s) => unreadCount(s.items));
  return (
    <View style={styles.wrap}>
      <IconButton
        icon={unread ? 'bell-badge-outline' : 'bell-outline'}
        iconColor={color}
        accessibilityLabel={unread ? t('inbox.openUnread', { count: unread }) : t('inbox.open')}
        onPress={() => router.push('/notifications')}
        testID="inbox-bell"
      />
      {unread ? (
        <Badge size={18} style={styles.badge} testID="inbox-badge">
          {unread > 9 ? '9+' : unread}
        </Badge>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  wrap: { marginRight: 4 },
  badge: { position: 'absolute', top: 4, right: 4 },
});
