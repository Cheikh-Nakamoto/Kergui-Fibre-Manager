import KerguiCore from '../../modules/kergui-core';
import type {
  AutoRejectEnforcer,
  BlockedDevice,
  AutoRejectRun,
  AutoRejectStatus,
  EnforcerAnswer,
  Device,
  DeviceLabel,
  FilterSetupResult,
  InspectReport,
  LogEntry,
  LoginResult,
  RouterInfo,
  Usage,
  UsageSample,
  WiFiSSID,
  WiFiSSIDChange,
} from './types';

/** The code of the core's 403 on a paid action (httpapi.NotEntitledCode). */
export const NOT_ENTITLED = 'not_entitled';

/** The code of the core's 409 when the box's list is full (httpapi.AccessListFullCode). */
export const ACCESS_LIST_FULL = 'access_list_full';

/** The code of the core's 409 when a write would cut this phone off the box (httpapi.SelfLockoutCode, #113). */
export const SELF_LOCKOUT = 'self_lockout';

/**
 * Why the core refused a write as cutting this phone off the box.
 * allow_list: closing the Wi-Fi with neither this phone nor a relay known
 * on the box (RFC-007), which would leave everyone out.
 */
export type SelfLockoutReason = 'self_device' | 'self_network' | 'last_network' | 'allow_list';

/**
 * What the core adds to some errors: a full list's kind and numbers, a
 * self lockout's reason.
 */
export type ApiErrorDetails = { kind?: string; limit?: number; used?: number; reason?: string };

// The only module that talks to the native core. Screens use the hooks in
// src/features, which use this.

/** An API answer with a 4xx/5xx status, or a core that could not be reached. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    /** The core's stable error code, when it gives one (e.g. 'not_entitled'). */
    readonly code?: string,
    /** The numbers that come with the code, e.g. a full list's limit. */
    readonly details: ApiErrorDetails = {},
  ) {
    super(message);
    this.name = 'ApiError';
  }

  /**
   * The core refused a paid action the entitlement does not cover (RFC-006):
   * shown as a lock and one neutral sentence, never as an error.
   */
  get notEntitled() {
    return this.status === 403 && this.code === NOT_ENTITLED;
  }

  /**
   * The box refused a block because its list is full (RFC-007 D6): shown
   * as « La box accepte N appareils au plus », never as the box's answer.
   */
  get accessListFull() {
    return this.status === 409 && this.code === ACCESS_LIST_FULL;
  }

  /**
   * The write would cut this phone off the box (#113): blocking it, or
   * turning off its network or the last one on. Shown in words, by reason.
   */
  get selfLockout() {
    return this.status === 409 && this.code === SELF_LOCKOUT;
  }

  /** How many devices the full list takes; 0 when the box never said. */
  get listLimit() {
    return this.details.limit ?? 0;
  }

  /** The box model does not support this action yet (UNVERIFIED endpoint). */
  get notSupported() {
    return this.status === 501;
  }

  /** The https box presented a different certificate than the pinned one. */
  get pinMismatch() {
    return this.status === 502 && this.message.includes('pinned fingerprint');
  }

  /**
   * A new box (#92) demands a new admin password after the label's login:
   * nothing was stored, the owner sets one with api.firstPassword.
   */
  get passwordChangeRequired() {
    return this.status === 409 && this.code === 'password_change_required';
  }

  /** The box refused the username or the password. */
  get authFailed() {
    return this.status === 401;
  }

  /** The core could not be reached at all (status 0): not open, not configured. */
  get coreUnreachable() {
    return this.status === 0;
  }

  /** The box accepted the connection but answered nothing in time. */
  get timedOut() {
    return this.status === 504 || /deadline exceeded|timeout|Client.Timeout/i.test(this.message);
  }
}

export async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res;
  try {
    res = await KerguiCore.request(method, path, body === undefined ? '' : JSON.stringify(body));
  } catch (e) {
    throw new ApiError(0, e instanceof Error ? e.message : String(e));
  }
  let data: unknown = null;
  if (res.body) {
    try {
      data = JSON.parse(res.body);
    } catch {
      data = null;
    }
  }
  if (res.status >= 400) {
    const err = data as { error?: string; code?: string; kind?: unknown; limit?: unknown; used?: unknown; reason?: unknown } | null;
    const details: ApiErrorDetails = {};
    if (typeof err?.reason === 'string') details.reason = err.reason;
    if (typeof err?.kind === 'string') details.kind = err.kind;
    if (typeof err?.limit === 'number') details.limit = err.limit;
    if (typeof err?.used === 'number') details.used = err.used;
    throw new ApiError(res.status, err?.error ?? `HTTP ${res.status}`, typeof err?.code === 'string' ? err.code : undefined, details);
  }
  return data as T;
}

const mac = (m: string) => encodeURIComponent(m);

export const api = {
  discover: () => call<RouterInfo>('GET', '/api/discover'),
  /** Discovery plus the logged-in reads; carries the box serial number. */
  inspect: () => call<InspectReport>('GET', '/api/inspect'),
  login: (username: string, password: string, test = false) =>
    call<LoginResult>('POST', '/api/login', { username, password, test }),
  /**
   * After a 409 password_change_required (#92): sets newPassword on the box
   * with the label's login, signs in with it and stores it, never the label
   * password. 400 weak_password before the box is asked, 422
   * password_rejected with the box's own message.
   */
  firstPassword: (username: string, password: string, newPassword: string) =>
    call<LoginResult>('POST', '/api/box/first-password', { username, password, newPassword }),
  devices: () => call<Device[]>('GET', '/api/devices'),
  rename: (m: string, customName: string) => call('PATCH', `/api/devices/${mac(m)}`, { custom_name: customName }),
  /**
   * The household's names and notes kept by this phone's core, with when
   * each was set (#95): what the relay is sent, what a list from the relay
   * is merged with.
   */
  labels: () => call<{ labels: DeviceLabel[] | null }>('GET', '/api/labels').then((r) => r?.labels ?? []),
  /** Labels made elsewhere (the relay's list, a rename through it): the later edit wins. */
  applyLabels: (labels: DeviceLabel[]) => call<{ changed: string[] | null }>('POST', '/api/labels', { labels }),
  block: (m: string) => call('POST', `/api/devices/${mac(m)}/block`),
  unblock: (m: string) => call('POST', `/api/devices/${mac(m)}/unblock`),
  /**
   * The box's Wi-Fi deny list ("Accès coupé"): MACs its access points refuse,
   * even with the right password. 501 when the model has no Wi-Fi access
   * control.
   */
  wifiBlocked: () => call<string[]>('GET', '/api/wifi/blocked'),
  /** Every device the box's lists hold, away ones included (RFC-007 D6). */
  blocked: () => call<{ devices: BlockedDevice[] | null }>('GET', '/api/blocked').then((r) => r?.devices ?? []),
  /** Cut Wi-Fi access (Basic): unlike block, the device leaves the Wi-Fi. */
  wifiBlock: (m: string) => call('POST', `/api/devices/${mac(m)}/wifi-block`),
  /** Give Wi-Fi access back (free, whatever the pass). */
  wifiUnblock: (m: string) => call('POST', `/api/devices/${mac(m)}/wifi-unblock`),
  ssids: () => call<WiFiSSID[]>('GET', '/api/wifi/ssids'),
  updateSSID: (id: string, change: WiFiSSIDChange) =>
    call<WiFiSSID>('PATCH', `/api/wifi/ssids/${encodeURIComponent(id)}`, change),
  /**
   * The setup step after a sign-in to a box (#56): the MAC filter on in
   * blocklist mode, never allowlist. Idempotent; the core writes the journal.
   */
  setupFilter: () => call<{ result: FilterSetupResult }>('POST', '/api/filter/setup'),
  /** Per-device consumption for a month, most first ("" = the current month). */
  usage: (period = '') =>
    call<Usage[] | null>('GET', period ? `/api/usage?period=${encodeURIComponent(period)}` : '/api/usage').then((u) => u ?? []),
  /**
   * Reads the box's byte counters once and records what each device used
   * since the last reading. The phone is the only one sampling: what happens
   * while Kergui is not running is not counted. 501 when the box has no
   * counters.
   */
  sampleUsage: () => call<UsageSample>('POST', '/api/usage/sample'),
  /** Auto-reject's setting and the devices it tracks. */
  autoReject: () => call<AutoRejectStatus>('GET', '/api/autoreject'),
  /**
   * Turns auto-reject on (Max: every device on the box now is accepted) or
   * off (free).
   */
  setAutoReject: (enabled: boolean) => call<AutoRejectStatus>('PUT', '/api/autoreject', { enabled }),
  /** One round of auto-reject: what the watch service runs on each tick. */
  runAutoReject: () => call<AutoRejectRun>('POST', '/api/autoreject/run'),
  /**
   * The hub's word on who enforces auto-reject (#36). Every round after it,
   * the Android watch service's included, stands down while the relay's
   * heartbeat is under 2 minutes old; an answer too old lets the phone act.
   */
  setAutoRejectEnforcer: (answer: EnforcerAnswer) =>
    call<{ enforcer: AutoRejectEnforcer }>('PUT', '/api/autoreject/enforcer', answer),
  /** Accepts a device under auto-reject (free): it will not be refused. */
  acceptDevice: (m: string) => call('POST', `/api/devices/${mac(m)}/accept`),
  /**
   * This phone's backup (a zip of appareils.csv and conso.csv), base64, its
   * file name, and the box id the hub keys it by (#89).
   */
  exportBackup: () => call<{ archive: string; name: string; boxId: string }>('GET', '/api/export'),
  /**
   * Imports a backup — made by « Faire une sauvegarde » or by `kergui export`
   * on a PC — base64: the backup's names and notes win.
   */
  importBackup: (archive: string) =>
    call<{ devices: number; named: number; usage: number }>('POST', '/api/import', { archive }),
  /** The devices approved on this box (#58), and those not pushed to the hub yet. */
  approvals: () => call<{ macs: string[] | null; unsynced: string[] | null }>('GET', '/api/approvals').then((a) => ({
    macs: a?.macs ?? [],
    unsynced: a?.unsynced ?? [],
  })),
  /** Replaces the list with the household's (the hub's): all synced. */
  setApprovals: (macs: string[]) => call('PUT', '/api/approvals', { macs }),
  /** « Autoriser »: the device never alerts again. */
  approve: (m: string) => call('PUT', `/api/approvals/${mac(m)}`),
  withdrawApproval: (m: string) => call('DELETE', `/api/approvals/${mac(m)}`),
  /**
   * The owner's phones as the hub lists them (#87): the core marks them
   * Owner on this box's list, or, with tenantId, on that household's remote list.
   */
  setOwnerDevices: (macs: string[], tenantId?: string) =>
    call('PUT', '/api/owner-devices', tenantId ? { macs, tenantId } : { macs }),
  /** The demo only: its visitor joins or leaves the box (#58). */
  demoVisitor: (present: boolean) => call('POST', '/api/demo/visitor', { present }),
  /** The demo only: its box's list full (or not), to show « liste pleine ». */
  demoListFull: (full: boolean) => call('POST', '/api/demo/list-full', { full }),
  /** One device's last months, oldest first, a month without a measure at 0 (Max). */
  deviceUsage: (mac: string, months: number) =>
    call<Usage[] | null>('GET', `/api/usage/devices/${encodeURIComponent(mac)}?months=${months}`).then((u) => u ?? []),
};

/** The box login the core keeps; stored false (and both empty) when there is none. */
export type BoxPassword = { stored: boolean; username: string; password: string };

export const core = {
  version: () => KerguiCore.version(),
  /**
   * The core's recent log lines, oldest first. It answers even before the
   * core is pointed at a box, which is what makes it useful on the
   * connection screen.
   */
  journal: async (since = 0, limit = 0): Promise<LogEntry[]> => {
    const raw = await KerguiCore.journal(since, limit);
    try {
      const parsed: unknown = JSON.parse(raw);
      return Array.isArray(parsed) ? (parsed as LogEntry[]) : [];
    } catch {
      return [];
    }
  },
  /**
   * Records one line in that journal: what only the app knows (the address
   * someone typed, a certificate they refused, a screen that gave up). Never
   * a password. It must never break a screen, so it swallows its own errors.
   */
  note: (level: 'debug' | 'info' | 'warn' | 'error', message: string) => {
    try {
      KerguiCore.note(level, message);
    } catch {
      // A journal that cannot be written is not worth an error on screen.
    }
  },
  /**
   * The box login the core keeps (#93), for « Mot de passe de la box » in
   * Réglages, shown only after the phone's own unlock. A core function, not
   * an API route: no route ever hands a box password out.
   */
  boxPassword: async (): Promise<BoxPassword> => {
    const parsed = JSON.parse(await KerguiCore.boxPassword()) as Partial<BoxPassword>;
    return { stored: parsed.stored === true, username: parsed.username ?? '', password: parsed.password ?? '' };
  },
  open: (vaultKey: string) => KerguiCore.open(vaultKey),
  /** The hub keys the core's own gate trusts (base64, comma-separated). */
  setPublicKeys: (keys: string) => KerguiCore.setPublicKeys(keys),
  /** The entitlement token the core's gate checks ('' for none). */
  setEntitlement: (token: string) => KerguiCore.setEntitlement(token),
  configure: (url: string, pin: string) => KerguiCore.configure(url, '', pin),
  fingerprint: (url: string) => KerguiCore.certificateFingerprint(url),
  startDemo: () => KerguiCore.startDemo(),
  stopDemo: () => KerguiCore.stopDemo(),
};
