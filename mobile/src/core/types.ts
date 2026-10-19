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

export type RouterInfo = {
  BaseURL: string;
  Vendor: string;
  Model: string;
  Firmware: string;
  AdapterID: string;
  Confidence: number;
  Reachable: boolean;
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
