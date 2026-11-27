import * as Linking from 'expo-linking';
import { useState } from 'react';
import { Platform, Share, StyleSheet, View } from 'react-native';
import { Button, Checkbox, HelperText, ProgressBar, Surface, Text, TextInput } from 'react-native-paper';

import { ApiError } from '../core/client';
import { errorMessage, t } from '../i18n';
import { boxPasswordMissing, boxPasswordStrength, suggestBoxPassword, type BoxPasswordStrength } from '../lib/boxPassword';
import { useSemantic } from './theme';

// A new box demands a new admin password after the label's login (#91, #93).
// The default is a suggested password of words and digits (#116); the owner
// may type their own twice instead, where the box's rule blocks and the
// gauge only advises. Before it goes to the box, one mandatory step has the
// owner keep it off the phone (audit C4): written down, sent on WhatsApp to
// someone close, or shown big for a photo. A one-phone household that loses
// the phone would otherwise have to reset the box. The password is never
// logged and never sent to the hub; only the share the owner picks carries it.
// The label's password is the connection screen's, kept in memory for the
// one call and never stored; « Annuler » goes back to that screen.

type Props = {
  /** Sets the new password on the box (api.firstPassword), then goes on. */
  onSubmit: (newPassword: string) => Promise<void>;
  onCancel: () => void;
};

const GAUGE: Record<BoxPasswordStrength, number> = { weak: 0.33, medium: 0.66, strong: 1 };

const MONO = Platform.select({ ios: 'Menlo', default: 'monospace' });

/** Why the core or the box refused it, in words; the box's own text when it gave one. */
function refusal(e: unknown): { message: string; reason?: string } {
  if (e instanceof ApiError && e.code === 'weak_password') return { message: t('boxFirst.weak') };
  if (e instanceof ApiError && e.code === 'password_rejected') {
    // "…the router refused the new password: <the box's message>"
    const reason = /refused the new password:\s*(.+)$/.exec(e.message)?.[1]?.trim();
    return { message: t('boxFirst.rejected'), reason: reason || undefined };
  }
  return { message: errorMessage(e) };
}

/** How the owner kept the password off the phone; any one unlocks the step. */
type Kept = { written: boolean; shared: boolean; photo: boolean };
const NOT_KEPT: Kept = { written: false, shared: false, photo: false };

/**
 * Sends the message through WhatsApp itself (never a web page that would see
 * the password), or the phone's share sheet when WhatsApp is not there.
 * True when it went out.
 */
async function sendToSomeone(password: string): Promise<boolean> {
  const message = t('boxFirst.shareText', { password });
  try {
    await Linking.openURL(`whatsapp://send?text=${encodeURIComponent(message)}`);
    return true;
  } catch {
    try {
      const r = await Share.share({ message });
      return r.action !== Share.dismissedAction;
    } catch {
      return false;
    }
  }
}

export function BoxFirstPassword({ onSubmit, onCancel }: Props) {
  const tones = useSemantic();
  const [suggested, setSuggested] = useState(() => suggestBoxPassword());
  const [own, setOwn] = useState(false);
  const [password, setPassword] = useState('');
  const [confirmation, setConfirmation] = useState('');
  const [step, setStep] = useState<'choose' | 'keep'>('choose');
  const [kept, setKept] = useState<Kept>(NOT_KEPT);
  const [big, setBig] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<{ message: string; reason?: string } | null>(null);

  const chosen = own ? password : suggested;
  const missing = boxPasswordMissing(password);
  const strength = boxPasswordStrength(password);
  const mismatch = confirmation !== '' && confirmation !== password;
  const ready = own ? password !== '' && missing.length === 0 && confirmation === password : true;
  const isKept = kept.written || kept.shared || kept.photo;
  const tone = strength === 'strong' ? tones.connected : strength === 'medium' ? tones.attention : tones.blocked;

  const chooseOwn = (on: boolean) => {
    setOwn(on);
    setPassword('');
    setConfirmation('');
  };
  const back = () => {
    setStep('choose');
    setKept(NOT_KEPT);
    setBig(false);
    setError(null);
  };
  const photoTaken = () => {
    setKept((k) => ({ ...k, photo: true }));
    setBig(false);
  };
  const share = async () => {
    if (await sendToSomeone(chosen)) setKept((k) => ({ ...k, shared: true }));
  };

  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      await onSubmit(chosen);
    } catch (e) {
      setError(refusal(e));
    } finally {
      setBusy(false);
    }
  };

  // The same screen for both steps: its testID says a new box is being set up.
  if (step === 'keep') {
    return (
      <View style={styles.gap} testID="box-first-password">
        <View style={styles.gap} testID="box-first-keep">
          <Text variant="titleMedium" accessibilityRole="header">
            {t('boxFirst.keepTitle')}
          </Text>
          <Text variant="bodyMedium">{t('boxFirst.keep')}</Text>
          {big ? (
            <Surface style={styles.big} elevation={1} testID="box-first-big" accessibilityLabel={chosen.split('').join(' ')}>
              <View style={styles.bigRow}>
                {chosen.split('').map((c, i) => (
                  <Text key={i} style={styles.bigChar} testID="box-first-big-char">
                    {c}
                  </Text>
                ))}
              </View>
              <Button mode="contained" onPress={photoTaken} testID="box-first-photo-done">
                {t('boxFirst.photoDone')}
              </Button>
            </Surface>
          ) : (
            <Text variant="headlineSmall" style={styles.mono} selectable testID="box-first-keep-password">
              {chosen}
            </Text>
          )}
          <Checkbox.Item
            label={t('boxFirst.written')}
            status={kept.written ? 'checked' : 'unchecked'}
            onPress={() => setKept((k) => ({ ...k, written: !k.written }))}
            position="leading"
            labelVariant="bodyMedium"
            disabled={busy}
            testID="box-first-written"
          />
          <Button mode="outlined" icon="whatsapp" onPress={() => void share()} disabled={busy} testID="box-first-share">
            {kept.shared ? `${t('boxFirst.share')} · ${t('boxFirst.shared')}` : t('boxFirst.share')}
          </Button>
          <Button mode="outlined" icon="camera" onPress={() => setBig(true)} disabled={busy || big} testID="box-first-photo">
            {kept.photo ? `${t('boxFirst.photo')} · ${t('boxFirst.photoTaken')}` : t('boxFirst.photo')}
          </Button>
          {!isKept ? (
            <HelperText type="info" testID="box-first-keep-needed">
              {t('boxFirst.keepNeeded')}
            </HelperText>
          ) : null}
          <Button mode="contained" onPress={submit} loading={busy} disabled={busy || !isKept} testID="box-first-submit">
            {t('boxFirst.submit')}
          </Button>
          <Button mode="text" onPress={back} disabled={busy} testID="box-first-back">
            {t('boxFirst.change')}
          </Button>
          {error ? (
            <View testID="box-first-error">
              <HelperText type="error">{error.message}</HelperText>
              {error.reason ? <HelperText type="error">{t('boxFirst.rejectedReason', { reason: error.reason })}</HelperText> : null}
            </View>
          ) : null}
        </View>
      </View>
    );
  }

  return (
    <View style={styles.gap} testID="box-first-password">
      <Text variant="titleMedium" accessibilityRole="header">
        {t('boxFirst.title')}
      </Text>
      <Text variant="bodyMedium">{t('boxFirst.intro')}</Text>
      {own ? (
        <>
          <Text variant="bodySmall" style={styles.hint}>
            {t('boxFirst.rules')}
          </Text>
          <TextInput
            label={t('boxFirst.new')}
            value={password}
            onChangeText={setPassword}
            secureTextEntry
            autoCapitalize="none"
            autoCorrect={false}
            testID="box-first-new"
          />
          {password ? (
            <View style={styles.gauge} testID="box-first-strength">
              <ProgressBar progress={GAUGE[strength]} color={tone.color} />
              <Text variant="bodySmall">{t('boxFirst.strength', { level: t(`boxFirst.strength.${strength}`) })}</Text>
            </View>
          ) : null}
          {password && missing.length > 0 ? (
            <HelperText type="info" testID="box-first-missing">
              {missing[0] === 'ascii'
                ? t('boxFirst.rule.ascii')
                : t('boxFirst.missing', { list: missing.map((m) => t(`boxFirst.rule.${m}` as 'boxFirst.rule.length')).join(', ') })}
            </HelperText>
          ) : null}
          <TextInput
            label={t('boxFirst.confirm')}
            value={confirmation}
            onChangeText={setConfirmation}
            secureTextEntry
            autoCapitalize="none"
            autoCorrect={false}
            testID="box-first-confirm"
          />
          {mismatch ? (
            <HelperText type="error" testID="box-first-mismatch">
              {t('boxFirst.mismatch')}
            </HelperText>
          ) : null}
          <Button mode="text" onPress={() => chooseOwn(false)} testID="box-first-suggest">
            {t('boxFirst.useSuggested')}
          </Button>
        </>
      ) : (
        <>
          <Text variant="labelLarge">{t('boxFirst.suggested')}</Text>
          <Text variant="headlineSmall" style={styles.mono} selectable testID="box-first-suggested">
            {suggested}
          </Text>
          <Text variant="bodySmall" style={styles.hint}>
            {t('boxFirst.suggestedHint')}
          </Text>
          <Button mode="outlined" icon="refresh" onPress={() => setSuggested(suggestBoxPassword())} testID="box-first-another">
            {t('boxFirst.another')}
          </Button>
          <Button mode="text" onPress={() => chooseOwn(true)} testID="box-first-own">
            {t('boxFirst.own')}
          </Button>
        </>
      )}
      <Button mode="contained" onPress={() => setStep('keep')} disabled={!ready} testID="box-first-next">
        {t('boxFirst.next')}
      </Button>
      <Button mode="outlined" onPress={onCancel} testID="box-first-cancel">
        {t('common.cancel')}
      </Button>
    </View>
  );
}

/**
 * A member's phone meets a new box (#93): only the owner sets its first
 * password, so nothing is tried; WhatsApp tells the owner, when the hub
 * gave their number.
 */
export function BoxOwnerNeeded({ ownerPhone, onBack }: { ownerPhone: string | null; onBack: () => void }) {
  const warn = () =>
    void Linking.openURL(`https://wa.me/${ownerPhone}?text=${encodeURIComponent(t('boxFirst.whatsappText'))}`).catch(
      () => undefined,
    );
  return (
    <View style={styles.gap} testID="box-owner-needed">
      <Text variant="titleMedium" accessibilityRole="header">
        {t('boxFirst.member')}
      </Text>
      <Text variant="bodyMedium">{t('boxFirst.memberHint')}</Text>
      {ownerPhone ? (
        <Button mode="contained" icon="whatsapp" onPress={warn} testID="box-owner-whatsapp">
          {t('boxFirst.whatsapp')}
        </Button>
      ) : null}
      <Button mode="outlined" onPress={onBack} testID="box-owner-back">
        {t('boxFirst.back')}
      </Button>
    </View>
  );
}

const styles = StyleSheet.create({
  gap: { gap: 12 },
  gauge: { gap: 4 },
  hint: { opacity: 0.8 },
  mono: { fontFamily: MONO },
  // Big, monospaced, one box per character: a photo reads every one.
  big: { padding: 16, gap: 16, borderRadius: 12 },
  bigRow: { flexDirection: 'row', flexWrap: 'wrap', justifyContent: 'center', gap: 6 },
  bigChar: { fontFamily: MONO, fontSize: 36, lineHeight: 48, minWidth: 30, textAlign: 'center' },
});
