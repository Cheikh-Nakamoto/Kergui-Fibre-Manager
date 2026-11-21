import { useQuery } from '@tanstack/react-query';
import * as WebBrowser from 'expo-web-browser';
import { useState } from 'react';
import { StyleSheet, View } from 'react-native';
import { Button, Card, HelperText, List, Text } from 'react-native-paper';

import { hubErrorMessage, t } from '../../i18n';
import { confirm } from '../../ui/confirm';
import type { Pass } from '../api';
import { useEntitlement } from '../entitlement';
import type { PaidTier, Tier } from '../gate';
import { hub } from '../hub';
import { naboo } from './strings';

// The naboopay flavor: prepaid passes paid with Wave / Orange Money through
// NabooPay. The checkout page opens in the browser; NabooPay's webhook tells
// the hub, which credits the pass; the app then fetches a fresh token.

const tierName = (tier: PaidTier) => t(tier === 'max' ? 'pro.max' : 'pro.basic');

export function NabooPayPane({ boxId, tier }: { boxId: string; tier: Tier }) {
  const refresh = useEntitlement((s) => s.refresh);
  const catalogue = useQuery({ queryKey: ['hub', 'passes'], queryFn: hub.passes, staleTime: 3_600_000 });
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [waiting, setWaiting] = useState(false);

  const trial = async () => {
    setBusy('trial');
    setError(null);
    try {
      await hub.startTrial(boxId);
      await refresh({ force: true });
    } catch (e) {
      setError(e);
    } finally {
      setBusy(null);
    }
  };

  const buy = async (p: Pass) => {
    const label = naboo.pass(tierName(p.tier), p.term, p.days);
    if (!(await confirm(naboo.payConfirm(label, naboo.price(p.priceXof)), naboo.pay))) return;
    setBusy(p.code);
    setError(null);
    try {
      const out = await hub.checkout(boxId, p.code);
      await WebBrowser.openBrowserAsync(out.checkoutUrl);
      // Back from the browser: the webhook may already have credited it.
      setWaiting(true);
      await refresh({ force: true });
    } catch (e) {
      setError(e);
    } finally {
      setBusy(null);
    }
  };

  const passes = catalogue.data?.passes ?? [];
  const maxActive = tier === 'max';

  return (
    <View style={styles.root}>
      {tier === 'free' && catalogue.data ? (
        <Card mode="outlined">
          <Card.Content style={styles.gap}>
            <Button mode="contained" icon="gift-outline" onPress={trial} loading={busy === 'trial'} disabled={!!busy} testID="naboo-trial">
              {naboo.trial(catalogue.data.trialDays)}
            </Button>
            <Text variant="bodySmall">{naboo.trialHint}</Text>
          </Card.Content>
        </Card>
      ) : null}

      <Text variant="titleMedium">{naboo.passesTitle}</Text>
      <Text variant="bodySmall">{naboo.passesHint}</Text>
      {tier === 'basic' ? <Text variant="bodySmall">{naboo.upgradeHint}</Text> : null}
      {maxActive ? <Text variant="bodySmall">{naboo.maxActive}</Text> : null}

      {(['basic', 'max'] as const).map((group) => (
        <List.Section key={group} title={tierName(group)} style={styles.section}>
          {passes
            .filter((p) => p.tier === group)
            .map((p) => (
              <List.Item
                key={p.code}
                title={naboo.term(p.term, p.days)}
                description={naboo.price(p.priceXof)}
                disabled={!!busy || (maxActive && group === 'basic')}
                onPress={() => void buy(p)}
                right={(props) => (busy === p.code ? <List.Icon {...props} icon="timer-sand" /> : <List.Icon {...props} icon="chevron-right" />)}
                testID={`naboo-pass-${p.code}`}
              />
            ))}
        </List.Section>
      ))}

      {catalogue.error ? (
        <HelperText type="error">
          {naboo.loadFailed} {hubErrorMessage(catalogue.error)}
        </HelperText>
      ) : null}
      {waiting ? <HelperText type="info">{naboo.waiting}</HelperText> : null}
      {error ? <HelperText type="error">{hubErrorMessage(error)}</HelperText> : null}
    </View>
  );
}

const styles = StyleSheet.create({
  root: { gap: 8 },
  gap: { gap: 8 },
  section: { marginVertical: 0 },
});
