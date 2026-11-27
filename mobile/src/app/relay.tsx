import { useQuery, useQueryClient } from '@tanstack/react-query';
import { router, Stack } from 'expo-router';
import { useEffect, useState } from 'react';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Button, Card, HelperText, List, Text, TextInput } from 'react-native-paper';

import { owner, type SignedBundle } from '../core/owner';
import { delegationsKey, loadDelegations, renameRelay } from '../features/delegations';
import { DEMO_RELAY_ID, useDemoRemote } from '../features/demoRemote';
import { hubErrorMessage, t } from '../i18n';
import { formatLogTime } from '../lib/format';
import { useAccount } from '../licensing/account';
import { config } from '../licensing/config';
import { household, householdKey } from '../licensing/household';
import { relays, type PairingStarted, type RelayKind } from '../licensing/relays';
import { useSession } from '../session/store';
import { QrCode } from '../ui/QrCode';
import { fontStyles } from '../ui/theme';
import { MAX_RELAY_NAME, RelayNamesSection } from '../ui/RelayNames';
import { KCard } from '../ui/KCard';

// Pairing a relay (issue #43). The owner picks what the relay is, shows a QR
// (the hub's pairing code and the owner's key fingerprint), waits for the
// relay to scan it, compares the six digits on both screens, and confirms:
// the phone's core then signs the relay into the household's trust bundle
// and the app uploads it, with the box password sealed for that relay
// (issue #44: ciphertext only, sealed in the core). Different digits remove
// the relay. The keys and the password never leave the core; this screen
// only ever holds public keys.
//
// In the demo there is no hub: the core plays the relay on the same QR, and
// the bundle it signs stays on the phone.

/** How often the QR screen asks the hub whether the relay has scanned. */
export const POLL_MS = 3000;

/** The relay being compared, as the hub listed it (or the demo made it). */
type Candidate = { id: string; name: string; kind: string; ed25519: string; x25519: string };

type Step =
  | { at: 'intro' }
  | { at: 'qr'; started: PairingStarted; kind: RelayKind }
  | { at: 'compare'; relay: Candidate; code: string; relayCode?: string; name: string }
  | { at: 'done'; name: string; passwordSent: boolean }
  | { at: 'rejected' }
  | { at: 'expired' };

/** "042 917": six digits read in two groups. */
export const groupCode = (code: string) => `${code.slice(0, 3)} ${code.slice(3)}`;

// The demo household's bundle, signed by the demo phone's own core: it
// stays here, nothing is uploaded.
let demoBundle: SignedBundle | null = null;

/** The relays already paired, by name, renamed from here (#57); shown above the intro. */
function PairedRelays({ demo }: { demo: boolean }) {
  const qc = useQueryClient();
  const demoRelay = useDemoRemote();
  const q = useQuery({ queryKey: delegationsKey, queryFn: loadDelegations, enabled: !demo });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  if (demo) {
    if (!demoRelay.relay) return null;
    return (
      <RelayNamesSection
        relays={[{ agentId: DEMO_RELAY_ID, kind: demoRelay.kind, label: demoRelay.relay }]}
        canRename
        onRename={(_, name) => demoRelay.rename(name)}
      />
    );
  }
  const rename = async (agentId: string, name: string) => {
    setBusy(true);
    setError(null);
    try {
      await renameRelay(agentId, name);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
      await qc.invalidateQueries({ queryKey: delegationsKey });
    }
  };
  return (
    <>
      <RelayNamesSection relays={q.data?.relays ?? []} canRename={!!q.data?.owner} busy={busy} onRename={rename} />
      {error ? (
        <HelperText type="error" testID="relay-rename-error">
          {hubErrorMessage(error)}
        </HelperText>
      ) : null}
    </>
  );
}

function Pairing({ demo, routerPin }: { demo: boolean; routerPin: string }) {
  const [step, setStep] = useState<Step>({ at: 'intro' });
  const [kind, setKind] = useState<RelayKind>('pc');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const run = async (work: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await work();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  const start = () =>
    run(async () => {
      if (demo) {
        const invite = await owner.invite(`DEMO-${Date.now().toString(36)}`);
        setStep({ at: 'qr', kind, started: { invite, expiresAt: new Date(Date.now() + 5 * 60_000).toISOString(), known: [] } });
        return;
      }
      setStep({ at: 'qr', kind, started: await relays.start(kind, routerPin) });
    });

  // Waiting for the relay to redeem the code: the hub lists it once it has.
  const qr = step.at === 'qr' ? step.started : null;
  useEffect(() => {
    if (!qr || demo) return;
    let stopped = false;
    const check = async () => {
      if (Date.parse(qr.expiresAt) < Date.now()) {
        if (!stopped) setStep({ at: 'expired' });
        return;
      }
      try {
        const agent = await relays.arrived(qr.known);
        if (!agent || stopped) return;
        const code = await relays.code(agent);
        if (!stopped) setStep({ at: 'compare', relay: agent, code, name: agent.name });
      } catch (e) {
        if (!stopped) setError(e);
      }
    };
    void check();
    const timer = setInterval(() => void check(), POLL_MS);
    return () => {
      stopped = true;
      clearInterval(timer);
    };
  }, [qr, demo]);

  const demoScan = () =>
    run(async () => {
      if (!qr) return;
      const relay = await owner.demoRelay(qr.invite);
      const code = await owner.pairingCode(relay.ed25519, relay.x25519);
      setStep({ at: 'compare', relay: { ...relay, id: relay.agentId }, code, relayCode: relay.code, name: relay.name });
    });

  // The name the owner keeps for the relay: its own (model, host name) unless changed here.
  const same = (candidate: Candidate, typed: string) =>
    run(async () => {
      const relay = { ...candidate, name: typed.trim() || candidate.name };
      if (demo) {
        demoBundle = await owner.signBundle('demo', demoBundle, {
          relay: { agentId: relay.id, kind: relay.kind, label: relay.name, ed25519: relay.ed25519, x25519: relay.x25519 },
        });
        // Sealed for real by the core, from the demo box's password; kept here.
        const sealed = await owner.sealCredentials('demo', demoBundle, relay.id);
        // The Devices tab now shows the (simulated) remote banner (#46).
        useDemoRemote.getState().paired(relay.name, relay.kind);
        setStep({ at: 'done', name: relay.name, passwordSent: sealed.boxPassword && sealed.relays.length > 0 });
        return;
      }
      const { passwordSent } = await relays.confirm(relay);
      setStep({ at: 'done', name: relay.name, passwordSent });
    });

  const different = (relay: Candidate) =>
    run(async () => {
      if (!demo) await relays.reject(relay);
      setStep({ at: 'rejected' });
    });

  const again = () => {
    setError(null);
    setStep({ at: 'intro' });
  };

  const body = () => {
    switch (step.at) {
      case 'intro':
        return (
          <View style={styles.gap} testID="relay-intro">
            <PairedRelays demo={demo} />
            <Text variant="titleMedium">{t('relay.introTitle')}</Text>
            <Text variant="bodyMedium">{t('relay.intro')}</Text>
            <Text variant="bodyMedium" style={styles.hint}>
              {t('relay.introSafe')}
            </Text>
            <List.Section titleStyle={fontStyles.display} title={t('relay.whichKind')}>
              {(
                [
                  ['pc', 'relay.kindPc', 'relay.kindPcHint', 'laptop'],
                  ['dedicated', 'relay.kindPhone', 'relay.kindPhoneHint', 'cellphone-link'],
                ] as const
              ).map(([k, title, hint, icon]) => (
                <List.Item
                  key={k}
                  title={t(title)}
                  description={t(hint)}
                  left={(p) => <List.Icon {...p} icon={icon} />}
                  right={(p) => <List.Icon {...p} icon={kind === k ? 'radiobox-marked' : 'radiobox-blank'} />}
                  onPress={() => setKind(k)}
                  accessibilityState={{ selected: kind === k }}
                  testID={`relay-kind-${k}`}
                />
              ))}
            </List.Section>
            <Button mode="contained" icon="qrcode" onPress={() => void start()} loading={busy} disabled={busy} testID="relay-start">
              {t('relay.start')}
            </Button>
          </View>
        );
      case 'qr':
        return (
          <View style={styles.gap}>
            <Text variant="titleMedium">{t('relay.scanTitle')}</Text>
            <QrCode value={step.started.invite} testID="relay-qr" />
            {step.kind === 'pc' ? (
              <>
                <Text variant="bodyMedium">{t('relay.scanPc')}</Text>
                <KCard>
                  <Card.Content>
                    <Text selectable variant="bodySmall" style={styles.mono} testID="relay-command">
                      {`kergui hub enroll ${step.started.invite} --hub ${config.hubUrl || 'https://hub.kergui.app'}`}
                    </Text>
                  </Card.Content>
                </KCard>
              </>
            ) : (
              <>
                <Text variant="bodyMedium">{t('relay.scanPhone')}</Text>
                {/* Typed on the relay phone when its camera is refused (#45). */}
                <Text selectable variant="bodySmall" style={styles.mono} testID="relay-invite-text">
                  {t('relay.invite', { invite: step.started.invite })}
                </Text>
              </>
            )}
            <Text variant="bodySmall" style={styles.hint}>
              {t('relay.expires', { time: formatLogTime(step.started.expiresAt).slice(0, 5) })}
            </Text>
            {demo ? (
              <Button mode="contained" icon="cellphone-check" onPress={() => void demoScan()} loading={busy} disabled={busy} testID="relay-demo-scan">
                {t('relay.demoScan')}
              </Button>
            ) : (
              <Text variant="bodyMedium" testID="relay-waiting">
                {t('relay.waiting')}
              </Text>
            )}
          </View>
        );
      case 'compare':
        return (
          <View style={styles.gap} testID="relay-compare">
            <Text variant="titleMedium">{t('relay.compareTitle')}</Text>
            <Text variant="bodyMedium">{t('relay.compare', { name: step.relay.name })}</Text>
            <Text
              variant="displaySmall"
              style={styles.code}
              accessibilityLabel={t('relay.codeLabel', { code: step.code.split('').join(' ') })}
              testID="relay-code"
            >
              {groupCode(step.code)}
            </Text>
            {step.relayCode ? (
              <Text variant="bodyMedium" style={styles.center} testID="relay-code-relay">
                {t('relay.relayScreen', { code: groupCode(step.relayCode) })}
              </Text>
            ) : null}
            <TextInput
              mode="outlined"
              label={t('relay.nameLabel')}
              value={step.name}
              onChangeText={(name) => setStep({ ...step, name })}
              maxLength={MAX_RELAY_NAME}
              testID="relay-name-input"
            />
            <Text variant="bodySmall" style={styles.hint}>
              {t('relay.nameHint')}
            </Text>
            <Button mode="contained" icon="check" onPress={() => void same(step.relay, step.name)} loading={busy} disabled={busy} testID="relay-same">
              {t('relay.same')}
            </Button>
            <Button mode="outlined" icon="close" onPress={() => void different(step.relay)} disabled={busy} testID="relay-different">
              {t('relay.different')}
            </Button>
          </View>
        );
      case 'done':
        return (
          <View style={styles.gap} testID="relay-done">
            <Text variant="titleMedium">{t('relay.doneTitle')}</Text>
            <Text variant="bodyMedium">{t('relay.done', { name: step.name })}</Text>
            <Text variant="bodyMedium" testID={step.passwordSent ? 'relay-password-sent' : 'relay-password-later'}>
              {t(step.passwordSent ? 'relay.passwordSent' : 'relay.passwordLater')}
            </Text>
            <Button mode="contained" onPress={() => (router.canGoBack() ? router.back() : router.replace('/'))} testID="relay-finish">
              {t('relay.finish')}
            </Button>
          </View>
        );
      case 'rejected':
        return (
          <View style={styles.gap} testID="relay-rejected">
            <Text variant="titleMedium">{t('relay.rejectedTitle')}</Text>
            <Text variant="bodyMedium">{t('relay.rejected')}</Text>
            <Button mode="contained" onPress={again} testID="relay-again">
              {t('relay.again')}
            </Button>
          </View>
        );
      case 'expired':
        return (
          <View style={styles.gap} testID="relay-expired">
            <Text variant="bodyMedium">{t('relay.expired')}</Text>
            <Button mode="contained" onPress={again} testID="relay-again">
              {t('relay.again')}
            </Button>
          </View>
        );
    }
  };

  return (
    <ScrollView contentContainerStyle={styles.body}>
      {demo ? (
        <Text variant="bodySmall" style={styles.hint} testID="relay-demo">
          {t('relay.demo')}
        </Text>
      ) : null}
      {body()}
      {error ? (
        <HelperText type="error" testID="relay-error">
          {hubErrorMessage(error)}
        </HelperText>
      ) : null}
    </ScrollView>
  );
}

/** The household's owner only: the bundle is theirs to sign. */
function SignedIn({ routerPin }: { routerPin: string }) {
  const q = useQuery({ queryKey: householdKey, queryFn: household.members });
  if (q.error) {
    return (
      <View style={styles.body}>
        <HelperText type="error" testID="relay-error">
          {hubErrorMessage(q.error)}
        </HelperText>
      </View>
    );
  }
  if (!q.data) return null;
  if (q.data.you.role !== 'owner') {
    return (
      <View style={styles.body}>
        <Text variant="bodyMedium" testID="relay-owner-only">
          {t('relay.ownerOnly')}
        </Text>
      </View>
    );
  }
  return <Pairing demo={false} routerPin={routerPin} />;
}

export default function RelayScreen() {
  const session = useSession((s) => s.session);
  const account = useAccount((s) => s.account);
  const demo = session?.mode === 'demo';

  const body = () => {
    if (demo) return <Pairing demo routerPin="" />;
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
          <Text variant="bodyMedium">{t('relay.signInFirst')}</Text>
          <Button mode="contained" onPress={() => router.push('/sign-in')} testID="relay-sign-in">
            {t('pro.signIn')}
          </Button>
        </View>
      );
    }
    return <SignedIn routerPin={session?.mode === 'router' ? session.pin : ''} />;
  };

  return (
    <>
      <Stack.Screen options={{ headerShown: true, title: t('relay.title') }} />
      {body()}
    </>
  );
}

const styles = StyleSheet.create({
  body: { padding: 16, gap: 12 },
  gap: { gap: 12 },
  hint: { opacity: 0.8 },
  center: { textAlign: 'center' },
  // A code, in the site's monospace (#128).
  code: { ...fontStyles.monoStrong, textAlign: 'center', letterSpacing: 4 },
  mono: fontStyles.mono,
});
