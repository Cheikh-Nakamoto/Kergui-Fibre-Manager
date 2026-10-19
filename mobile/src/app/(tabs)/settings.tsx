import { useQueryClient } from '@tanstack/react-query';
import Constants from 'expo-constants';
import { router } from 'expo-router';
import { ScrollView } from 'react-native';
import { Button, List } from 'react-native-paper';

import { core } from '../../core/client';
import { t } from '../../i18n';
import { formatFingerprint } from '../../lib/format';
import { useSession } from '../../session/store';
import { confirm } from '../../ui/confirm';

export default function Settings() {
  const { session, signOut } = useSession();
  const qc = useQueryClient();

  const forget = async () => {
    if (!(await confirm(t('settings.signOutConfirm'), t('settings.signOut'), true))) return;
    await signOut();
    qc.clear();
    router.replace('/');
  };

  return (
    <ScrollView contentContainerStyle={{ padding: 8 }}>
      <List.Section title={t('settings.box')}>
        {session?.mode === 'router' ? (
          <>
            <List.Item title={session.routerUrl} left={(p) => <List.Icon {...p} icon="router-wireless" />} />
            {session.pin ? (
              <List.Item
                title={formatFingerprint(session.pin)}
                titleNumberOfLines={4}
                titleStyle={{ fontSize: 12 }}
                left={(p) => <List.Icon {...p} icon="certificate-outline" />}
              />
            ) : null}
          </>
        ) : (
          <List.Item title={t('settings.demo')} left={(p) => <List.Icon {...p} icon="flask-outline" />} />
        )}
      </List.Section>
      <List.Item
        title={t('settings.journal')}
        description={t('settings.journalHint')}
        left={(p) => <List.Icon {...p} icon="text-box-search-outline" />}
        right={(p) => <List.Icon {...p} icon="chevron-right" />}
        onPress={() => router.push('/journal')}
        testID="settings-journal"
      />
      <List.Item
        title={t('settings.version', { app: Constants.expoConfig?.version ?? '?', core: core.version() })}
        left={(p) => <List.Icon {...p} icon="information-outline" />}
      />
      <Button mode="outlined" onPress={forget} style={{ margin: 16 }} testID="settings-sign-out">
        {t('settings.signOut')}
      </Button>
    </ScrollView>
  );
}
