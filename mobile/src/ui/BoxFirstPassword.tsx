import * as Linking from 'expo-linking';
import { useState } from 'react';
import { StyleSheet, View } from 'react-native';
import { Button, HelperText, ProgressBar, Text, TextInput } from 'react-native-paper';

import { ApiError } from '../core/client';
import { errorMessage, t } from '../i18n';
import { boxPasswordMissing, boxPasswordStrength, type BoxPasswordStrength } from '../lib/boxPassword';
import { useSemantic } from './theme';

// A new box demands a new admin password after the label's login (#91, #93).
// The owner types it twice; the box's rule blocks, the gauge only advises.
// The label's password is the connection screen's, kept in memory for the
// one call and never stored; « Annuler » goes back to that screen.

type Props = {
  /** Sets the new password on the box (api.firstPassword), then goes on. */
  onSubmit: (newPassword: string) => Promise<void>;
  onCancel: () => void;
};

const GAUGE: Record<BoxPasswordStrength, number> = { weak: 0.33, medium: 0.66, strong: 1 };

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

export function BoxFirstPassword({ onSubmit, onCancel }: Props) {
  const tones = useSemantic();
  const [password, setPassword] = useState('');
  const [confirmation, setConfirmation] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<{ message: string; reason?: string } | null>(null);

  const missing = boxPasswordMissing(password);
  const strength = boxPasswordStrength(password);
  const mismatch = confirmation !== '' && confirmation !== password;
  const ready = password !== '' && missing.length === 0 && confirmation === password;
  const tone = strength === 'strong' ? tones.connected : strength === 'medium' ? tones.attention : tones.blocked;

  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      await onSubmit(password);
    } catch (e) {
      setError(refusal(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <View style={styles.gap} testID="box-first-password">
      <Text variant="titleMedium" accessibilityRole="header">
        {t('boxFirst.title')}
      </Text>
      <Text variant="bodyMedium">{t('boxFirst.intro')}</Text>
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
      <Button mode="contained" onPress={submit} loading={busy} disabled={busy || !ready} testID="box-first-submit">
        {t('boxFirst.submit')}
      </Button>
      <Button mode="outlined" onPress={onCancel} disabled={busy} testID="box-first-cancel">
        {t('common.cancel')}
      </Button>
      {error ? (
        <View testID="box-first-error">
          <HelperText type="error">{error.message}</HelperText>
          {error.reason ? <HelperText type="error">{t('boxFirst.rejectedReason', { reason: error.reason })}</HelperText> : null}
        </View>
      ) : null}
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
});
