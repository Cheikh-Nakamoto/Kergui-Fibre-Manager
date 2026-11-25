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
  /**
   * The household approved it (#58): « Approuver », or a name given. It then
   * never alerts. Set by the phone's core; absent on lists from elsewhere.
   */
  Approved?: boolean;
  /**
   * What the phone's core says the device is to the household (#69): « Self »
   * is the phone running the app (#86), « Owner » a phone of the household's
   * owner (#87). A marked device is left out of the Devices tab, its counts,
   * its alerts and the inbox. Absent otherwise.
   */
  Mark?: DeviceMark;
  /** The household's notes on it (a backup's « notes » column). */
  Notes?: string;
  /**
   * When the household last set its name or notes (#95), RFC 3339; absent
   * when never. The owner's phone and the relay each keep a copy of the
   * labels: the later edit wins.
   */
  LabelAt?: string;
};

/**
 * A device's name and notes as the household set them, and when (#95):
 * GET /api/labels, and the body of POST /api/labels on the core or, signed,
 * on the relay. A missing name or notes leaves that one as it is.
 */
export type DeviceLabel = { mac: string; name?: string; notes?: string; at: string };

/** The marks the core puts on a device (mobilecore/self.go). */
export type DeviceMark = 'Self' | 'Owner';

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

/** Where a device stands under auto-reject (Max). */
export type AutoRejectState = 'pending' | 'accepted' | 'refused';

/** GET/PUT /api/autoreject: the setting, kept in the core for the app and the watch service. */
export type AutoRejectStatus = {
  enabled: boolean;
  /** When it was turned on, RFC 3339; '' when off. */
  since: string;
  delay_minutes: number;
  devices: { mac: string; state: AutoRejectState; first_seen: string; deadline: string }[];
};

/** POST /api/autoreject/run: one round of the rule. */
export type AutoRejectRun = {
  enabled: boolean;
  /** false when the pass no longer covers it: nothing was refused. */
  entitled: boolean;
  refused: { mac: string; name: string }[];
  pending: number;
  /**
   * Who enforced this round (#36): 'relay' while the household's relay
   * does, as the hub last said; the phone then touched nothing.
   */
  enforcer?: AutoRejectEnforcer;
};

/** Who enforces auto-reject (#36): the household's relay, or this phone. */
export type AutoRejectEnforcer = 'phone' | 'relay';

/** The hub's GET router/auto-reject answer, handed to the core as is. */
export type EnforcerAnswer = {
  enforcer: AutoRejectEnforcer;
  agentId?: string;
  relayHeartbeatAt?: string;
  relayHeartbeatAgeSeconds?: number;
};

/** What the MAC filter setup step did to the box (#56). */
export type FilterSetupResult = 'enabled' | 'already' | 'permit' | 'unsupported';
