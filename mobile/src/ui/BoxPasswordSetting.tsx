import * as Clipboard from 'expo-clipboard';
import * as LocalAuthentication from 'expo-local-authentication';
import { useFocusEffect } from 'expo-router';
import { useCallback, useEffect, useState } from 'react';
import { View } from 'react-native';
import { Button, HelperText, List, Text } from 'react-native-paper';

import { core, type BoxPassword } from '../core/client';
import { errorMessage, t } from '../i18n';
import { boxRole } from '../session/boxOwner';

// Réglages › « Mot de passe de la box » (#93): the owner may need it for a
// technician. It lives in the core's vault; it is read only after the
// phone's own unlock (biometrics or the phone's code), kept on screen until
// « Masquer » or leaving Réglages, and never written anywhere else. A member
// does not see the item.

/** A phone with no code at all cannot prove its owner is the one holding it. */
const NO_LOCK = new Set(['not_enrolled', 'passcode_not_set', 'not_available']);

export function BoxPasswordSetting() {
  const [owner, setOwner] = useState(false);
  const [shown, setShown] = useState<BoxPassword | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    void boxRole().then((r) => live && setOwner(r.owner));
    return () => {
      live = false;
    };
  }, []);

  // Leaving Réglages hides it again: the next look asks for the unlock again.
  useFocusEffect(useCallback(() => () => setShown(null), []));

  if (!owner) return null;

  const reveal = async () => {
    setNote(null);
    setError(null);
    try {
      const unlock = await LocalAuthentication.authenticateAsync({
        promptMessage: t('settings.boxPasswordPrompt'),
        cancelLabel: t('common.cancel'),
      });
      if (!unlock.success) {
        // Cancelling is an answer, not an error; a phone without a lock is told why.
        if (NO_LOCK.has(unlock.error)) setError(t('settings.boxPasswordNoLock'));
        return;
      }
      setShown(await core.boxPassword());
    } catch (e) {
      setError(errorMessage(e));
    }
  };

  const copy = async () => {
    if (!shown) return;
    await Clipboard.setStringAsync(shown.password);
    setNote(t('settings.boxPasswordCopied'));
  };

  return (
    <View>
      <List.Item
        title={t('settings.boxPassword')}
        description={t('settings.boxPasswordHint')}
        descriptionNumberOfLines={3}
        left={(p) => <List.Icon {...p} icon="form-textbox-password" />}
        right={(p) => <List.Icon {...p} icon={shown ? 'eye-outline' : 'lock-outline'} />}
        onPress={shown ? undefined : reveal}
        testID="settings-box-password"
      />
      {shown ? (
        shown.stored ? (
          <View style={{ paddingHorizontal: 16, gap: 8 }} testID="settings-box-password-shown">
            <Text variant="bodyMedium" selectable>
              {t('settings.boxPasswordUsername', { username: shown.username })}
            </Text>
            <Text variant="bodyMedium" selectable testID="settings-box-password-value">
              {t('settings.boxPasswordValue', { password: shown.password })}
            </Text>
            <View style={{ flexDirection: 'row', gap: 8 }}>
              <Button mode="outlined" icon="content-copy" onPress={copy} testID="settings-box-password-copy">
                {t('settings.boxPasswordCopy')}
              </Button>
              <Button mode="text" onPress={() => setShown(null)} testID="settings-box-password-hide">
                {t('settings.boxPasswordHide')}
              </Button>
            </View>
          </View>
        ) : (
          <HelperText type="info">{t('settings.boxPasswordNone')}</HelperText>
        )
      ) : null}
      {note ? <HelperText type="info">{note}</HelperText> : null}
      {error ? <HelperText type="error">{error}</HelperText> : null}
    </View>
  );
}
