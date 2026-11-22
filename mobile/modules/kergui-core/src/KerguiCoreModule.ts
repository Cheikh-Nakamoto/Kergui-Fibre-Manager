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
  // The owner's keys (issue #43). Generated and kept in the Go core's vault;
  // nothing here returns private material.
  /** The owner's public keys, created on the first call: JSON {keyId, ed25519, x25519} (base64). */
  ownerKeys(): Promise<string>;
  /** The QR payload (kgp1.…) for a pairing code from the hub: the code and the owner's fingerprint. */
  pairingInvite(code: string): Promise<string>;
  /** The six digits for a relay's public keys as the hub lists them (base64). */
  pairingCode(relayEd25519: string, relayX25519: string): Promise<string>;
  /**
   * Signs the household's next trust bundle. current is the hub's GET
   * trust-bundle answer ('' for none), change {relay?, routerPin?}. Returns
   * JSON {payload, sig, version}: the body of PUT trust-bundle.
   */
  signTrustBundle(tenantId: string, current: string, change: string): Promise<string>;
  /** Demo only: plays the relay's part of pairing on the QR; JSON {agentId, name, kind, ed25519, x25519, code}. */
  demoRelayRedeem(invite: string): Promise<string>;
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
  /** Auto-reject (Max): the "Accepter" action on a new-device notification. */
  approveText: string;
  /** The notification of a device auto-reject refused; `{name}` and `{mac}` are replaced. */
  refusedTitle: string;
  refusedBody: string;
};

export default requireNativeModule<KerguiCoreModule>('KerguiCore');
