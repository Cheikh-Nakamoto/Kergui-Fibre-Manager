import { useState, type ReactNode } from 'react';
import { StyleSheet, View } from 'react-native';
import { Button, HelperText, List, Text, TextInput } from 'react-native-paper';

import { RecoveryError } from '../core/recovery';
import { hubErrorMessage, t } from '../i18n';
import type { MemberRole } from '../licensing/api';

// The "Téléphone perdu" screen's parts (issue #48). Presentational: the
// signing happens in the core, the uploads in src/features/recovery.ts.

/** A user-facing sentence for a failed recovery. */
export function recoveryErrorMessage(e: unknown): string {
  if (e instanceof RecoveryError) {
    return e.reason === 'other' ? t('recovery.error.other', { error: e.message }) : t(`recovery.error.${e.reason}`);
  }
  return hubErrorMessage(e);
}

export type CoOwnerRow = {
  id: string;
  phone: string;
  role: MemberRole;
  /** The member's phone published its key. */
  hasKey: boolean;
  coOwner: boolean;
};

type CoOwnersProps = {
  rows: CoOwnerRow[];
  busy: boolean;
  /** No relay paired yet: the screen says so and offers nothing. */
  noRelay?: boolean;
  onDesignate: (id: string) => void;
  onRemove: (id: string) => void;
};

/** The owner's co-owners: who may take the household over if this phone is lost. */
export function CoOwnersSection({ rows, busy, noRelay, onDesignate, onRemove }: CoOwnersProps) {
  let body: ReactNode;
  if (noRelay) {
    body = (
      <Text variant="bodyMedium" style={[styles.hint, styles.pad]} testID="coowner-no-relay">
        {t('recovery.noRelay')}
      </Text>
    );
  } else if (rows.length === 0) {
    body = (
      <Text variant="bodyMedium" style={[styles.hint, styles.pad]} testID="coowner-none">
        {t('recovery.noMembers')}
      </Text>
    );
  } else {
    body = rows.map((r) => (
      <View key={r.id} testID={`coowner-row-${r.id}`}>
        <List.Item
          title={r.phone}
          description={
            !r.hasKey
              ? t('recovery.noKey', { phone: r.phone })
              : r.role !== 'admin' && !r.coOwner
                ? t('recovery.viewerOnly')
                : t(r.coOwner ? 'recovery.coOwner' : 'recovery.notCoOwner')
          }
          descriptionNumberOfLines={3}
          left={(p) => <List.Icon {...p} icon={r.coOwner ? 'account-key' : 'account-outline'} />}
          testID={`coowner-state-${r.id}`}
        />
        <View style={styles.actions}>
          {r.coOwner ? (
            <Button compact disabled={busy} onPress={() => onRemove(r.id)} testID={`coowner-remove-${r.id}`}>
              {t('recovery.removeCoOwner')}
            </Button>
          ) : r.hasKey && r.role === 'admin' ? (
            <Button compact mode="contained-tonal" disabled={busy} onPress={() => onDesignate(r.id)} testID={`coowner-add-${r.id}`}>
              {t('recovery.designate')}
            </Button>
          ) : null}
        </View>
      </View>
    ));
  }
  return (
    <List.Section title={t('recovery.coOwners')} testID="recovery-coowners">
      <Text variant="bodySmall" style={[styles.hint, styles.pad]}>
        {t('recovery.coOwnersHint')}
      </Text>
      {body}
    </List.Section>
  );
}

/** A co-owner's phone: take the household over. */
export function TakeOverSection({ busy, onTakeOver }: { busy: boolean; onTakeOver: () => void }) {
  return (
    <List.Section title={t('recovery.takeOver')} testID="recovery-takeover-section">
      <Text variant="bodyMedium" style={[styles.hint, styles.pad]}>
        {t('recovery.takeOverHint')}
      </Text>
      <View style={styles.pad}>
        <Button mode="contained" icon="account-key" loading={busy} disabled={busy} onPress={onTakeOver} testID="recovery-takeover">
          {t('recovery.takeOver')}
        </Button>
      </View>
    </List.Section>
  );
}

type AtHomeProps = {
  busy: boolean;
  initialAddress?: string;
  onRecover: (relayAddress: string, boxPassword: string) => void;
};

/** The owner's new phone, at home: the relay's address and the box password. */
export function AtHomeSection({ busy, initialAddress = '', onRecover }: AtHomeProps) {
  const [address, setAddress] = useState(initialAddress);
  const [password, setPassword] = useState('');
  return (
    <List.Section title={t('recovery.atHome')} testID="recovery-at-home">
      <View style={[styles.pad, styles.gap]}>
        <Text variant="bodyMedium" style={styles.hint}>
          {t('recovery.atHomeHint')}
        </Text>
        <TextInput
          mode="outlined"
          label={t('recovery.relayAddress')}
          value={address}
          onChangeText={setAddress}
          keyboardType="numbers-and-punctuation"
          autoCapitalize="none"
          autoCorrect={false}
          testID="recovery-relay-address"
        />
        <HelperText type="info">{t('recovery.relayAddressHint')}</HelperText>
        <TextInput
          mode="outlined"
          label={t('recovery.boxPassword')}
          value={password}
          onChangeText={setPassword}
          secureTextEntry
          autoCapitalize="none"
          autoCorrect={false}
          testID="recovery-box-password"
        />
        <Button
          mode="contained"
          icon="home-lock"
          loading={busy}
          disabled={busy || !address.trim() || !password}
          onPress={() => onRecover(address.trim(), password)}
          testID="recovery-home-submit"
        >
          {t('recovery.recover')}
        </Button>
      </View>
    </List.Section>
  );
}

/** The household is this phone's now. */
export function RecoveredNotice() {
  return (
    <Text variant="bodyMedium" style={styles.done} testID="recovery-done">
      {t('recovery.done')}
    </Text>
  );
}

const styles = StyleSheet.create({
  actions: { flexDirection: 'row', flexWrap: 'wrap', gap: 8, paddingHorizontal: 16 },
  hint: { opacity: 0.8 },
  pad: { paddingHorizontal: 16 },
  gap: { gap: 8 },
  done: { paddingHorizontal: 16, fontWeight: '600' },
});
