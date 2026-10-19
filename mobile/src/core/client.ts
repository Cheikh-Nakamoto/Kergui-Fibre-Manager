import KerguiCore from '../../modules/kergui-core';
import type { Device, FilterState, LogEntry, LoginResult, RouterInfo, WiFiSSID, WiFiSSIDChange } from './types';

// The only module that talks to the native core. Screens use the hooks in
// src/features, which use this.

/** An API answer with a 4xx/5xx status, or a core that could not be reached. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
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
    const msg = (data as { error?: string } | null)?.error ?? `HTTP ${res.status}`;
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

const mac = (m: string) => encodeURIComponent(m);

export const api = {
  discover: () => call<RouterInfo>('GET', '/api/discover'),
  login: (username: string, password: string, test = false) =>
    call<LoginResult>('POST', '/api/login', { username, password, test }),
  devices: () => call<Device[]>('GET', '/api/devices'),
  rename: (m: string, customName: string) => call('PATCH', `/api/devices/${mac(m)}`, { custom_name: customName }),
  block: (m: string) => call('POST', `/api/devices/${mac(m)}/block`),
  unblock: (m: string) => call('POST', `/api/devices/${mac(m)}/unblock`),
  ssids: () => call<WiFiSSID[]>('GET', '/api/wifi/ssids'),
  updateSSID: (id: string, change: WiFiSSIDChange) =>
    call<WiFiSSID>('PATCH', `/api/wifi/ssids/${encodeURIComponent(id)}`, change),
  filter: () => call<FilterState>('GET', '/api/filter'),
  setFilter: (enabled: boolean) => call<FilterState>('POST', '/api/filter/toggle', { enabled }),
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
  configure: (url: string, pin: string) => KerguiCore.configure(url, '', pin),
  fingerprint: (url: string) => KerguiCore.certificateFingerprint(url),
  startDemo: () => KerguiCore.startDemo(),
  stopDemo: () => KerguiCore.stopDemo(),
};
