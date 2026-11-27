import { StyleSheet, View } from 'react-native';
import { Button, Card, Text } from 'react-native-paper';

import { useHouseInternet, useRemoteMode } from '../features/queries';
import { t } from '../i18n';
import { useGate } from '../licensing/useGate';
import { ActionError } from './ActionError';
import { confirm } from './confirm';
import { LockedButton } from './LockedButton';
import { Hud } from './Hud';
import { useSemantic } from './theme';
import { KCard } from './KCard';

/**
 * "Internet de la maison" (#50): cut Internet for every device but the
 * relays and the protected ones (Basic), and give it back (free). Shown
 * only through the household's relay, or in the demo once its relay is
 * paired; the relay is the one that decides what is spared.
 */
export function HouseInternet() {
  const { available, run } = useHouseInternet();
  const remote = !!useRemoteMode().data;
  const { guard, can } = useGate();
  const tones = useSemantic();
  if (!available) return null;

  // Cutting the whole house is asked first (#119); giving Internet back is
  // not: it harms nobody.
  const cut = () =>
    guard(
      'block',
      async () => {
        const yes = await confirm({
          title: t('house.cutTitle'),
          message: t('house.cutConfirm'),
          confirmLabel: t('house.cutYes'),
          destructive: true,
          icon: 'web-off',
        });
        if (yes) run.mutate(true);
      },
      t('house.cut'),
    );
  const restore = () => run.mutate(false);
  const done = run.isSuccess ? run.data : null;

  return (
    // The box's state: one of the two places with the site's HUD corners (#128).
    <Hud style={styles.card}>
      <KCard testID="house-internet">
        <Card.Title titleVariant="titleMedium" title={t('house.title')} subtitle={t('house.hint')} subtitleNumberOfLines={2} />
        <Card.Content style={styles.content}>
          <View style={styles.row}>
            {/* Red cuts, green gives back (#75); a cut the account lacks shows its lock (#120). */}
            {!can('block') ? (
              <LockedButton label={t('house.cut')} onPress={cut} testID="house-cut" />
            ) : (
              <Button
                mode="outlined"
                textColor={tones.blocked.color}
                style={{ borderColor: tones.blocked.color }}
                icon="web-off"
                onPress={cut}
                disabled={run.isPending}
                loading={run.isPending && run.variables}
                testID="house-cut"
              >
                {t('house.cut')}
              </Button>
            )}
            <Button
              mode="contained"
              buttonColor={tones.restore.color}
              textColor={tones.restore.onColor}
              icon="web"
              onPress={restore}
              disabled={run.isPending}
              loading={run.isPending && !run.variables}
              testID="house-restore"
            >
              {t('house.restore')}
            </Button>
          </View>
          {run.isPending && remote ? <Text variant="bodySmall">{t('remote.sending')}</Text> : null}
          {done?.action === 'cut' ? (
            <Text variant="bodyMedium" testID="house-cut-done">
              {t('house.cutDone', { n: done.cut.length, spared: done.spared.length })}
            </Text>
          ) : null}
          {done?.action === 'restore' ? (
            <Text variant="bodyMedium" testID="house-restore-done">
              {t('house.restoreDone', { n: done.restored.length })}
            </Text>
          ) : null}
          {!remote ? <Text variant="bodySmall">{t('house.demoNote')}</Text> : null}
          <ActionError error={run.error} />
        </Card.Content>
      </KCard>
    </Hud>
  );
}

const styles = StyleSheet.create({
  card: { marginHorizontal: 12, marginTop: 12 },
  content: { gap: 8 },
  row: { flexDirection: 'row', gap: 8, flexWrap: 'wrap' },
});
