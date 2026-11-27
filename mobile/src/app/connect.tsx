import { router, Stack } from 'expo-router';
import { useState } from 'react';
import { KeyboardAvoidingView, Platform, ScrollView, StyleSheet, View } from 'react-native';
import { Button, HelperText, Text, TextInput } from 'react-native-paper';

import { ApiError, api, core } from '../core/client';
import { errorMessage, t } from '../i18n';
import { relays } from '../licensing/relays';
import { DEFAULT_BOX_ADDRESS, trustDecision } from '../lib/boxTrust';
import { formatFingerprint, normaliseRouterUrl } from '../lib/format';
import { boxRole, type BoxRole } from '../session/boxOwner';
import { knownBoxPin, rememberBoxPin } from '../session/boxPins';
import { setupMACFilter } from '../session/filterSetup';
import { useSession } from '../session/store';
import { BoxFirstPassword, BoxOwnerNeeded } from '../ui/BoxFirstPassword';
import { confirm } from '../ui/confirm';
import { Fold } from '../ui/Fold';
import { PasswordInput } from '../ui/PasswordInput';

/**
 * « C'est bien votre box ? » (#122, audit H6), asked only where a household
 * can answer: an address typed by hand, or a box whose certificate is not
 * the one Kergui remembers. The fingerprint waits under « Détails ».
 */
function askTrust(fingerprint: string, changed: boolean): Promise<boolean> {
  return confirm({
    title: t('connect.trustTitle'),
    message: changed ? t('connect.trustChanged') : t('connect.trustBody'),
    confirmLabel: t('connect.trust'),
    cancelLabel: t('connect.notMine'),
    details: t('connect.fingerprint', { fingerprint: formatFingerprint(fingerprint) }),
  });
}

/**
 * Whether to go on with the certificate an https box presents (#122): the
 * one Kergui remembers for this address goes on; the very first one on the
 * default private address is kept without a question, its fingerprint in
 * the journal; anything else asks. A different certificate is never
 * accepted in silence.
 */
async function trusted(url: string, pin: string): Promise<boolean> {
  let known: string | null | undefined;
  try {
    known = await knownBoxPin(url);
  } catch (e) {
    core.note('warn', `connexion: empreintes connues illisibles (${message(e)})`);
    known = undefined; // asks
  }
  const decision = trustDecision(url, pin, known);
  const fp = formatFingerprint(pin);
  switch (decision) {
    case 'known':
      core.note('info', `connexion: certificat déjà connu pour ${url} (empreinte ${fp})`);
      return true;
    case 'firstDefault':
      core.note('info', `connexion: première connexion à l’adresse par défaut, certificat retenu sans question (empreinte ${fp})`);
      return true;
    case 'changed':
      core.note('warn', `connexion: le certificat de ${url} a changé (empreinte ${fp}, connue ${formatFingerprint(known ?? '')})`);
      return askTrust(pin, true);
    default:
      return askTrust(pin, false);
  }
}

/** The household answered « Non »: nothing is tried, and the screen says why. */
const REFUSED = 'refused';

/**
 * Finds how to reach the box: https first (pinning its self-signed
 * certificate on first use), then plain http for the older models.
 *
 * Every attempt is written to the core's journal, because this is the step
 * that fails in a household and the one nobody can describe afterwards. A
 * candidate that does not work is a reason to try the next one, never a
 * reason to stop: an https port that answers the handshake but serves no
 * router page used to end the search before http was ever tried. « Non » to
 * « C'est bien votre box ? » stops: plain http on the same address is not
 * tried behind the household's back.
 */
async function reach(input: string): Promise<Target | typeof REFUSED> {
  const url = normaliseRouterUrl(input);
  const candidates = url.startsWith('https://') ? [url, url.replace(/^https:/, 'http:')] : [url];
  for (const candidate of candidates) {
    core.note('info', `connexion: essai de ${candidate}`);
    let pin = '';
    if (candidate.startsWith('https://')) {
      try {
        pin = await core.fingerprint(candidate);
      } catch (e) {
        core.note('warn', `connexion: pas de certificat sur ${candidate} (${message(e)})`);
        continue; // nothing listens on 443: try http
      }
      if (!pin) {
        core.note('warn', `connexion: certificat sans empreinte sur ${candidate}`);
        continue;
      }
      if (!(await trusted(candidate, pin))) {
        core.note('warn', 'connexion: certificat refusé par l’utilisateur (« Non »)');
        return REFUSED;
      }
    }
    try {
      await core.configure(candidate, pin);
      const info = await api.discover();
      if (info.Reachable) {
        core.note('info', `connexion: ${candidate} répond (${info.Vendor} ${info.Model}, adaptateur ${info.AdapterID})`);
        if (pin) await remember(candidate, pin);
        // « ZTE F6600P » for Réglages (#124): what discovery recognised.
        return { url: candidate, pin, model: [info.Vendor, info.Model].filter(Boolean).join(' ') };
      }
      core.note('warn', `connexion: ${candidate} répond mais n’est pas une box reconnue`);
    } catch (e) {
      core.note('warn', `connexion: ${candidate} a échoué (${message(e)})`);
    }
  }
  throw new Error(t('connect.unreachable'));
}

/** The box answered as a box: its fingerprint is the one to recognise next time. */
async function remember(url: string, pin: string) {
  try {
    await rememberBoxPin(url, pin);
    core.note('info', `connexion: empreinte enregistrée pour ${url} : ${formatFingerprint(pin)}`);
  } catch (e) {
    core.note('warn', `connexion: empreinte non enregistrée (${message(e)})`);
  }
}

/** A short reason for the journal, never a stack. */
function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/** A new box that demands a new admin password (#93), and who this phone is for it. */
/** Where the box answered, and what it is. */
type Target = { url: string; pin: string; model: string };

type NewBox = { target: Target; role: BoxRole };

export default function Connect() {
  const connected = useSession((s) => s.connected);
  const [address, setAddress] = useState(DEFAULT_BOX_ADDRESS);
  // The default login of the boxes Kergui knows is `user` (#91, Q14), on the label.
  const [username, setUsername] = useState('user');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [refused, setRefused] = useState(false);
  // « Adresse de la box » and « Identifiant » are pre-filled right (#122).
  const [advanced, setAdvanced] = useState(false);
  const [newBox, setNewBox] = useState<NewBox | null>(null);

  /** What follows a login the box accepted, by either path. */
  const finish = async (target: Target) => {
    await setupMACFilter(); // blocklist on (#56); never in the way
    await connected({ mode: 'router', routerUrl: target.url, pin: target.pin, ...(target.model ? { model: target.model } : {}) });
    // A new password reaches the relays sealed, in the background (#44).
    void relays.afterBoxLogin();
    router.replace('/devices');
  };

  const submit = async () => {
    setBusy(true);
    setError(null);
    setRefused(false);
    try {
      const target = await reach(address);
      if (target === REFUSED) {
        setRefused(true);
        return;
      }
      try {
        await api.login(username, password); // stored encrypted by the core, never echoed back
      } catch (e) {
        if (!(e instanceof ApiError && e.passwordChangeRequired)) throw e;
        // Nothing was stored: the owner sets the new password, a member is
        // sent to the owner and nothing is tried.
        const role = await boxRole();
        core.note('info', `connexion: box neuve, nouveau mot de passe demandé (${role.owner ? 'propriétaire' : 'membre'})`);
        setNewBox({ target, role });
        return;
      }
      await finish(target);
    } catch (e) {
      // The journal keeps why; the screen says what to do about it.
      core.note('error', `connexion: abandon (${message(e)})`);
      // « essayez admin » is said where the identifier is.
      if (e instanceof ApiError && e.authFailed) setAdvanced(true);
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  /** Back to the connection screen with nothing kept, the label's password included. */
  const cancelNewBox = () => {
    setNewBox(null);
    setPassword('');
  };

  const setFirstPassword = async (newPassword: string) => {
    if (!newBox) return;
    // The label's password goes to the core for this one call; the core keeps
    // only the new one.
    await api.firstPassword(username, password, newPassword);
    core.note('info', 'connexion: nouveau mot de passe de la box enregistré');
    setPassword('');
    await finish(newBox.target);
  };

  if (newBox) {
    return (
      <KeyboardAvoidingView style={{ flex: 1 }} behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
        <Stack.Screen options={{ headerShown: true, title: t('boxFirst.title') }} />
        <ScrollView contentContainerStyle={styles.body} keyboardShouldPersistTaps="handled">
          {newBox.role.owner ? (
            <BoxFirstPassword onSubmit={setFirstPassword} onCancel={cancelNewBox} />
          ) : (
            <BoxOwnerNeeded ownerPhone={newBox.role.ownerPhone} onBack={cancelNewBox} />
          )}
        </ScrollView>
      </KeyboardAvoidingView>
    );
  }

  // A refused `user` is often a box whose label says `admin` (Q14).
  const tryAdmin = error instanceof ApiError && error.authFailed && username.trim() === 'user';

  return (
    <KeyboardAvoidingView style={{ flex: 1 }} behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
      <Stack.Screen options={{ headerShown: true, title: t('connect.title') }} />
      <ScrollView contentContainerStyle={styles.body} keyboardShouldPersistTaps="handled">
        <Text variant="bodyMedium">{t('connect.hint')}</Text>
        <PasswordInput
          label={t('connect.password')}
          value={password}
          onChangeText={setPassword}
          testID="connect-password"
        />
        <HelperText type="info">{t('connect.credentialsHint')}</HelperText>
        <Button mode="contained" onPress={submit} loading={busy} disabled={busy || !password} testID="connect-submit">
          {t('connect.submit')}
        </Button>
        {error ? (
          <HelperText type="error" testID="connect-error">
            {tryAdmin ? t('connect.tryAdmin') : errorMessage(error)}
          </HelperText>
        ) : null}
        {refused ? (
          <HelperText type="error" testID="connect-refused">
            {t('connect.refused')}
          </HelperText>
        ) : null}
        <Fold
          title={t('connect.advanced')}
          icon="cog-outline"
          expanded={advanced}
          onToggle={setAdvanced}
          style={styles.fold}
          testID="connect-advanced"
        >
          <View style={styles.advanced}>
            <TextInput
              label={t('connect.address')}
              value={address}
              onChangeText={setAddress}
              autoCapitalize="none"
              autoCorrect={false}
              keyboardType="url"
              testID="connect-address"
            />
            <TextInput
              label={t('connect.username')}
              value={username}
              onChangeText={setUsername}
              autoCapitalize="none"
              autoCorrect={false}
              testID="connect-username"
            />
          </View>
        </Fold>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

const styles = StyleSheet.create({
  body: { padding: 24, gap: 16 },
  fold: { paddingHorizontal: 0 },
  advanced: { gap: 16, paddingTop: 8 },
});
