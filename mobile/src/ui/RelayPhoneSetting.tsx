import { router, useFocusEffect } from 'expo-router';
import { useCallback, useState, type ReactNode } from 'react';
import { List, Switch } from 'react-native-paper';

import { relayPhone } from '../core/relayPhone';
import { relayConsent } from '../features/relayConsent';
import { t } from '../i18n';

// Plus de fonctionnalités > « Ce téléphone sert de relais » (issues #45, #76). On: the relay
// service runs, with its ongoing notification. Turning it on before the
// phone is paired and confirmed opens the relay flow (scan the owner's QR);
// in the demo it always does, and nothing is started. So does turning it on
// before the relay's disclosure was accepted (#53): the flow shows it first,
// and « Annuler » there leaves the switch off.
// `left` replaces the item's icon (the screen's coloured badge).
export function RelayPhoneSetting({ demo, left }: { demo: boolean; left?: () => ReactNode }) {
  const [on, setOn] = useState(false);
  const [busy, setBusy] = useState(false);
  useFocusEffect(
    useCallback(() => {
      setOn(!demo && relayPhone.enabled());
    }, [demo]),
  );

  const toggle = async (next: boolean) => {
    if (busy) return;
    setBusy(true);
    try {
      if (!next) {
        await relayPhone.stop();
        setOn(false);
        return;
      }
      if (
        demo ||
        !relayPhone.supported() ||
        !(await relayConsent.accepted(false)) ||
        !(await relayPhone.enrolled()) ||
        !(await relayPhone.trusted())
      ) {
        router.push('/become-relay');
        return;
      }
      await relayPhone.askPermissions();
      await relayPhone.start();
      setOn(true);
    } catch {
      router.push('/become-relay');
    } finally {
      setBusy(false);
    }
  };

  return (
    <List.Item
      title={t('relayPhone.title')}
      description={on ? t('relayPhone.settingsOn') : t('relayPhone.settingsHint')}
      descriptionNumberOfLines={4}
      left={left ?? ((p) => <List.Icon {...p} icon="cellphone-link" />)}
      onPress={() => router.push('/become-relay')}
      right={() => (
        <Switch
          value={on}
          onValueChange={(v) => void toggle(v)}
          disabled={busy}
          accessibilityLabel={t('relayPhone.title')}
          testID="settings-relay-phone-switch"
        />
      )}
      testID="settings-relay-phone"
    />
  );
}
