import { useQuery, useQueryClient } from '@tanstack/react-query';
import { router, Stack } from 'expo-router';
import { useState } from 'react';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Button, HelperText, Text } from 'react-native-paper';

import { delegationsKey } from '../features/delegations';
import { demoFindRelays, isCoOwner, loadRecovery, recovery, recoveryKey, useDemoRecovery } from '../features/recovery';
import { t } from '../i18n';
import { useAccount } from '../licensing/account';
import type { HouseholdMember } from '../licensing/api';
import { config } from '../licensing/config';
import { DEMO_HOUSEHOLD, household, householdKey } from '../licensing/household';
import { useSession } from '../session/store';
import { confirm } from '../ui/confirm';
import { AtHomeSection, CoOwnersSection, RecoveredNotice, TakeOverSection, recoveryErrorMessage, type CoOwnerRow } from '../ui/Recovery';

// "Téléphone perdu" (issue #48). The owner's phone names co-owners in
// advance. A co-owner's phone can take the household over. The owner's new
// phone, at home on the Wi-Fi, finds the relay by itself (#117) and proves
// the box password to it. Either
// way the core signs the household's next bundle with this phone's key, the
// old one revoked, and the relays refuse the old key's commands once they
// have it. The demo plays it all in memory.

function rowsOf(members: HouseholdMember[], coOwner: (m: HouseholdMember) => boolean): CoOwnerRow[] {
  return members
    .filter((m) => m.role !== 'owner')
    .map((m) => ({ id: m.membershipId, phone: m.phone ?? t('household.unknownPhone'), role: m.role, hasKey: !!m.keys, coOwner: coOwner(m) }));
}

function SignedIn() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: householdKey, queryFn: household.members });
  const r = useQuery({ queryKey: recoveryKey, queryFn: loadRecovery, enabled: !!q.data });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [done, setDone] = useState(false);

  const act = async (run: () => Promise<unknown>, after?: () => void) => {
    setBusy(true);
    setError(null);
    try {
      await run();
      after?.();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
      await Promise.all([
        qc.invalidateQueries({ queryKey: recoveryKey }),
        qc.invalidateQueries({ queryKey: delegationsKey }),
        qc.invalidateQueries({ queryKey: householdKey }),
      ]);
    }
  };

  const view = r.data;
  const members = q.data?.members ?? [];
  const member = (id: string) => members.find((m) => m.membershipId === id);

  const designate = async (id: string) => {
    const m = member(id);
    if (!m || !(await confirm(t('recovery.designateConfirm', { phone: m.phone ?? '' })))) return;
    void act(() => recovery.designate(m));
  };
  const remove = async (id: string) => {
    const m = member(id);
    const keyId = m?.keys?.keyId;
    if (!m || !keyId || !(await confirm(t('recovery.removeConfirm', { phone: m.phone ?? '' })))) return;
    void act(() => recovery.remove(keyId));
  };
  const takeOver = async () => {
    if (!(await confirm(t('recovery.takeOverConfirm')))) return;
    void act(() => recovery.takeOver(), () => setDone(true));
  };
  const atHome = async (address: string, password: string) => {
    if (!(await confirm(t('recovery.recoverConfirm')))) return;
    void act(() => recovery.atHome(address, password), () => setDone(true));
  };

  const shown = error ?? (q.data ? r.error : q.error);
  return (
    <ScrollView contentContainerStyle={styles.body}>
      {done ? <RecoveredNotice /> : null}
      {view?.owner ? (
        <>
          {!done ? (
            <Text variant="bodyMedium" style={styles.hint} testID="recovery-owner">
              {t('recovery.owner')}
            </Text>
          ) : null}
          <CoOwnersSection
            rows={rowsOf(members, (m) => isCoOwner(m, view))}
            busy={busy || r.isFetching}
            noRelay={!view.hasRelay}
            onDesignate={(id) => void designate(id)}
            onRemove={(id) => void remove(id)}
          />
        </>
      ) : view && !done ? (
        <>
          {view.member?.coOwner ? <TakeOverSection busy={busy} onTakeOver={() => void takeOver()} /> : null}
          <AtHomeSection busy={busy} find={recovery.findRelays} onRecover={(a, p) => void atHome(a, p)} />
        </>
      ) : null}
      {shown ? (
        <HelperText type="error" testID="recovery-error">
          {recoveryErrorMessage(shown)}
        </HelperText>
      ) : null}
    </ScrollView>
  );
}

/** The demo: the example household, in memory, nothing signed or sent. */
function DemoRecovery() {
  const demo = useDemoRecovery();
  const [error, setError] = useState<unknown>(null);
  const rows = rowsOf(DEMO_HOUSEHOLD.members, (m) => !!demo.coOwners[m.membershipId]).map((r) => ({ ...r, hasKey: true }));
  const phone = (id: string) => rows.find((r) => r.id === id)?.phone ?? '';
  const designate = async (id: string) => {
    if (await confirm(t('recovery.designateConfirm', { phone: phone(id) }))) demo.designate(id);
  };
  const remove = async (id: string) => {
    if (await confirm(t('recovery.removeConfirm', { phone: phone(id) }))) demo.remove(id);
  };
  const recover = async (address: string, password: string) => {
    setError(null);
    if (!(await confirm(t('recovery.recoverConfirm')))) return;
    try {
      demo.recover(address, password);
    } catch (e) {
      setError(e);
    }
  };
  return (
    <ScrollView contentContainerStyle={styles.body}>
      <CoOwnersSection rows={rows} busy={false} onDesignate={(id) => void designate(id)} onRemove={(id) => void remove(id)} />
      {demo.recovered ? <RecoveredNotice /> : <AtHomeSection busy={false} find={demoFindRelays} onRecover={(a, p) => void recover(a, p)} />}
      {error ? (
        <HelperText type="error" testID="recovery-error">
          {recoveryErrorMessage(error)}
        </HelperText>
      ) : null}
      <Text variant="bodySmall" style={styles.hint} testID="recovery-demo">
        {t('recovery.demo')}
      </Text>
    </ScrollView>
  );
}

export default function RecoveryScreen() {
  const demo = useSession((s) => s.session?.mode === 'demo');
  const account = useAccount((s) => s.account);

  const body = () => {
    if (demo) return <DemoRecovery />;
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
          <Text variant="bodyMedium">{t('recovery.signInFirst')}</Text>
          <Button mode="contained" onPress={() => router.push('/sign-in')} testID="recovery-sign-in">
            {t('pro.signIn')}
          </Button>
        </View>
      );
    }
    return <SignedIn />;
  };

  return (
    <>
      <Stack.Screen options={{ headerShown: true, title: t('recovery.title') }} />
      {body()}
    </>
  );
}

const styles = StyleSheet.create({
  body: { padding: 16, gap: 8 },
  gap: { gap: 12 },
  hint: { opacity: 0.8 },
});
