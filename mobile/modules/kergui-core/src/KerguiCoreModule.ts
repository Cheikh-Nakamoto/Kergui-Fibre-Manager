import { NativeModule, requireNativeModule } from 'expo';

/** Raw answer of the in-memory API (same shapes as `kergui serve`). */
export type CoreResponse = { status: number; body: string };

declare class KerguiCoreModule extends NativeModule<{}> {
  version(): string;
  /** Opens the local database; vaultKey encrypts the stored router credentials. */
  open(vaultKey: string): Promise<void>;
  /** Points the core at a router. adapterId '' = auto-detect; pinSha256 '' for http. */
  configure(routerUrl: string, adapterId: string, pinSha256: string): Promise<void>;
  request(method: string, path: string, body: string): Promise<CoreResponse>;
  /**
   * The hub's Ed25519 public keys the core trusts to sign entitlement tokens:
   * raw 32-byte keys in standard base64, comma-separated. '' trusts none.
   */
  setPublicKeys(keys: string): Promise<void>;
  /**
   * The current entitlement token ('' for none). The core then refuses the
   * paid writes it does not cover with a 403 {"code": "not_entitled"}.
   */
  setEntitlement(token: string): Promise<void>;
  /** SHA-256 of the certificate an https router presents (trust on first use). */
  certificateFingerprint(routerUrl: string): Promise<string>;
  /**
   * The core's recent log lines as a JSON array of
   * `{seq, time, level, msg}`, oldest first. `since` is the last sequence
   * number already shown (0 for everything kept), `limit` caps the answer.
   */
  journal(since: number, limit: number): Promise<string>;
  /** Records one line from the app in the same journal. Never a secret. */
  note(level: string, message: string): void;
  /** Starts the embedded demo router (login admin / admin) and points the core at it. */
  startDemo(): Promise<void>;
  stopDemo(): Promise<void>;

  // Android only: the foreground service that keeps watching the box.
  /** Saves the watch settings and starts the service. */
  startWatch?(config: WatchSettings, resetKnown: boolean): Promise<void>;
  /** Stops the service; forgetKnown also drops the devices seen so far. */
  stopWatch?(forgetKnown: boolean): Promise<void>;
  watchEnabled?(): boolean;
  /** True when the app is exempt from battery optimisation. */
  batteryUnrestricted?(): boolean;
  /** Opens the system prompt that exempts the app from battery optimisation. */
  requestBatteryUnrestricted?(): Promise<void>;
}

/** What the watch service needs to run with the app closed. */
export type WatchSettings = {
  vaultKey: string;
  routerUrl: string;
  pin: string;
  intervalSec: number;
  ongoingTitle: string;
  ongoingText: string;
  /** `{count}` and `{time}` are replaced. */
  ongoingCountText: string;
  ongoingErrorText: string;
  ongoingChannel: string;
  alertTitle: string;
  /** `{name}` and `{mac}` are replaced. */
  alertBody: string;
  alertChannel: string;
};

export default requireNativeModule<KerguiCoreModule>('KerguiCore');
