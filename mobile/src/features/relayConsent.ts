import * as SecureStore from 'expo-secure-store';

// Play's prominent disclosure for the phone relay (#53): the person reads
// what the relay sends to the Kergui server, the permanent notification and
// how to stop it, and accepts, before this phone first becomes a relay. The
// answer is remembered on the phone so it is not asked again. The demo asks
// too, but its answer lives in memory only: accepting in the demo does not
// count for a real box.

/** SecureStore key of the accepted disclosure; bump the version when its text changes what is sent. */
export const RELAY_CONSENT_KEY = 'kergui.relayDisclosure.v1';

let demoAccepted = false;

export const relayConsent = {
  /** Whether the disclosure was accepted (in the demo: during this run of the app). */
  async accepted(demo: boolean): Promise<boolean> {
    if (demo) return demoAccepted;
    try {
      return (await SecureStore.getItemAsync(RELAY_CONSENT_KEY)) === '1';
    } catch {
      return false;
    }
  },
  async accept(demo: boolean): Promise<void> {
    if (demo) {
      demoAccepted = true;
      return;
    }
    await SecureStore.setItemAsync(RELAY_CONSENT_KEY, '1');
  },
  /** Tests only: the demo's answer is forgotten, as when the app restarts. */
  forgetDemo(): void {
    demoAccepted = false;
  },
};
