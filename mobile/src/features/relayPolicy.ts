import { api, core } from '../core/client';
import { owner } from '../core/owner';
import type { AutoRejectEnforcer, AutoRejectStatus, EnforcerAnswer } from '../core/types';
import { useAccount } from '../licensing/account';
import { config } from '../licensing/config';
import { afterCoreEntitlement, useEntitlement } from '../licensing/entitlement';
import { hub } from '../licensing/hub';
import { withHousehold } from '../licensing/household';

// Auto-reject on the household's relay (RFC-006, #36), the app's side.
//
// Publication: the relay enforces the rule 24/7 only once the owner has told
// it to. The owner's core signs the relay policy — the rule as it stands in
// the core (on/off, since, the devices accepted on this phone) and the
// entitlement token — and the app PUTs it to …/relay-policy whenever any of
// it changes. A phone that is not the owner's gets nothing from its core
// (the bundle is not its key's), so it never publishes. Best effort: a hub
// out of reach is noted in the journal and tried again later; the setting on
// the phone is never undone for it.
//
// Election: before a round the app asks the hub who enforces (GET
// router/auto-reject) and hands the answer to the core (PUT
// /api/autoreject/enforcer). The core's rounds — the Android watch
// service's, which never calls the hub, included — then stand down while
// the relay's heartbeat is under 2 minutes old, on the phone's own clock.
// The answer ages out by itself: with the app closed or the hub out of
// reach, the phone takes over within 2 minutes, which is the safe side.

export type PolicyOutcome = 'published' | 'unchanged' | 'skipped' | 'failed';

/** A failed upload is tried again after this, unless the rule changes first. */
const RETRY_MS = 2 * 60_000;
/** The hub is asked who enforces at most this often; the core keeps the answer meanwhile. */
const ASK_EVERY_MS = 20_000;

/** What the hub was last brought up to date with (fingerprint), or null. */
let sent: string | null = null;
let failed: { fp: string; at: number } | null = null;
let queue: Promise<unknown> = Promise.resolve();
let asked: { at: number; who: AutoRejectEnforcer } | null = null;

/** Forget what was sent and asked (sign-out, another household; tests). */
export function forgetRelayPolicy(): void {
  sent = null;
  failed = null;
  asked = null;
}

/**
 * Signed in to a hub, on a real box: the demo's rule is not the
 * household's (the core refuses to sign one in the demo too).
 */
function inHousehold(demo: boolean): boolean {
  return !demo && !!config.hubUrl && !!useAccount.getState().account;
}

function fingerprint(st: AutoRejectStatus, token: string | null): string {
  const accepted = st.devices
    .filter((d) => d.state === 'accepted')
    .map((d) => d.mac.toLowerCase())
    .sort();
  const tenant = useAccount.getState().household()?.tenantId ?? '';
  return JSON.stringify([tenant, st.enabled, st.since, accepted, token ?? '']);
}

const message = (e: unknown) => (e instanceof Error ? e.message : String(e));


async function publish(force: boolean, demo: boolean): Promise<PolicyOutcome> {
  if (!inHousehold(demo)) return 'skipped';
  let st: AutoRejectStatus;
  try {
    st = await api.autoReject();
  } catch {
    // A box without Wi-Fi access control, or the core not pointed at one.
    return 'skipped';
  }
  const fp = fingerprint(st, useEntitlement.getState().token);
  if (!force && fp === sent) return 'unchanged';
  if (!force && failed?.fp === fp && Date.now() - failed.at < RETRY_MS) return 'skipped';
  try {
    const out = await withHousehold(async (tok, tenantId): Promise<PolicyOutcome> => {
      const bundle = await hub.trustBundle(tok, tenantId);
      // No bundle: no relay paired yet, nothing to tell (pairing syncs again).
      if (!bundle) return 'unchanged';
      const signed = await owner.signRelayPolicy(tenantId, bundle, await hub.relayPolicy(tok, tenantId));
      // Not the owner's phone, or the hub already says the same.
      if (!signed) return 'unchanged';
      await hub.putRelayPolicy(tok, tenantId, signed);
      core.note('info', `relais : règle de rejet automatique transmise (v${signed.version})`);
      return 'published';
    });
    sent = fp;
    failed = null;
    return out;
  } catch (e) {
    failed = { fp, at: Date.now() };
    core.note('warn', `relais : règle de rejet automatique non transmise (${message(e)})`);
    return 'failed';
  }
}

/**
 * Brings the household's relay policy up to date with this phone's rule and
 * token. Cheap when nothing changed (one call to the core, none to the hub);
 * force signs and sends even then (a token just handed to the core, a relay
 * just paired). Calls run one after the other. Never throws.
 */
export function syncRelayPolicy({ force = false, demo = false }: { force?: boolean; demo?: boolean } = {}): Promise<PolicyOutcome> {
  const run = queue.then(() => publish(force, demo));
  queue = run.catch(() => undefined);
  return run.catch(() => 'failed' as const);
}

/**
 * Asks the hub who enforces auto-reject and hands the answer to the core,
 * before a round. 'relay': the core's rounds stand down while that relay's
 * heartbeat is fresh. No session, the demo, the rule off, the hub out of
 * reach or an answer that makes no sense: 'phone', and the phone acts.
 * Never throws.
 */
export async function refreshEnforcer({ demo = false }: { demo?: boolean } = {}): Promise<AutoRejectEnforcer> {
  if (!inHousehold(demo)) return 'phone';
  try {
    if (!(await api.autoReject()).enabled) return 'phone';
  } catch {
    return 'phone';
  }
  if (asked && Date.now() - asked.at < ASK_EVERY_MS) return asked.who;
  let answer: EnforcerAnswer;
  try {
    answer = await withHousehold((tok, tenantId) => hub.autoRejectEnforcer(tok, tenantId));
  } catch {
    answer = { enforcer: 'phone' };
  }
  const age = answer.relayHeartbeatAgeSeconds;
  if (answer.enforcer !== 'relay' || typeof age !== 'number' || !Number.isFinite(age) || age < 0) answer = { enforcer: 'phone' };
  let who: AutoRejectEnforcer = 'phone';
  try {
    who = (await api.setAutoRejectEnforcer(answer)).enforcer === 'relay' ? 'relay' : 'phone';
  } catch {
    who = 'phone';
  }
  asked = { at: Date.now(), who };
  return who;
}

// A new token (a pass bought, renewed or lapsed) reaches the relay as soon
// as the core holds it.
afterCoreEntitlement(() => void syncRelayPolicy({ force: true }));
