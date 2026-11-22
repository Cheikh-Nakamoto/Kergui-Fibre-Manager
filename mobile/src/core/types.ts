// Shapes of the core's JSON API — the same as `kergui serve` (docs/api.md).
// Go structs without json tags serialise with their field names.

export type Device = {
  ID: string;
  MAC: string;
  IP: string;
  Hostname: string;
  CustomName: string;
  Vendor: string;
  SSID: string;
  FirstSeen: string;
  LastSeen: string;
  Connected: boolean;
  Blocked: boolean;
  NewlySeen: boolean;
};

/**
 * A device's consumption over one month (GET /api/usage). sent is its upload,
 * received its download; months recorded before the split have only bytes.
 */
export type Usage = {
  mac: string;
  period: string;
  bytes: number;
  sent: number;
  received: number;
};

/** One sampling round of the box's byte counters (POST /api/usage/sample). */
export type UsageSample = { devices: number; bytes: number; baseline: boolean };

export type RouterInfo = {
  BaseURL: string;
  Vendor: string;
  Model: string;
  Firmware: string;
  AdapterID: string;
  Confidence: number;
  Reachable: boolean;
  /**
   * The box serial number, upper case (e.g. "ZTEG1A2B3C4D"): the identity a
   * Pro pass binds to. Empty when the model does not expose it yet. Only
   * /api/inspect fills it in (it needs the box login).
   */
  SerialNumber?: string;
};

/** GET /api/inspect: discovery plus what the logged-in adapter read. */
export type InspectReport = {
  Info: RouterInfo;
  AuthOK: boolean;
  Warnings: string[] | null;
};

export type WiFiSSID = {
  id: string;
  alias: string;
  band: string;
  name: string;
  enabled: boolean;
  hidden: boolean;
  security: string;
  locked?: string;
};

export type WiFiSSIDChange = {
  enabled?: boolean;
  name?: string;
  password?: string;
  hidden?: boolean;
};

export type FilterState = { enabled: boolean };

export type LoginResult = { status: string; stored: boolean; adapter: string; router_id: string };

/** One line of the core's journal (GET /api/logs, or KerguiCore.journal). */
export type LogEntry = {
  seq: number;
  time: string;
  /** The logger's own spelling: DEBUG | INFO | WARN | ERROR. */
  level: string;
  msg: string;
};
