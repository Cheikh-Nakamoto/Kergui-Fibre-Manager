import KerguiCore from '../../modules/kergui-core';
import type {
  AutoRejectRun,
  AutoRejectStatus,
  Device,
  FilterSetupResult,
  FilterState,
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

// The only module that talks to the native core. Screens use the hooks in
// src/features, which use this.

/** An API answer with a 4xx/5xx status, or a core that could not be reached. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    /** The core's stable error code, when it gives one (e.g. 'not_entitled'). */
    readonly code?: string,
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

  /** The box model does not support this action yet (UNVERIFIED endpoint). */
  get notSupported() {
    return this.status === 501;
  }

  /** The https box presented a different certificate than the pinned one. */
  get pinMismatch() {
    return this.status === 502 && this.message.includes('pinned fingerprint');
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
    const err = data as { error?: string; code?: string } | null;
    throw new ApiError(res.status, err?.error ?? `HTTP ${res.status}`, typeof err?.code === 'string' ? err.code : undefined);
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
  devices: () => call<Device[]>('GET', '/api/devices'),
  rename: (m: string, customName: string) => call('PATCH', `/api/devices/${mac(m)}`, { custom_name: customName }),
  block: (m: string) => call('POST', `/api/devices/${mac(m)}/block`),
  unblock: (m: string) => call('POST', `/api/devices/${mac(m)}/unblock`),
  /**
   * The box's Wi-Fi deny list ("Accès coupé"): MACs its access points refuse,
   * even with the right password. 501 when the model has no Wi-Fi access
   * control.
   */
  wifiBlocked: () => call<string[]>('GET', '/api/wifi/blocked'),
  /** Cut Wi-Fi access (Basic): unlike block, the device leaves the Wi-Fi. */
  wifiBlock: (m: string) => call('POST', `/api/devices/${mac(m)}/wifi-block`),
  /** Give Wi-Fi access back (free, whatever the pass). */
  wifiUnblock: (m: string) => call('POST', `/api/devices/${mac(m)}/wifi-unblock`),
  ssids: () => call<WiFiSSID[]>('GET', '/api/wifi/ssids'),
  updateSSID: (id: string, change: WiFiSSIDChange) =>
    call<WiFiSSID>('PATCH', `/api/wifi/ssids/${encodeURIComponent(id)}`, change),
  filter: () => call<FilterState>('GET', '/api/filter'),
  setFilter: (enabled: boolean) => call<FilterState>('POST', '/api/filter/toggle', { enabled }),
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
  /** Accepts a device under auto-reject (free): it will not be refused. */
  acceptDevice: (m: string) => call('POST', `/api/devices/${mac(m)}/accept`),
  /** This phone's backup (a zip of appareils.csv and conso.csv), base64, and its file name. */
  exportBackup: () => call<{ archive: string; name: string }>('GET', '/api/export'),
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
  /** « Approuver »: the device never alerts again. */
  approve: (m: string) => call('PUT', `/api/approvals/${mac(m)}`),
  withdrawApproval: (m: string) => call('DELETE', `/api/approvals/${mac(m)}`),
  /** The demo only: its visitor joins or leaves the box (#58). */
  demoVisitor: (present: boolean) => call('POST', '/api/demo/visitor', { present }),
  /** One device's last months, oldest first, a month without a measure at 0 (Max). */
  deviceUsage: (mac: string, months: number) =>
    call<Usage[] | null>('GET', `/api/usage/devices/${encodeURIComponent(mac)}?months=${months}`).then((u) => u ?? []),
};

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
