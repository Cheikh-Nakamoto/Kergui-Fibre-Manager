import { useQuery, useQueryClient } from '@tanstack/react-query';
import { router, Stack, useFocusEffect, useLocalSearchParams } from 'expo-router';
import { useCallback, useState, type ComponentType } from 'react';
import { RefreshControl, ScrollView, StyleSheet, View } from 'react-native';
import { Button, Card, HelperText, List, Text } from 'react-native-paper';

import { hubErrorMessage, t } from '../i18n';
import { formatDay } from '../lib/format';
import { useNow } from '../lib/useNow';
import { useAccount } from '../licensing/account';
import { HubError } from '../licensing/api';
import { config } from '../licensing/config';
import { currentTier, useEntitlement } from '../licensing/entitlement';
import { FEATURES, requiredTier, type Feature, type Tier } from '../licensing/gate';
import { hub } from '../licensing/hub';
import { PlayPane } from '../licensing/play/PlayPane';
import { useSession } from '../session/store';
import { confirm } from '../ui/confirm';

// Inlined at build time (babel.config.js): in the play bundle this is
// `null`, and the NabooPay module is not bundled at all.
const NabooPayPane: ComponentType<{ boxId: string; tier: Tier }> | null =
  process.env.KERGUI_FLAVOR === 'naboopay'
    ? // eslint-disable-next-line @typescript-eslint/no-require-imports
      (require('../licensing/naboopay/NabooPayPane') as typeof import('../licensing/naboopay/NabooPayPane')).NabooPayPane
    : null;

const tierName = (tier: Tier) => t(tier === 'max' ? 'pro.max' : tier === 'basic' ? 'pro.basic' : 'pro.freeTier');

function Status({ tier }: { tier: Tier }) {
  const claims = useEntitlement((s) => s.claims);
  const now = Math.floor(useNow() / 1000);
  const paid = tier !== 'free' && claims;
  return (
    <Card mode="contained">
      <Card.Content style={styles.gap}>
        <Text variant="titleMedium">{t('pro.current', { tier: tierName(tier) })}</Text>
        {paid && now < claims.expiresAt ? <Text variant="bodyMedium">{t('pro.until', { date: formatDay(claims.expiresAt) })}</Text> : null}
        {paid && now >= claims.expiresAt ? (
          <Text variant="bodyMedium">{t('pro.grace', { date: formatDay(claims.expiresAt + 3 * 86400) })}</Text>
        ) : null}
      </Card.Content>
    </Card>
  );
}

/** Moves the account's pass from a replaced box onto this one. */
function Transfer({ boxId }: { boxId: string }) {
  const qc = useQueryClient();
  const refresh = useEntitlement((s) => s.refresh);
  const subs = useQuery({ queryKey: ['hub', 'subscriptions'], queryFn: hub.subscriptions });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [done, setDone] = useState(false);
  const elsewhere = (subs.data ?? []).filter((s) => s.active && s.boxId !== boxId);
  if (elsewhere.length === 0) return null;

  const move = async (from: string, tier: Tier) => {
    if (!(await confirm(t('pro.transferConfirm', { tier: tierName(tier), from, to: boxId })))) return;
    setBusy(true);
    setError(null);
    try {
      await hub.transfer(from, boxId);
      setDone(true);
      await refresh({ force: true });
      await qc.invalidateQueries({ queryKey: ['hub', 'subscriptions'] });
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <List.Section title={t('pro.transferTitle')}>
      <Text variant="bodySmall" style={styles.pad}>
        {t('pro.transferHint')}
      </Text>
      {elsewhere.map((s) => (
        <List.Item
          key={s.boxId}
          title={t('pro.transferTo', { box: s.boxId })}
          description={`${tierName(s.effectiveTier)} · ${t('pro.until', { date: formatDay(s.expiresAt) })}`}
          left={(p) => <List.Icon {...p} icon="swap-horizontal" />}
          disabled={busy}
          onPress={() => void move(s.boxId, s.effectiveTier)}
          testID={`pro-transfer-${s.boxId}`}
        />
      ))}
      {done ? <HelperText type="info">{t('pro.transferDone')}</HelperText> : null}
      {error ? <HelperText type="error">{hubErrorMessage(error)}</HelperText> : null}
    </List.Section>
  );
}

function Offer({ boxId, tier }: { boxId: string; tier: Tier }) {
  const account = useAccount((s) => s.account);
  const signOut = useAccount((s) => s.signOut);
  if (!account) {
    return (
      <View style={styles.gap}>
        <Text variant="bodyMedium">{t('pro.signInFirst')}</Text>
        <Button mode="contained" onPress={() => router.push('/sign-in')} testID="pro-sign-in">
          {t('pro.signIn')}
        </Button>
      </View>
    );
  }
  return (
    <View style={styles.gap}>
      {config.flavor === 'naboopay' && NabooPayPane ? <NabooPayPane boxId={boxId} tier={tier} /> : <PlayPane tier={tier} />}
      <Transfer boxId={boxId} />
      <List.Item
        title={t('pro.account', { phone: account.phone })}
        left={(p) => <List.Icon {...p} icon="account-circle-outline" />}
        right={() => (
          <Button compact onPress={() => void signOut()}>
            {t('pro.signOut')}
          </Button>
        )}
      />
    </View>
  );
}

export default function ProScreen() {
  const { feature } = useLocalSearchParams<{ feature?: string }>();
  const demo = useSession((s) => s.session?.mode === 'demo');
  const { boxId, claims, refreshing, error, refresh, identify } = useEntitlement();
  const tier = currentTier({ claims, boxId }, useNow());
  const needed = feature && (FEATURES as readonly string[]).includes(feature) ? requiredTier(feature as Feature) : null;

  // Whenever the screen comes into view, the token is worth refreshing.
  useFocusEffect(
    useCallback(() => {
      void refresh();
    }, [refresh]),
  );

  const body = () => {
    if (demo) return <Text variant="bodyMedium">{t('pro.demo')}</Text>;
    if (!config.hubUrl) return <Text variant="bodyMedium">{t('pro.unconfigured')}</Text>;
    if (boxId === '') return <Text variant="bodyMedium">{t('pro.unsupported')}</Text>;
    if (boxId === null) {
      return (
        <View style={styles.gap}>
          <Text variant="bodyMedium">{t('pro.unidentified')}</Text>
          <Button mode="outlined" onPress={() => void identify().then(() => refresh({ force: true }))}>
            {t('pro.identify')}
          </Button>
        </View>
      );
    }
    return <Offer boxId={boxId} tier={tier} />;
  };

  const showError = error instanceof HubError && !error.sessionInvalid;
  return (
    <ScrollView
      contentContainerStyle={styles.body}
      refreshControl={<RefreshControl refreshing={refreshing} onRefresh={() => void refresh({ force: true })} />}
    >
      <Stack.Screen options={{ headerShown: true, title: t('pro.title') }} />
      {needed && needed !== 'free' && !demo ? (
        <HelperText type="info" style={styles.needs}>
          {t('pro.needs', { tier: tierName(needed) })}
        </HelperText>
      ) : null}
      <Text variant="headlineSmall" style={styles.heading}>
        {t('pro.pitch')}
      </Text>
      {!demo ? <Status tier={tier} /> : null}
      <Text variant="bodyMedium" style={styles.free}>
        {t('pro.free')}
      </Text>
      {body()}
      {showError ? <HelperText type={error.offline ? 'info' : 'error'}>{hubErrorMessage(error)}</HelperText> : null}
      <Text variant="bodySmall" style={styles.legal}>
        {t('pro.legal')}
      </Text>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  body: { padding: 24, gap: 16 },
  heading: { fontFamily: 'Inter_700Bold' },
  gap: { gap: 12 },
  pad: { paddingHorizontal: 16, opacity: 0.8 },
  needs: { fontSize: 14 },
  free: { opacity: 0.8 },
  legal: { opacity: 0.6 },
});
