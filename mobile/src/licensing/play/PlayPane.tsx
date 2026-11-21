import { useEffect } from 'react';
import { StyleSheet, View } from 'react-native';
import { Button, Card, HelperText, Text } from 'react-native-paper';

import { hubErrorMessage, t } from '../../i18n';
import { HubError } from '../api';
import type { PaidTier, Tier } from '../gate';
import { usePlay } from './store';

// The play flavor's offer: Google Play subscriptions, priced by Google.

function message(e: unknown): string {
  if (e instanceof HubError) return hubErrorMessage(e);
  if (e && typeof e === 'object' && 'message' in e) return t('error.generic', { error: String((e as { message: unknown }).message) });
  return t('error.generic', { error: String(e) });
}

export function PlayPane({ tier }: { tier: Tier }) {
  const { available, products, status, error, notice, init, buy, restore, manage } = usePlay();
  useEffect(() => {
    void init();
  }, [init]);

  const busy = status !== 'idle';
  return (
    <View style={styles.root}>
      {(['basic', 'max'] as PaidTier[]).map((plan) => {
        const p = products[plan];
        const name = t(plan === 'max' ? 'pro.max' : 'pro.basic');
        const current = tier === plan;
        return (
          <Card key={plan} mode={current ? 'contained' : 'outlined'}>
            <Card.Title title={name} subtitle={p?.price ? t('play.perMonth', { price: p.price }) : undefined} />
            <Card.Content style={styles.gap}>
              <Text variant="bodyMedium">{t(plan === 'max' ? 'pro.maxFeatures' : 'pro.basicFeatures')}</Text>
              {current ? (
                <Button mode="outlined" onPress={manage} testID={`play-manage-${plan}`}>
                  {t('play.manage')}
                </Button>
              ) : (
                <Button
                  mode="contained"
                  onPress={() => void buy(plan)}
                  loading={status === 'buying'}
                  disabled={!available || !p || busy || (plan === 'basic' && tier === 'max')}
                  testID={`play-buy-${plan}`}
                >
                  {t('play.subscribe', { tier: name })}
                </Button>
              )}
            </Card.Content>
          </Card>
        );
      })}
      <Button mode="text" onPress={() => void restore()} disabled={!available || busy} loading={status === 'syncing'}>
        {t('play.restore')}
      </Button>
      {status === 'pending' ? <HelperText type="info">{t('play.pending')}</HelperText> : null}
      {!available ? <HelperText type="info">{t('play.unavailable')}</HelperText> : null}
      {notice ? <HelperText type="info">{t(notice === 'restored' ? 'play.restored' : 'play.nothingToRestore')}</HelperText> : null}
      {error ? <HelperText type="error">{message(error)}</HelperText> : null}
    </View>
  );
}

const styles = StyleSheet.create({
  root: { gap: 12 },
  gap: { gap: 8 },
});
