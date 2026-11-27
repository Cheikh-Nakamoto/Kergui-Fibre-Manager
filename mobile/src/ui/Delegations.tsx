import { StyleSheet, View } from 'react-native';
import { Button, List, Switch, Text } from 'react-native-paper';

import type { BundleDelegation, MemberAccess } from '../core/remote';
import { spacedCode } from '../features/delegations';
import { t } from '../i18n';
import { formatDay } from '../lib/format';
import type { MemberRole } from '../licensing/api';
import { fontStyles } from './theme';

// The Household screen's "Accès à distance" (issue #47): for the owner, one
// row per member with what the bundle delegates them, the six digits to
// compare with their screen, "Voir" / "Contrôler" / "Révoquer", and
// "Réservé au propriétaire"; for a member, what they hold. Presentational:
// the signing happens in src/features/delegations.ts.

export type DelegationRow = {
  id: string;
  phone: string;
  role: MemberRole;
  /** The member's phone published its key. */
  hasKey: boolean;
  code?: string;
  delegation?: BundleDelegation;
};

export function delegationState(d: BundleDelegation | undefined): string {
  if (!d) return t('delegation.none');
  return t(d.scope === 'control' ? 'delegation.control' : 'delegation.view', { date: formatDay(d.expiresAt) });
}

type OwnerProps = {
  rows: DelegationRow[];
  ownerOnly: boolean;
  busy: boolean;
  /** No relay paired yet: the screen says so and offers nothing. */
  noRelay?: boolean;
  onGrant: (id: string, scope: 'view' | 'control') => void;
  onRevoke: (id: string) => void;
  onOwnerOnly: (on: boolean) => void;
};

export function DelegationsSection({ rows, ownerOnly, busy, noRelay, onGrant, onRevoke, onOwnerOnly }: OwnerProps) {
  return (
    <List.Section titleStyle={fontStyles.display} title={t('delegation.title')} testID="delegations">
      <Text variant="bodySmall" style={styles.hint}>
        {t('delegation.hint')}
      </Text>
      {noRelay ? (
        <Text variant="bodyMedium" style={styles.hint} testID="delegation-no-relay">
          {t('delegation.noRelay')}
        </Text>
      ) : (
        <>
          <List.Item
            title={t('delegation.ownerOnly')}
            description={t('delegation.ownerOnlyHint')}
            descriptionNumberOfLines={4}
            right={() => (
              <Switch value={ownerOnly} disabled={busy} onValueChange={onOwnerOnly} testID="delegation-owner-only" />
            )}
          />
          {rows.map((r) => (
            <View key={r.id} testID={`delegation-row-${r.id}`} style={styles.row}>
              <List.Item
                title={r.phone}
                description={r.hasKey ? delegationState(r.delegation) : t('delegation.noKey', { phone: r.phone })}
                descriptionNumberOfLines={3}
                left={(p) => <List.Icon {...p} icon={r.delegation?.scope === 'control' ? 'shield-key' : r.delegation ? 'eye-outline' : 'key-outline'} />}
                testID={`delegation-state-${r.id}`}
              />
              {r.hasKey ? (
                <>
                  {r.code ? (
                    <Text variant="bodySmall" style={[styles.hint, styles.pad]} testID={`delegation-code-${r.id}`}>
                      {t('delegation.code', { code: spacedCode(r.code) })}
                    </Text>
                  ) : null}
                  <View style={styles.actions}>
                    <Button
                      compact
                      mode={r.delegation?.scope === 'view' ? 'contained-tonal' : 'text'}
                      disabled={busy}
                      onPress={() => onGrant(r.id, 'view')}
                      testID={`delegation-view-${r.id}`}
                    >
                      {t('delegation.grantView')}
                    </Button>
                    {r.role === 'admin' ? (
                      <Button
                        compact
                        mode={r.delegation?.scope === 'control' ? 'contained-tonal' : 'text'}
                        disabled={busy}
                        onPress={() => onGrant(r.id, 'control')}
                        testID={`delegation-control-${r.id}`}
                      >
                        {t('delegation.grantControl')}
                      </Button>
                    ) : null}
                    {r.delegation ? (
                      <Button compact disabled={busy} onPress={() => onRevoke(r.id)} testID={`delegation-revoke-${r.id}`}>
                        {t('delegation.revoke')}
                      </Button>
                    ) : null}
                  </View>
                  {r.role !== 'admin' ? (
                    <Text variant="bodySmall" style={[styles.hint, styles.pad]}>
                      {t('delegation.viewerControl')}
                    </Text>
                  ) : null}
                </>
              ) : null}
            </View>
          ))}
        </>
      )}
    </List.Section>
  );
}

/** A member's own access, as the bundle holds it. */
export function MemberAccessSection({ access, ownerOnly }: { access: MemberAccess | null; ownerOnly: boolean }) {
  const lines: string[] = [];
  const state = access?.state ?? 'none';
  if (state === 'active' && access?.expiresAt) {
    lines.push(t(access.scope === 'control' ? 'delegation.you.control' : 'delegation.you.view', { date: formatDay(access.expiresAt) }));
    if (ownerOnly) lines.push(t('delegation.you.ownerOnly'));
  } else if (state === 'active' || state === 'none') {
    lines.push(t('delegation.you.none'));
  } else {
    lines.push(t(`delegation.you.${state}`));
  }
  if (access?.code && state !== 'other_owner') lines.push(t('delegation.you.code', { code: spacedCode(access.code) }));
  return (
    <List.Section titleStyle={fontStyles.display} title={t('delegation.you')} testID="delegation-you">
      {lines.map((l) => (
        <Text key={l} variant="bodyMedium" style={[styles.hint, styles.pad]}>
          {l}
        </Text>
      ))}
    </List.Section>
  );
}

const styles = StyleSheet.create({
  row: { marginBottom: 4 },
  actions: { flexDirection: 'row', flexWrap: 'wrap', gap: 8, paddingHorizontal: 16 },
  hint: { opacity: 0.8 },
  pad: { paddingHorizontal: 16 },
});
