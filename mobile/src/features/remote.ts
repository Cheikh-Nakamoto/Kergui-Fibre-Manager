import * as SecureStore from 'expo-secure-store';

import { RemoteCommandError, remoteCore, type RemoteRelay, type VerifiedResult } from '../core/remote';
import type { Device } from '../core/types';
import { t } from '../i18n';
import { useAccount } from '../licensing/account';
import { HubError, type HubBundle, type HubRelayStatus } from '../licensing/api';
import { config } from '../licensing/config';
import { hub } from '../licensing/hub';
import { withHousehold } from '../licensing/household';

// Remote mode (issue #46, RFC-005 design rule 1). A phone signed in to a
// household whose owner-signed bundle names a relay goes through the hub for
// the device list and for block / unblock — even at home, on the box's own
// Wi-Fi: the box keeps one admin session, and a phone logging in beside the
// relay would knock it off. The hub only carries; the Go core verifies the
// relay's signature over every list and every result (src/core/remote.ts).
//
// When the hub or the relay cannot be reached, the app says so. It never
// falls back to logging in to the box itself: that would be the second
// session rule 1 forbids, and the household would lose its remote access
// without being told.

/** The household this phone reaches through its relay. */
export type RemoteHousehold = {
  tenantId: string;
  /** The hub's bundle, the one the core checked; null when the hub could not be reached. */
  bundle: HubBundle | null;
  relays: RemoteRelay[];
  /** Why there is no bundle. */
  error?: unknown;
};

export { RemoteCommandError };

/** What the Devices tab shows in remote mode. */
export type RemoteDevices = {
  devices: Device[];
  /** When the relay read the box; null before it ever pushed a list. */
  asOf: string | null;
  relay: HubRelayStatus;
};

/** A list older than this is shown as possibly out of date (the relay pushes every minute). */
export const STALE_AFTER_S = 180;

/** How old the relay's list is, and whether to warn about it. */
export function freshness(asOf: string | null, nowMs: number): { ageS: number | null; stale: boolean } {
  if (!asOf) return { ageS: null, stale: true };
  const at = Date.parse(asOf);
  if (Number.isNaN(at)) return { ageS: null, stale: true };
  const ageS = Math.max(0, Math.round((nowMs - at) / 1000));
  return { ageS, stale: ageS > STALE_AFTER_S };
}

// Where the household's remote mode is remembered, so a hub that cannot be
// reached at start-up does not turn the phone back into a second session.
const REMEMBER_KEY = 'kergui.remote.tenant';

async function remember(tenantId: string | null) {
  try {
    if (tenantId) await SecureStore.setItemAsync(REMEMBER_KEY, tenantId);
    else await SecureStore.deleteItemAsync(REMEMBER_KEY);
  } catch {
    // A keystore that fails: the next detection decides again.
  }
}

async function remembered(): Promise<string | null> {
  try {
    return await SecureStore.getItemAsync(REMEMBER_KEY);
  } catch {
    return null;
  }
}

/**
 * The household this phone goes through the hub for, or null for local mode:
 * no hub in this build, not signed in, no bundle, a bundle this phone's
 * owner key did not sign (another member's phone: delegations are #47), or
 * no relay in it. A hub that cannot be reached keeps the last answer.
 */
export async function detectRemote(): Promise<RemoteHousehold | null> {
  if (!config.hubUrl || !useAccount.getState().account) return null;
  try {
    const found = await withHousehold(async (tok, tenantId): Promise<RemoteHousehold | null> => {
      const bundle = await hub.trustBundle(tok, tenantId);
      if (!bundle) return null;
      const mine = await remoteCore.relays(tenantId, bundle);
      return mine.owner && mine.relays.length ? { tenantId, bundle, relays: mine.relays } : null;
    });
    await remember(found?.tenantId ?? null);
    return found;
  } catch (e) {
    // The hub cannot be reached, or answered what the core would not
    // believe: a phone that was in remote mode stays in it and shows why,
    // rather than logging in to the box beside the relay.
    const tenantId = await remembered();
    if (tenantId) return { tenantId, bundle: null, relays: [], error: e };
    if (e instanceof HubError && (e.offline || e.status >= 500)) return null;
    throw e;
  }
}

/** The hub could not be reached when remote mode was decided: nothing can be verified. */
function needBundle(h: RemoteHousehold): HubBundle {
  if (!h.bundle) throw h.error ?? new HubError(0, 'NETWORK', 'the hub could not be reached');
  return h.bundle;
}

/** The device list the relay signed, with its as_of and the relay's state. */
export async function remoteDevices(h: RemoteHousehold): Promise<RemoteDevices> {
  const bundle = needBundle(h);
  return withHousehold(async (tok, tenantId) => {
    const [served, relay] = await Promise.all([hub.routerDevices(tok, tenantId), hub.relayStatus(tok, tenantId)]);
    if (!served) return { devices: [], asOf: null, relay };
    const v = await remoteCore.devices(tenantId, bundle, served.snapshot);
    return { devices: v.devices, asOf: v.asOf, relay };
  });
}

export type CommandOptions = {
  /** The box serial the app kept (useEntitlement's boxId): the gate's box. */
  boxId: string;
  pollMs?: number;
  now?: () => number;
  sleep?: (ms: number) => Promise<void>;
};

const defaultSleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

/**
 * Blocks or unblocks a device through the relay: refused up front when the
 * relay is not heard (nothing is queued for nobody), signed in the core with
 * the owner's key after its paid-action gate, carried by the hub, followed
 * until the relay answers or the command expires, and believed only once the
 * core has checked the relay's signature over the answer.
 */
export async function remoteSetBlocked(h: RemoteHousehold, mac: string, blocked: boolean, opts: CommandOptions): Promise<VerifiedResult> {
  const bundle = needBundle(h);
  const now = opts.now ?? Date.now;
  const sleep = opts.sleep ?? defaultSleep;
  const pollMs = opts.pollMs ?? 1500;
  return withHousehold(async (tok, tenantId) => {
    const relay = await hub.relayStatus(tok, tenantId);
    if (relay.state === 'none') throw new RemoteCommandError('relay_none');
    if (relay.state === 'offline') throw new RemoteCommandError('relay_offline', relay.lastSeenAt);

    const action = blocked ? 'block' : 'unblock';
    const signed = await remoteCore.sign(tenantId, mac, action, opts.boxId);
    let cmd = await hub.deviceCommand(tok, tenantId, signed.mac, action, { payload: signed.payload, sig: signed.sig });
    const deadline = Date.parse(signed.expiresAt);
    while (cmd.status !== 'done') {
      // The hub marks it expired within half a minute; the phone's own clock
      // does not wait for that.
      if (cmd.status === 'expired' || now() > deadline) throw new RemoteCommandError('expired');
      await sleep(pollMs);
      cmd = await hub.command(tok, tenantId, signed.cmdId);
    }
    const res = await remoteCore.verify(tenantId, signed.cmdId, bundle, cmd.result);
    if (res.status === 403) throw new RemoteCommandError('refused');
    if (res.status >= 400) throw new RemoteCommandError('box', undefined, res.status);
    return res;
  });
}

/** "moins d'une minute", "5 min", "2 h": an age as the banner says it. */
export function ageWords(ageS: number): string {
  if (ageS < 60) return t('remote.age.now');
  const min = Math.floor(ageS / 60);
  if (min < 60) return t('remote.age.min', { n: min });
  const h = Math.floor(min / 60);
  return h < 48 ? t('remote.age.h', { n: h }) : t('remote.age.days', { n: Math.floor(h / 24) });
}
