import * as SecureStore from 'expo-secure-store';

import { RemoteCommandError, remoteCore, type RemoteRelay, type VerifiedResult } from '../core/remote';
import type { Device, DeviceLabel, Usage, WiFiSSID, WiFiSSIDChange } from '../core/types';
import { t } from '../i18n';
import { useAccount } from '../licensing/account';
import { HubError, type HubBundle, type HubCommand, type HubRelayStatus } from '../licensing/api';
import { config } from '../licensing/config';
import { hub } from '../licensing/hub';
import { withHousehold } from '../licensing/household';
import { renewDue } from './delegations';

// Remote mode (issue #46, RFC-005 design rule 1). A phone signed in to a
// household whose owner-signed bundle names a relay goes through the hub for
// the device list and for block / unblock. The hub only carries; the Go core
// verifies the relay's signature over every list and every result
// (src/core/remote.ts).
//
// « Proprio prioritaire » (#94): the owner's phone at home, on the box's own
// network, talks to the box directly instead (features/atHome.ts,
// useRemoteMode). The box keeps one admin session; the owner's wins, and the
// relay yields to it without going offline ("paused"), until the owner
// leaves or a command the owner signs reaches it through the hub. A member's
// phone always goes through the relay.
//
// Away from home, when the hub or the relay cannot be reached, the app says
// so. It never falls back to logging in to the box itself: from outside the
// box cannot be reached anyway, and the household is told why.

/** The household this phone reaches through its relay. */
export type RemoteHousehold = {
  tenantId: string;
  /** The hub's bundle, the one the core checked; null when the hub could not be reached. */
  bundle: HubBundle | null;
  relays: RemoteRelay[];
  /**
   * Set on a member's phone (issue #47): its commands are signed with its
   * own key, under the owner's delegation. Absent on the owner's phone.
   */
  member?: { control: boolean; ownerOnly: boolean };
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
  /** The relay that signed the list; null before any relay pushed one. */
  agentId?: string | null;
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
 * no hub in this build, not signed in, no bundle, a bundle that neither this
 * phone's owner key signed nor delegates its member key (issue #47), or no
 * relay in it. On the owner's phone it also renews the members' delegations
 * that end within a week. A hub that cannot be reached keeps the last answer.
 */
export async function detectRemote(): Promise<RemoteHousehold | null> {
  if (!config.hubUrl || !useAccount.getState().account) return null;
  try {
    const found = await withHousehold(async (tok, tenantId): Promise<RemoteHousehold | null> => {
      let bundle = await hub.trustBundle(tok, tenantId);
      if (!bundle) return null;
      const mine = await remoteCore.relays(tenantId, bundle);
      if (mine.owner) {
        if (mine.delegations.length) bundle = await renewDue(tok, tenantId, bundle);
        return mine.relays.length ? { tenantId, bundle, relays: mine.relays } : null;
      }
      const m = mine.member;
      if (m?.state === 'active' && mine.relays.length) {
        return { tenantId, bundle, relays: mine.relays, member: { control: m.control, ownerOnly: mine.ownerOnly } };
      }
      return null;
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
    if (!served) return { devices: [], asOf: null, relay, agentId: null };
    const v = await remoteCore.devices(tenantId, bundle, served.snapshot);
    return { devices: v.devices, asOf: v.asOf, relay, agentId: v.agentId };
  });
}

export type CommandOptions = {
  /** The box serial the app kept (useEntitlement's boxId): the gate's box. */
  boxId: string;
  pollMs?: number;
  /**
   * Household data, not box work (#95: the names): sent while the relay is
   * paused for the owner at home, by a member too.
   */
  whilePaused?: boolean;
  now?: () => number;
  sleep?: (ms: number) => Promise<void>;
};

const defaultSleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

/**
 * Blocks or unblocks a device through the relay: refused up front when the
 * relay is not heard (nothing is queued for nobody), signed in the core with
 * the owner's key (a member's own key, under its delegation) after its
 * paid-action gate, carried by the hub, followed
 * until the relay answers or the command expires, and believed only once the
 * core has checked the relay's signature over the answer.
 */
export async function remoteSetBlocked(h: RemoteHousehold, mac: string, blocked: boolean, opts: CommandOptions): Promise<VerifiedResult> {
  const bundle = needBundle(h);
  if (h.member && !h.member.control) throw new RemoteCommandError(h.member.ownerOnly ? 'owner_only' : 'not_delegated');
  return withHousehold(async (tok, tenantId) => {
    await requireRelay(tok, tenantId, !!h.member);
    const action = blocked ? 'block' : 'unblock';
    const signed = h.member
      ? await remoteCore.signMember(tenantId, bundle, mac, action, opts.boxId)
      : await remoteCore.sign(tenantId, mac, action, opts.boxId);
    const cmd = await hub.deviceCommand(tok, tenantId, signed.mac, action, { payload: signed.payload, sig: signed.sig });
    return follow(tok, tenantId, bundle, signed.cmdId, signed.expiresAt, cmd, opts);
  });
}

/**
 * Refuses up front when the hub does not hear the relay: nothing is queued for
 * nobody. A paused relay (#94) runs the owner's command, which takes the box
 * back; a member's waits until the owner has left home.
 */
async function requireRelay(tok: string, tenantId: string, member: boolean, whilePaused = false): Promise<HubRelayStatus> {
  const relay = await hub.relayStatus(tok, tenantId);
  if (relay.state === 'none') throw new RemoteCommandError('relay_none');
  if (relay.state === 'offline') throw new RemoteCommandError('relay_offline', relay.lastSeenAt);
  if (relay.state === 'paused' && member && !whilePaused) throw new RemoteCommandError('relay_paused');
  return relay;
}

/**
 * Follows a queued command until the relay answers or it expires, and
 * believes the answer only once the core has checked the relay's signature.
 */
async function follow(
  tok: string,
  tenantId: string,
  bundle: HubBundle,
  cmdId: string,
  expiresAt: string,
  first: HubCommand,
  opts: CommandOptions,
): Promise<VerifiedResult> {
  const now = opts.now ?? Date.now;
  const sleep = opts.sleep ?? defaultSleep;
  const pollMs = opts.pollMs ?? 1500;
  const deadline = Date.parse(expiresAt);
  let cmd = first;
  while (cmd.status !== 'done') {
    // The hub marks it expired within half a minute; the phone's own clock
    // does not wait for that.
    if (cmd.status === 'expired' || now() > deadline) throw new RemoteCommandError('expired');
    await sleep(pollMs);
    cmd = await hub.command(tok, tenantId, cmdId);
  }
  const res = await remoteCore.verify(tenantId, cmdId, bundle, cmd.result);
  if (res.status === 501) throw new RemoteCommandError('unverified');
  if (res.status === 403) throw new RemoteCommandError('refused');
  // The relay paused for the owner at home in the meantime (#94).
  if (res.status === 503 && (res.body as { code?: unknown } | undefined)?.code === 'owner_at_home') {
    throw new RemoteCommandError('relay_paused');
  }
  if (res.status >= 400) throw new RemoteCommandError('box', undefined, res.status);
  return res;
}

/**
 * Any request of the relay's allow-list (issue #50): signed in the core
 * after its paid-action gate, carried by POST router/commands, believed once
 * the relay's signature checks. A read needs only a view delegation on a
 * member's phone; a write needs control.
 */
export async function remoteRequest(
  h: RemoteHousehold,
  method: 'GET' | 'POST' | 'PATCH',
  path: string,
  body: unknown,
  opts: CommandOptions,
): Promise<VerifiedResult> {
  const bundle = needBundle(h);
  if (method !== 'GET' && h.member && !h.member.control) throw new RemoteCommandError(h.member.ownerOnly ? 'owner_only' : 'not_delegated');
  return withHousehold(async (tok, tenantId) => {
    await requireRelay(tok, tenantId, !!h.member, opts.whilePaused);
    const signed = await remoteCore.request(tenantId, bundle, method, path, body, opts.boxId);
    const cmd = await hub.routerCommand(tok, tenantId, { payload: signed.payload, sig: signed.sig, body: signed.body });
    return follow(tok, tenantId, bundle, signed.cmdId, signed.expiresAt, cmd, opts);
  });
}

/**
 * Sends names and notes to the relay (#95), signed like any command: the
 * relay keeps each one unless it already has a later edit. Returns the MACs
 * whose label it changed.
 */
export async function remoteLabels(h: RemoteHousehold, labels: DeviceLabel[], opts: CommandOptions): Promise<string[]> {
  const r = await remoteRequest(h, 'POST', '/api/labels', { labels }, { ...opts, whilePaused: true });
  const changed = (r.body as { changed?: unknown } | undefined)?.changed;
  return Array.isArray(changed) ? changed.filter((m): m is string => typeof m === 'string') : [];
}

/** What a remote screen shows: the relay's answer, and when the relay read the box (as_of). */
export type RemoteRead<T> = { data: T; asOf: string };

/** This month's consumption per device, as the relay measured it just now. */
export async function remoteUsage(h: RemoteHousehold, opts: CommandOptions): Promise<RemoteRead<Usage[]>> {
  const r = await remoteRequest(h, 'GET', '/api/usage', undefined, opts);
  return { data: Array.isArray(r.body) ? (r.body as Usage[]) : [], asOf: r.executedAt };
}

/** The household's Wi-Fi networks, read by the relay. */
export async function remoteSSIDs(h: RemoteHousehold, opts: CommandOptions): Promise<RemoteRead<WiFiSSID[]>> {
  const r = await remoteRequest(h, 'GET', '/api/wifi/ssids', undefined, opts);
  return { data: Array.isArray(r.body) ? (r.body as WiFiSSID[]) : [], asOf: r.executedAt };
}

/** Turns a network on / off or renames it; a password never goes through the relay. */
export async function remoteUpdateSSID(h: RemoteHousehold, id: string, change: WiFiSSIDChange, opts: CommandOptions): Promise<VerifiedResult> {
  if (change.password !== undefined || change.hidden !== undefined) throw new RemoteCommandError('local_only');
  return remoteRequest(h, 'PATCH', `/api/wifi/ssids/${id}`, change, opts);
}

/** The Wi-Fi deny list the relay read, lower case; null when this model keeps it disabled (not validated). */
export async function remoteWiFiDenied(h: RemoteHousehold, opts: CommandOptions): Promise<string[] | null> {
  try {
    const r = await remoteRequest(h, 'GET', '/api/wifi/blocked', undefined, opts);
    return Array.isArray(r.body) ? (r.body as string[]).map((m) => m.toLowerCase()) : [];
  } catch (e) {
    if (e instanceof RemoteCommandError && (e.reason === 'unverified' || (e.reason === 'box' && e.status === 501))) return null;
    throw e;
  }
}

/** Cuts (Basic) or gives back (free) one device's Wi-Fi access through the relay. */
export function remoteSetWiFiCut(h: RemoteHousehold, mac: string, cut: boolean, opts: CommandOptions): Promise<VerifiedResult> {
  return remoteRequest(h, 'POST', `/api/devices/${mac.toLowerCase()}/${cut ? 'wifi-block' : 'wifi-unblock'}`, undefined, opts);
}

/** What a whole-house cut or restore changed. */
export type HouseResult = { cut: string[]; restored: string[]; spared: string[]; failed: string[] };

function houseResult(body: unknown): HouseResult {
  const o = (body && typeof body === 'object' ? body : {}) as Record<string, unknown>;
  const list = (k: string) => (Array.isArray(o[k]) ? (o[k] as unknown[]).filter((m): m is string => typeof m === 'string') : []);
  return { cut: list('cut'), restored: list('restored'), spared: list('spared'), failed: list('failed') };
}

// What the last whole-house cut blocked, so "Rétablir" gives back exactly
// that even after the relay restarted.
const HOUSE_CUT_KEY = 'kergui.remote.houseCut';

/** Cuts Internet for the whole house (Basic); the relay spares itself, the other relays and the protected devices. */
export async function remoteCutInternet(h: RemoteHousehold, opts: CommandOptions): Promise<HouseResult> {
  const r = houseResult((await remoteRequest(h, 'POST', '/api/internet/cut', undefined, opts)).body);
  try {
    const before: string[] = JSON.parse((await SecureStore.getItemAsync(HOUSE_CUT_KEY)) ?? '[]');
    await SecureStore.setItemAsync(HOUSE_CUT_KEY, JSON.stringify([...new Set([...before, ...r.cut])]));
  } catch {
    // The relay remembers too.
  }
  return r;
}

/** Gives Internet back to what the cut took (free). */
export async function remoteRestoreInternet(h: RemoteHousehold, opts: CommandOptions): Promise<HouseResult> {
  let macs: string[] = [];
  try {
    macs = JSON.parse((await SecureStore.getItemAsync(HOUSE_CUT_KEY)) ?? '[]');
  } catch {
    macs = [];
  }
  const r = houseResult((await remoteRequest(h, 'POST', '/api/internet/restore', macs.length ? { macs } : undefined, opts)).body);
  try {
    await SecureStore.deleteItemAsync(HOUSE_CUT_KEY);
  } catch {
    // Next restore names them again: unblocking twice is harmless.
  }
  return r;
}

/** "moins d'une minute", "5 min", "2 h": an age as the banner says it. */
export function ageWords(ageS: number): string {
  if (ageS < 60) return t('remote.age.now');
  const min = Math.floor(ageS / 60);
  if (min < 60) return t('remote.age.min', { n: min });
  const h = Math.floor(min / 60);
  return h < 48 ? t('remote.age.h', { n: h }) : t('remote.age.days', { n: Math.floor(h / 24) });
}
