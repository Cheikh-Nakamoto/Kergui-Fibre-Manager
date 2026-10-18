import KerguiCore from '../../modules/kergui-core';
import type { Device, FilterState, LoginResult, RouterInfo, WiFiSSID, WiFiSSIDChange } from './types';

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
  open: (vaultKey: string) => KerguiCore.open(vaultKey),
  configure: (url: string, pin: string) => KerguiCore.configure(url, '', pin),
  fingerprint: (url: string) => KerguiCore.certificateFingerprint(url),
  startDemo: () => KerguiCore.startDemo(),
  stopDemo: () => KerguiCore.stopDemo(),
};
