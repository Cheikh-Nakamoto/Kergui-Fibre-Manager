import { useQuery, useQueryClient } from '@tanstack/react-query';
import Constants from 'expo-constants';
import { router, useFocusEffect } from 'expo-router';
import { useCallback, useState } from 'react';
import { Linking, ScrollView } from 'react-native';
import { Button, HelperText, List, Switch } from 'react-native-paper';

import {
  alertsEnabled,
  askBatteryUnrestricted,
  batteryRestricted,
  disableAlerts,
  forgetKnown,
} from '../../alerts/alerts';
import { pushToken } from '../../alerts/push';
import { switchAlertsOn } from '../../alerts/switchOn';

import { api, core } from '../../core/client';
import type { Device } from '../../core/types';
import { lastList } from '../../features/lastList';
import { keys } from '../../features/queries';
import { t } from '../../i18n';
import { formatFingerprint } from '../../lib/format';
import { config } from '../../licensing/config';
import { useEntitlement } from '../../licensing/entitlement';
import { privacyUrl } from '../../licensing/privacy';
import { useGate } from '../../licensing/useGate';
import { useSession } from '../../session/store';
import { MoreFeaturesTile } from '../../ui/FeatureTile';
import { BoxPasswordSetting } from '../../ui/BoxPasswordSetting';
import { HubBackup } from '../../ui/HubBackup';
import { ImportBackup } from '../../ui/ImportBackup';
import { confirm } from '../../ui/confirm';
import { Fold } from '../../ui/Fold';
import { Mono } from '../../ui/Mono';
import { fontStyles } from '../../ui/theme';

/**
 * « ZTE F6600P » (#124, audit L3): what discovery recognised at connection,
 * or, for a session from before, what it recognises now. '' when unknown
 * (the box does not answer): Réglages then say « Box », nothing made up.
 */
function useBoxModel(): string {
  const session = useSession((s) => s.session);
  const kept = session?.mode === 'router' ? (session.model ?? '') : '';
  const url = session?.mode === 'router' ? session.routerUrl : '';
  const q = useQuery({
    queryKey: ['box', 'model', url],
    queryFn: async () => {
      const info = await api.discover();
      return [info.Vendor, info.Model].filter(Boolean).join(' ');
    },
    enabled: !!url && !kept,
    staleTime: Infinity,
    retry: false,
  });
  return kept || q.data || '';
}

export default function Settings() {
  const { session, signOut } = useSession();
  const qc = useQueryClient();
  const { tier, demo } = useGate();
  const model = useBoxModel();

  const [alerts, setAlerts] = useState(false);
  const [denied, setDenied] = useState(false);
  const [restricted, setRestricted] = useState(batteryRestricted);
  // Read again on return: the auto-reject screen may have turned them on.
  useFocusEffect(
    useCallback(() => {
      void alertsEnabled().then(setAlerts);
      setRestricted(batteryRestricted());
    }, []),
  );

  const toggleAlerts = async (on: boolean) => {
    setDenied(false);
    if (!on) {
      // The hub stops pushing to this phone too (#49).
      await pushToken.forget();
      await disableAlerts();
      setAlerts(false);
      return;
    }
    // With the app closed, the hub's push brings the alert (#49).
    const ok = await switchAlertsOn(qc.getQueryData<Device[]>(keys.devices));
    setAlerts(ok);
    setDenied(!ok);
    setRestricted(batteryRestricted());
  };

  const forget = async () => {
    const yes = await confirm({
      title: t('settings.signOutTitle'),
      message: t('settings.signOutConfirm'),
      confirmLabel: t('settings.signOut'),
      destructive: true,
      icon: 'link-off',
    });
    if (!yes) return;
    await disableAlerts();
    await forgetKnown();
    // The last list read belongs to this box (#124).
    await lastList.forget();
    await signOut();
    // The next box has its own serial number, and its own pass.
    await useEntitlement.getState().forgetBox();
    qc.clear();
    router.replace('/');
  };

  return (
    <ScrollView contentContainerStyle={{ padding: 8 }}>
      {/* Relays, household, WhatsApp, auto-reject and the level's features
          live on their own screen (#76): Réglages keeps the essentials. */}
      <MoreFeaturesTile onPress={() => router.push('/features')} />
      <List.Section titleStyle={fontStyles.display} title={t('settings.box')}>
        {session?.mode === 'router' ? (
          <>
            {/* What the box is, in words (#124); its address and certificate
                wait under « Détails techniques ». */}
            <List.Item
              title={model ? t('settings.boxModel', { model }) : t('settings.box')}
              left={(p) => <List.Icon {...p} icon="router-wireless" />}
              testID="settings-box-model"
            />
            {/* Owner only, behind the phone's unlock (#93). */}
            <BoxPasswordSetting />
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
      {/* Every backup under one title (#134): the household's copy on the
          Kergui server, then the .zip to export or import (at home only). */}
      <List.Section titleStyle={fontStyles.display} title={t('settings.backupTitle')} testID="settings-backup">
        <HubBackup />
        {session?.mode === 'router' ? <ImportBackup /> : null}
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
        title={t('report.title')}
        description={t('report.settingsHint')}
        descriptionNumberOfLines={3}
        left={(p) => <List.Icon {...p} icon="message-alert-outline" />}
        right={(p) => <List.Icon {...p} icon="chevron-right" />}
        onPress={() => router.push('/report')}
        testID="settings-report"
      />
      {/* Play's User Data policy (#37): the policy is one tap away. */}
      <List.Item
        title={t('settings.privacy')}
        description={t('settings.privacyHint')}
        descriptionNumberOfLines={3}
        left={(p) => <List.Icon {...p} icon="shield-account-outline" />}
        right={(p) => <List.Icon {...p} icon="open-in-new" />}
        onPress={() => void Linking.openURL(privacyUrl({ hubUrl: config.hubUrl, demo })).catch(() => undefined)}
        testID="settings-privacy"
      />
      {/* For a technician, closed (#124, audit L3): the box's address, its
          certificate's fingerprint, the app's and its engine's versions. */}
      <Fold title={t('settings.technical')} icon="information-outline" testID="settings-technical">
        {session?.mode === 'router' ? (
          <>
            <List.Item
              title={<Mono>{session.routerUrl}</Mono>}
              left={(p) => <List.Icon {...p} icon="ip-network-outline" />}
              testID="settings-box-url"
            />
            {session.pin ? (
              <List.Item
                title={<Mono>{formatFingerprint(session.pin)}</Mono>}
                titleNumberOfLines={4}
                titleStyle={{ fontSize: 12 }}
                left={(p) => <List.Icon {...p} icon="certificate-outline" />}
                testID="settings-box-fingerprint"
              />
            ) : null}
          </>
        ) : null}
        <List.Item
          title={t('settings.version', { app: Constants.expoConfig?.version ?? '?', core: core.version() })}
          left={(p) => <List.Icon {...p} icon="information-outline" />}
          testID="settings-version"
        />
      </Fold>
      <Button mode="outlined" onPress={forget} style={{ margin: 16 }} testID="settings-sign-out">
        {t('settings.signOut')}
      </Button>
    </ScrollView>
  );
}
