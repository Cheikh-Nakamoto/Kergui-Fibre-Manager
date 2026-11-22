import { useQuery, useQueryClient } from '@tanstack/react-query';
import { router, Stack } from 'expo-router';
import { useEffect, useState } from 'react';
import { RefreshControl, ScrollView, StyleSheet, View } from 'react-native';
import { Button, Chip, HelperText, List, Text, TextInput } from 'react-native-paper';

import {
  DEMO_CODES,
  delegationOf,
  delegations,
  delegationsKey,
  loadDelegations,
  useDemoDelegations,
  type DelegationsView,
} from '../features/delegations';
import { hubErrorMessage, t } from '../i18n';
import { formatDay } from '../lib/format';
import { useAccount } from '../licensing/account';
import type { HouseholdMember, HouseholdMembers, MemberRole } from '../licensing/api';
import { config } from '../licensing/config';
import { DEMO_HOUSEHOLD, household, householdKey } from '../licensing/household';
import { useSession } from '../session/store';
import { confirm } from '../ui/confirm';
import { DelegationsSection, MemberAccessSection, type DelegationRow } from '../ui/Delegations';

// The Household screen (issue #42). Everyone in the household sees who is
// in it; the owner invites a number on WhatsApp with a role, changes a
// member's role, removes a member, and withdraws a pending invite. The
// invite is accepted when that number signs in to Kergui. Below, remote
// access (issue #47): the owner delegates "voir" or "contrôler" to a
// member's own key, revokes it, or keeps the box to themself; a member sees
// what they hold, and their phone publishes its key here.

const roleName = (r: MemberRole) => t(r === 'owner' ? 'household.owner' : r === 'admin' ? 'household.admin' : 'household.viewer');

function Members({
  data,
  owner,
  busy,
  act,
  view,
}: {
  data: HouseholdMembers;
  owner: boolean;
  busy: boolean;
  act: (run: () => Promise<unknown>) => void;
  view?: DelegationsView;
}) {
  const label = (m: HouseholdMember) => {
    const phone = m.phone ?? t('household.unknownPhone');
    return m.userId === data.you.userId ? t('household.you', { phone }) : phone;
  };
  const remove = async (m: HouseholdMember) => {
    if (!(await confirm(t('household.removeConfirm', { phone: m.phone ?? t('household.unknownPhone') })))) return;
    // A member who leaves keeps no remote access: their delegation is
    // revoked in the bundle the relays read.
    const delegated = delegationOf(m, view);
    act(async () => {
      await household.remove(m.membershipId);
      if (delegated) await delegations.revoke(delegated.keyId);
    });
  };
  return (
    <List.Section title={t('household.members', { count: data.members.length })}>
      {data.members.map((m) => {
        const manageable = owner && m.role !== 'owner';
        return (
          <View key={m.membershipId} testID={`household-member-${m.membershipId}`}>
            <List.Item
              title={label(m)}
              description={roleName(m.role)}
              left={(p) => <List.Icon {...p} icon={m.role === 'owner' ? 'home-account' : 'account-outline'} />}
            />
            {manageable ? (
              <View style={styles.actions}>
                <Button
                  compact
                  disabled={busy}
                  onPress={() => act(() => household.changeRole(m.membershipId, m.role === 'admin' ? 'viewer' : 'admin'))}
                  testID={`household-role-${m.membershipId}`}
                >
                  {t(m.role === 'admin' ? 'household.makeViewer' : 'household.makeAdmin')}
                </Button>
                <Button compact disabled={busy} onPress={() => void remove(m)} testID={`household-remove-${m.membershipId}`}>
                  {t('household.remove')}
                </Button>
              </View>
            ) : null}
          </View>
        );
      })}
    </List.Section>
  );
}

function Invite({ busy, act }: { busy: boolean; act: (run: () => Promise<unknown>) => Promise<boolean> }) {
  const [phone, setPhone] = useState('');
  const [role, setRole] = useState<Exclude<MemberRole, 'owner'>>('viewer');
  const [sentTo, setSentTo] = useState<string | null>(null);
  const send = async () => {
    setSentTo(null);
    let to = '';
    const ok = await act(async () => {
      to = (await household.invite(phone.trim(), role)).phone;
    });
    if (ok) {
      setSentTo(to);
      setPhone('');
    }
  };
  return (
    <List.Section title={t('household.invite')}>
      <View style={styles.gap}>
        <TextInput
          mode="outlined"
          label={t('household.invitePhone')}
          value={phone}
          onChangeText={setPhone}
          keyboardType="phone-pad"
          testID="household-invite-phone"
        />
        <View style={styles.actions}>
          {(['viewer', 'admin'] as const).map((r) => (
            <Chip key={r} selected={role === r} showSelectedCheck onPress={() => setRole(r)} testID={`household-invite-role-${r}`}>
              {roleName(r)}
            </Chip>
          ))}
        </View>
        <Text variant="bodySmall" style={styles.hint}>
          {t('household.roleHint')}
        </Text>
        <Button mode="contained" icon="whatsapp" onPress={() => void send()} loading={busy} disabled={busy || !phone.trim()} testID="household-invite-send">
          {t('household.inviteSend')}
        </Button>
        {sentTo ? (
          <HelperText type="info" testID="household-invite-sent">
            {t('household.inviteSent', { phone: sentTo })}
          </HelperText>
        ) : null}
      </View>
    </List.Section>
  );
}

function Pending({ data, busy, act }: { data: HouseholdMembers; busy: boolean; act: (run: () => Promise<unknown>) => void }) {
  const invites = data.invites ?? [];
  if (invites.length === 0) return null;
  return (
    <List.Section title={t('household.pending')}>
      {invites.map((i) => (
        <List.Item
          key={i.id}
          title={i.phone}
          description={t('household.pendingItem', { role: roleName(i.role), date: formatDay(i.expiresAt) })}
          left={(p) => <List.Icon {...p} icon="email-fast-outline" />}
          right={() => (
            <Button compact disabled={busy} onPress={() => act(() => household.cancelInvite(i.id))} testID={`household-cancel-${i.id}`}>
              {t('household.cancelInvite')}
            </Button>
          )}
        />
      ))}
    </List.Section>
  );
}

/** The owner's rows: every member but the owner, with their key's code and delegation. */
function useDelegationRows(data: HouseholdMembers | undefined, view: DelegationsView | undefined, owner: boolean): DelegationRow[] {
  const others = (data?.members ?? []).filter((m) => m.role !== 'owner');
  const withKeys = others.filter((m) => m.keys);
  const codes = useQuery({
    queryKey: ['hub', 'delegation-codes', ...withKeys.map((m) => m.keys?.keyId)],
    queryFn: async () => Object.fromEntries(await Promise.all(withKeys.map(async (m) => [m.membershipId, await delegations.code(m)] as const))),
    enabled: owner && withKeys.length > 0,
    staleTime: Infinity,
  });
  return others.map((m) => ({
    id: m.membershipId,
    phone: m.phone ?? t('household.unknownPhone'),
    role: m.role,
    hasKey: !!m.keys,
    code: codes.data?.[m.membershipId],
    delegation: delegationOf(m, view),
  }));
}

function SignedIn() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: householdKey, queryFn: household.members });
  const d = useQuery({ queryKey: delegationsKey, queryFn: loadDelegations, enabled: !!q.data });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  // One change at a time; the list is read again after each.
  const act = async (run: () => Promise<unknown>): Promise<boolean> => {
    setBusy(true);
    setError(null);
    try {
      await run();
      return true;
    } catch (e) {
      setError(e);
      return false;
    } finally {
      setBusy(false);
      await Promise.all([qc.invalidateQueries({ queryKey: householdKey }), qc.invalidateQueries({ queryKey: delegationsKey })]);
    }
  };
  const fire = (run: () => Promise<unknown>) => void act(run);

  const data = q.data;
  const owner = data?.you.role === 'owner';
  const shown = error ?? (data ? null : q.error);
  const rows = useDelegationRows(data, d.data, owner);

  // A member's phone publishes its own key (created in its core) for the
  // owner to delegate; once, when the household does not list it yet.
  const you = data && !owner ? data.you : null;
  useEffect(() => {
    if (!you) return;
    let live = true;
    delegations
      .publishKey(you)
      .then((sent) => {
        if (sent && live) void qc.invalidateQueries({ queryKey: householdKey });
      })
      .catch(() => undefined); // the next visit tries again
    return () => {
      live = false;
    };
  }, [you?.userId, you?.keys?.keyId, qc]); // eslint-disable-line react-hooks/exhaustive-deps

  const member = (id: string) => data?.members.find((m) => m.membershipId === id);
  const grant = (id: string, scope: 'view' | 'control') => {
    const m = member(id);
    if (m) fire(() => delegations.grant(m, scope));
  };
  const revoke = async (id: string) => {
    const m = member(id);
    const del = m && delegationOf(m, d.data);
    if (!m || !del) return;
    if (!(await confirm(t('delegation.revokeConfirm', { phone: m.phone ?? t('household.unknownPhone') })))) return;
    fire(() => delegations.revoke(del.keyId));
  };
  return (
    <ScrollView
      contentContainerStyle={styles.body}
      refreshControl={<RefreshControl refreshing={q.isFetching && !!data} onRefresh={() => void q.refetch()} />}
    >
      {data ? (
        <>
          <Text variant="titleMedium">{data.household.name}</Text>
          <Members data={data} owner={owner} busy={busy} act={fire} view={d.data} />
          {owner ? (
            <>
              {rows.length ? (
                <DelegationsSection
                  rows={rows}
                  ownerOnly={!!d.data?.ownerOnly}
                  busy={busy || d.isFetching}
                  noRelay={d.isSuccess && !(d.data.owner && d.data.hasRelay)}
                  onGrant={grant}
                  onRevoke={(id) => void revoke(id)}
                  onOwnerOnly={(on) => fire(() => delegations.setOwnerOnly(on))}
                />
              ) : null}
              <Invite busy={busy} act={act} />
              <Pending data={data} busy={busy} act={fire} />
            </>
          ) : (
            <>
              {d.data ? <MemberAccessSection access={d.data.member} ownerOnly={d.data.ownerOnly} /> : null}
              <Text variant="bodyMedium" style={styles.hint} testID="household-owner-only">
                {t('household.ownerOnly')}
              </Text>
            </>
          )}
        </>
      ) : null}
      {shown ? (
        <HelperText type="error" testID="household-error">
          {hubErrorMessage(shown)}
        </HelperText>
      ) : null}
    </ScrollView>
  );
}

/** The demo's remote access: the example members, in memory, nothing signed. */
function DemoDelegations() {
  const demo = useDemoDelegations();
  const rows: DelegationRow[] = DEMO_HOUSEHOLD.members
    .filter((m) => m.role !== 'owner')
    .map((m) => ({
      id: m.membershipId,
      phone: m.phone ?? '',
      role: m.role,
      hasKey: true,
      code: DEMO_CODES[m.membershipId],
      delegation: demo.byMember[m.membershipId],
    }));
  const revoke = async (id: string) => {
    const phone = rows.find((r) => r.id === id)?.phone ?? '';
    if (await confirm(t('delegation.revokeConfirm', { phone }))) demo.revoke(id);
  };
  return (
    <>
      <DelegationsSection
        rows={rows}
        ownerOnly={demo.ownerOnly}
        busy={false}
        onGrant={(id, scope) => demo.grant(id, scope)}
        onRevoke={(id) => void revoke(id)}
        onOwnerOnly={demo.setOwnerOnly}
      />
      <Text variant="bodySmall" style={styles.hint} testID="delegation-demo">
        {t('delegation.demo')}
      </Text>
    </>
  );
}

export default function HouseholdScreen() {
  const demo = useSession((s) => s.session?.mode === 'demo');
  const account = useAccount((s) => s.account);

  const body = () => {
    if (demo) {
      // A made-up household: nothing is sent from the demo.
      return (
        <ScrollView contentContainerStyle={styles.body}>
          <Text variant="bodyMedium" testID="household-demo">
            {t('household.demo')}
          </Text>
          <Members data={DEMO_HOUSEHOLD} owner={false} busy={false} act={() => undefined} />
          <DemoDelegations />
        </ScrollView>
      );
    }
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
          <Text variant="bodyMedium">{t('household.signInFirst')}</Text>
          <Button mode="contained" onPress={() => router.push('/sign-in')} testID="household-sign-in">
            {t('pro.signIn')}
          </Button>
        </View>
      );
    }
    return <SignedIn />;
  };

  return (
    <>
      <Stack.Screen options={{ headerShown: true, title: t('household.title') }} />
      {body()}
    </>
  );
}

const styles = StyleSheet.create({
  body: { padding: 16, gap: 8 },
  gap: { gap: 12 },
  actions: { flexDirection: 'row', flexWrap: 'wrap', gap: 8, paddingHorizontal: 16 },
  hint: { opacity: 0.8 },
});
