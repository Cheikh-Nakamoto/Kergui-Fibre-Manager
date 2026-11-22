import { useQuery, useQueryClient } from '@tanstack/react-query';
import * as Linking from 'expo-linking';
import { router, Stack } from 'expo-router';
import { useState } from 'react';
import { RefreshControl, ScrollView, StyleSheet, View } from 'react-native';
import { Button, HelperText, List, Switch, Text } from 'react-native-paper';

import {
  DEMO_PROVIDER,
  alertChannelsKey,
  loadAlertChannels,
  useDemoWhatsApp,
  whatsapp,
  type HubKeyState,
} from '../features/whatsapp';
import { errorMessage, t } from '../i18n';
import { useAccount } from '../licensing/account';
import type { WhatsAppProvider } from '../licensing/api';
import { config } from '../licensing/config';
import { useSession } from '../session/store';
import { confirm } from '../ui/confirm';

// Alertes WhatsApp (issue #49), from Réglages. The person writes first:
// « Recevoir les alertes sur WhatsApp » asks the hub for a wa.me link that
// pre-fills "KERGUI <code>" and opens WhatsApp. The provider's licence notice
// (Evolution Go) is shown here, as its licence asks. The owner switches
// « Répondre 1 pour bloquer », which delegates — or revokes — the hub's
// narrow key in the bundle the relays read. The demo plays it in memory.

function Attribution({ provider }: { provider: WhatsAppProvider }) {
  const text = provider.notice || (provider.name === 'console' ? t('whatsapp.console') : '');
  if (!text) return null;
  return (
    <List.Item
      title={t('whatsapp.provider')}
      description={text}
      descriptionNumberOfLines={4}
      left={(p) => <List.Icon {...p} icon="information-outline" />}
      testID="whatsapp-attribution"
    />
  );
}

const hubKeyHint: Record<HubKeyState['state'], Parameters<typeof t>[0]> = {
  on: 'whatsapp.hubKeyOn',
  off: 'whatsapp.hubKeyOff',
  unavailable: 'whatsapp.hubKeyUnavailable',
  no_relay: 'whatsapp.hubKeyNoRelay',
  not_owner: 'whatsapp.hubKeyNotOwner',
};

function HubKeySwitch({ state, busy, onChange }: { state: HubKeyState['state']; busy: boolean; onChange: (on: boolean) => void }) {
  const switchable = state === 'on' || state === 'off';
  return (
    <List.Item
      title={t('whatsapp.hubKey')}
      description={t(hubKeyHint[state])}
      descriptionNumberOfLines={5}
      left={(p) => <List.Icon {...p} icon="numeric-1-box-outline" />}
      right={() => (
        <Switch
          value={state === 'on'}
          disabled={!switchable || busy}
          onValueChange={onChange}
          accessibilityLabel={t('whatsapp.hubKey')}
          testID="whatsapp-hub-key"
        />
      )}
    />
  );
}

/** Asks before giving the hub its one power; taking it back needs no question. */
async function confirmHubKey(on: boolean): Promise<boolean> {
  return !on || confirm(t('whatsapp.hubKeyConfirm'));
}

function Status({ linked, count }: { linked: boolean; count: number }) {
  return (
    <List.Item
      title={t(linked ? 'whatsapp.linked' : 'whatsapp.notLinked')}
      description={t('whatsapp.linkedCount', { count })}
      left={(p) => <List.Icon {...p} icon={linked ? 'check-circle-outline' : 'whatsapp'} />}
      testID="whatsapp-status"
    />
  );
}

function SignedIn() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: alertChannelsKey, queryFn: loadAlertChannels });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [note, setNote] = useState<string | null>(null);

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
      await qc.invalidateQueries({ queryKey: alertChannelsKey });
    }
  };

  const optIn = () =>
    run(async () => {
      const inv = await whatsapp.invite();
      const text = decodeURIComponent(new URL(inv.waLink).searchParams.get('text') ?? '');
      try {
        await Linking.openURL(inv.waLink);
        setNote(t('whatsapp.optInSent'));
      } catch {
        setNote(t('whatsapp.cannotOpen', { text, number: inv.number }));
      }
    });

  const setHubKey = async (on: boolean) => {
    if (await confirmHubKey(on)) await run(() => whatsapp.setHubKey(on));
  };

  const v = q.data;
  const shown = error ?? (v ? null : q.error);
  return (
    <ScrollView
      contentContainerStyle={styles.body}
      refreshControl={<RefreshControl refreshing={q.isFetching && !!v} onRefresh={() => void q.refetch()} />}
    >
      <Text variant="bodyMedium">{t('whatsapp.intro')}</Text>
      {v ? (
        <>
          <Status linked={v.linked} count={v.linkedCount} />
          {v.canLink ? (
            <Button mode="contained" icon="whatsapp" onPress={() => void optIn()} loading={busy} disabled={busy} testID="whatsapp-optin">
              {t(v.linked ? 'whatsapp.optInAgain' : 'whatsapp.optIn')}
            </Button>
          ) : (
            <Text variant="bodySmall" style={styles.hint}>
              {t('whatsapp.viewerOnly')}
            </Text>
          )}
          {note ? (
            <Text variant="bodySmall" style={styles.hint} testID="whatsapp-note">
              {note}
            </Text>
          ) : null}
          <Text variant="bodySmall" style={styles.hint}>
            {t('whatsapp.stop')}
          </Text>
          <HubKeySwitch state={v.hubKey.state} busy={busy} onChange={(on) => void setHubKey(on)} />
          <Attribution provider={v.provider} />
        </>
      ) : null}
      {shown ? (
        <HelperText type="error" testID="whatsapp-error">
          {errorMessage(shown)}
        </HelperText>
      ) : null}
    </ScrollView>
  );
}

function Demo() {
  const demo = useDemoWhatsApp();
  return (
    <ScrollView contentContainerStyle={styles.body}>
      <Text variant="bodyMedium">{t('whatsapp.intro')}</Text>
      <Status linked={demo.linked} count={demo.linked ? 1 : 0} />
      <Button mode="contained" icon="whatsapp" onPress={demo.link} testID="whatsapp-optin">
        {t(demo.linked ? 'whatsapp.optInAgain' : 'whatsapp.optIn')}
      </Button>
      {demo.message ? (
        <Text variant="bodySmall" style={styles.hint} testID="whatsapp-demo-message">
          {t('whatsapp.demoMessage', { text: demo.message })}
        </Text>
      ) : null}
      <Text variant="bodySmall" style={styles.hint}>
        {t('whatsapp.stop')}
      </Text>
      <HubKeySwitch
        state={demo.hubKey ? 'on' : 'off'}
        busy={false}
        onChange={(on) => void confirmHubKey(on).then((ok) => ok && demo.setHubKey(on))}
      />
      <Attribution provider={DEMO_PROVIDER} />
    </ScrollView>
  );
}

export default function WhatsAppScreen() {
  const demo = useSession((s) => s.session?.mode === 'demo');
  const account = useAccount((s) => s.account);

  const body = () => {
    if (demo) return <Demo />;
    if (!config.hubUrl) {
      return (
        <View style={styles.body}>
          <Text variant="bodyMedium">{t('pro.unconfigured')}</Text>
        </View>
      );
    }
    if (!account) {
      return (
        <View style={[styles.body, styles.gap]}>
          <Text variant="bodyMedium">{t('whatsapp.signInFirst')}</Text>
          <Button mode="contained" onPress={() => router.push('/sign-in')} testID="whatsapp-sign-in">
            {t('pro.signIn')}
          </Button>
        </View>
      );
    }
    return <SignedIn />;
  };

  return (
    <>
      <Stack.Screen options={{ headerShown: true, title: t('whatsapp.title') }} />
      {body()}
    </>
  );
}

const styles = StyleSheet.create({
  body: { padding: 16, gap: 8 },
  gap: { gap: 12 },
  hint: { opacity: 0.8 },
});
