import * as SecureStore from 'expo-secure-store';

import { disableAlerts, forgetKnown } from '../alerts/alerts';
import { useInbox } from '../alerts/inbox';
import { useSession } from '../session/store';
import { useAccount } from './account';
import { HubError } from './api';
import { useEntitlement } from './entitlement';
import { hub } from './hub';

// « Supprimer mon compte » (#37, Google Play's account-deletion policy).
// The hub erases the account and everything it keeps for it
// (DELETE /licensing/v1/account with the kgs_ session: rights, trial,
// complaints, owner phones, and the household when nobody else is in it).
// Then the phone forgets the account, both sessions, the box and what it
// cached from the hub, and the app is back on the welcome screen.

/** What the person types to confirm, after the first alert. */
export const DELETE_WORD = 'SUPPRIMER';

/** The demo has no account: nothing to delete. */
export class DemoAccountError extends Error {
  constructor() {
    super('the demo has no account');
    this.name = 'DemoAccountError';
  }
}

// What the phone keeps from the hub or about the household, beside the
// account and its tokens (which their own stores clear).
const CACHED_KEYS = ['kergui.hub.entitlement', 'kergui.hub.backup', 'kergui.remote.tenant', 'kergui.remote.houseCut', 'kergui.inbox'];

export async function deleteAccount(): Promise<void> {
  if (useSession.getState().session?.mode === 'demo') throw new DemoAccountError();
  try {
    await hub.deleteAccount();
  } catch (e) {
    // The session was already refused (the account deleted from another
    // phone): there is nothing left on the hub, the phone still forgets.
    if (!(e instanceof HubError && e.sessionInvalid)) throw e;
  }
  await wipePhone();
}

/** Empties the phone of the account, the box and the caches. */
async function wipePhone(): Promise<void> {
  await disableAlerts().catch(() => undefined);
  await forgetKnown().catch(() => undefined);
  await useAccount.getState().invalidate();
  await useEntitlement.getState().forgetBox();
  useInbox.getState().clear();
  for (const k of CACHED_KEYS) {
    try {
      await SecureStore.deleteItemAsync(k);
    } catch {
      // Nothing stored, or a keystore that fails: gone either way.
    }
  }
  await useSession.getState().signOut();
}
