import * as Linking from 'expo-linking';
import { Share } from 'react-native';

/**
 * Sends a message through WhatsApp itself (never a web page such as wa.me,
 * which would see a password in it), or the phone's share sheet when
 * WhatsApp is not there. True when it went out.
 */
export async function sendOnWhatsApp(message: string): Promise<boolean> {
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
