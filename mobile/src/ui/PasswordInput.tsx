import { useState } from 'react';
import { TextInput, type TextInputProps } from 'react-native-paper';

import { t } from '../i18n';

/**
 * A password field with an eye (#122, audit H6): the box label's password is
 * long and random, so it can be shown while typing, and hidden again. Hidden
 * by default; the eye's id is the field's plus `-eye`. It only shows what is
 * typed: nothing is kept or sent by it.
 */
export function PasswordInput({
  testID,
  ...props
}: Omit<TextInputProps, 'secureTextEntry' | 'right' | 'testID'> & { testID: string }) {
  const [shown, setShown] = useState(false);
  return (
    <TextInput
      autoCapitalize="none"
      autoCorrect={false}
      {...props}
      testID={testID}
      secureTextEntry={!shown}
      right={
        <TextInput.Icon
          icon={shown ? 'eye-off' : 'eye'}
          onPress={() => setShown((s) => !s)}
          // Showing it does not open the keyboard.
          forceTextInputFocus={false}
          accessibilityLabel={shown ? t('password.hide') : t('password.show')}
          testID={`${testID}-eye`}
        />
      }
    />
  );
}
