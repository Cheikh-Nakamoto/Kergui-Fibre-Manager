import { useQueryClient } from '@tanstack/react-query';
import Constants from 'expo-constants';
import { router } from 'expo-router';
import { useEffect, useState } from 'react';
import { ScrollView } from 'react-native';
import { Button, HelperText, List, Switch } from 'react-native-paper';

import {
  alertsEnabled,
  askBatteryUnrestricted,
  batteryRestricted,
  disableAlerts,
  enableAlerts,
  forgetKnown,
} from '../../alerts/alerts';
import { pushToken } from '../../alerts/push';

import { core } from '../../core/client';
import type { Device } from '../../core/types';
import { keys } from '../../features/queries';
import { t } from '../../i18n';
import { formatFingerprint } from '../../lib/format';
import { useEntitlement } from '../../licensing/entitlement';
import { useGate } from '../../licensing/useGate';
import { useSession } from '../../session/store';
import { AutoRejectSetting } from '../../ui/AutoRejectSetting';
import { RelayPhoneSetting } from '../../ui/RelayPhoneSetting';
import { confirm } from '../../ui/confirm';

export default function Settings() {
  const { session, signOut } = useSession();
  const qc = useQueryClient();
  const { tier, demo } = useGate();

  const [alerts, setAlerts] = useState(false);
  const [denied, setDenied] = useState(false);
  const [restricted, setRestricted] = useState(batteryRestricted);
  useEffect(() => {
    void alertsEnabled().then(setAlerts);
  }, []);

  const toggleAlerts = async (on: boolean) => {
    setDenied(false);
    if (!on) {
      // The hub stops pushing to this phone too (#49).
      await pushToken.forget();
      await disableAlerts();
      setAlerts(false);
      return;
    }
    const ok = await enableAlerts(qc.getQueryData<Device[]>(keys.devices));
    setAlerts(ok);
    setDenied(!ok);
    setRestricted(batteryRestricted());
    // With the app closed, the hub's push brings the alert (#49).
    if (ok) void pushToken.sync();
  };

  const forget = async () => {
    if (!(await confirm(t('settings.signOutConfirm'), t('settings.signOut'), true))) return;
    await disableAlerts();
    await forgetKnown();
    await signOut();
    // The next box has its own serial number, and its own pass.
    await useEntitlement.getState().forgetBox();
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
        title={t('pro.title')}
        description={demo ? t('pro.demo') : t('pro.current', { tier: t(tier === 'max' ? 'pro.max' : tier === 'basic' ? 'pro.basic' : 'pro.freeTier') })}
        descriptionNumberOfLines={3}
        left={(p) => <List.Icon {...p} icon="account-circle-outline" />}
        right={(p) => <List.Icon {...p} icon="chevron-right" />}
        onPress={() => router.push('/account')}
        testID="settings-account"
      />
      <List.Item
        title={t('household.title')}
        description={t('household.settingsHint')}
        left={(p) => <List.Icon {...p} icon="home-account" />}
        right={(p) => <List.Icon {...p} icon="chevron-right" />}
        onPress={() => router.push('/household')}
        testID="settings-household"
      />
      <List.Item
        title={t('whatsapp.title')}
        description={t('whatsapp.settingsHint')}
        descriptionNumberOfLines={3}
        left={(p) => <List.Icon {...p} icon="whatsapp" />}
        right={(p) => <List.Icon {...p} icon="chevron-right" />}
        onPress={() => router.push('/whatsapp')}
        testID="settings-whatsapp"
      />
      <List.Item
        title={t('relay.title')}
        description={t('relay.settingsHint')}
        descriptionNumberOfLines={3}
        left={(p) => <List.Icon {...p} icon="access-point-network" />}
        right={(p) => <List.Icon {...p} icon="chevron-right" />}
        onPress={() => router.push('/relay')}
        testID="settings-relay"
      />
      <RelayPhoneSetting demo={demo} />
      <List.Item
        title={t('recovery.title')}
        description={t('recovery.settingsHint')}
        descriptionNumberOfLines={3}
        left={(p) => <List.Icon {...p} icon="cellphone-key" />}
        right={(p) => <List.Icon {...p} icon="chevron-right" />}
        onPress={() => router.push('/recovery')}
        testID="settings-recovery"
      />
      <List.Item
        title={t('settings.alerts')}
        description={t('settings.alertsHint')}
        descriptionNumberOfLines={4}
        left={(p) => <List.Icon {...p} icon="bell-ring-outline" />}
        right={() => (
          <Switch value={alerts} onValueChange={toggleAlerts} accessibilityLabel={t('settings.alerts')} testID="settings-alerts" />
        )}
      />
      {denied ? <HelperText type="error">{t('settings.alertsDenied')}</HelperText> : null}
      {alerts && restricted ? (
        <List.Item
          title={t('watch.battery')}
          description={t('watch.batteryHint')}
          descriptionNumberOfLines={4}
          left={(p) => <List.Icon {...p} icon="battery-alert-variant-outline" />}
          onPress={async () => {
            await askBatteryUnrestricted();
            setRestricted(batteryRestricted());
          }}
          testID="settings-battery"
        />
      ) : null}
      <AutoRejectSetting
        // The watch service runs the rule and posts the "Accepter"
        // notifications: on a real box, it needs the alerts on.
        onEnabled={async () => {
          if (session?.mode === 'router' && !alerts) await toggleAlerts(true);
        }}
      />
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
