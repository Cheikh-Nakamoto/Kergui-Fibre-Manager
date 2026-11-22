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
  /**
   * Seals the box password the core logged in with for the relays of the
   * owner's bundle (or only agentId, when not empty); JSON {version,
   * boxPassword, relays: [{agentId, blob, version}]}. Ciphertext only.
   */
  sealCredentials(tenantId: string, bundle: string, agentId: string): Promise<string>;
  // Remote mode (issue #46, RFC-005 design rule 1). JS carries the hub's
  // answers; the core decides whether to believe them.
  /**
   * Whether this phone goes through the hub: JSON {owner, ownerOnly, relays:
   * [{agentId, kind, label}], delegations: [{keyId, scope, expiresAt, label}],
   * coOwners: [{keyId, label}], member?: {keyId, state, scope, expiresAt,
   * control, code, coOwner}}, from the bundle
   * this phone's owner key signed, or one that delegates its member key
   * (bundle is the hub's GET trust-bundle answer, '' for none).
   */
  remoteRelays(tenantId: string, bundle: string): Promise<string>;
  /**
   * Verifies the hub's device snapshot (GET router/devices "snapshot")
   * against the relays of the owner's bundle; JSON {agentId, asOf, devices}.
   */
  openDeviceSnapshot(tenantId: string, bundle: string, snapshot: string): Promise<string>;
  /**
   * Signs a block or unblock with the owner's key after the paid-action gate
   * (boxId: the serial the app kept); JSON {payload, sig, cmdId, mac,
   * expiresAt}. A refusal's message starts with "not_entitled".
   */
  signDeviceCommand(tenantId: string, mac: string, action: string, boxId: string): Promise<string>;
  /**
   * Verifies the relay's signed result of a command against the owner's
   * bundle; JSON {agentId, status, body, executedAt}.
   */
  verifyCommandResult(tenantId: string, cmdId: string, bundle: string, result: string): Promise<string>;
  // Member delegations (issue #47).
  /** This phone's member key, created on the first call: JSON {keyId, ed25519, x25519} (base64). */
  memberKeys(): Promise<string>;
  /**
   * Signs a block or unblock with this phone's member key, when bundle (the
   * hub's GET trust-bundle answer) delegates it control; same JSON as
   * signDeviceCommand. A refusal's message starts with "owner_only",
   * "not_delegated" or "not_entitled".
   */
  signMemberCommand(tenantId: string, bundle: string, mac: string, action: string, boxId: string): Promise<string>;
  /**
   * The owner's next bundle with every member delegation due within a week
   * renewed for 30 days (JSON {payload, sig, version}), or '' when none is due.
   */
  renewDelegations(tenantId: string, current: string): Promise<string>;
  // Recovery of a lost owner phone (issue #48).
  /**
   * The co-owner path: when this phone's member key is a co-owner of bundle
   * (the hub's GET trust-bundle answer), this phone's owner key takes the
   * household over. JSON {tenantId, bundle: {payload, sig, version}, revoked}:
   * the bundle to PUT, revoked the old owner key id. A refusal's message
   * starts with "not_co_owner".
   */
  takeOverHousehold(tenantId: string, bundle: string): Promise<string>;
  /**
   * The path at home: proves boxPassword to the relay at relayAddress (an
   * address on the home network) and signs the household's next bundle with
   * this phone's owner key. Same JSON as takeOverHousehold. A refusal's
   * message starts with "recovery_not_lan", "recovery_proof",
   * "recovery_rate_limited", "recovery_not_ready", "recovery_unreachable" or
   * "recovery_already_owner".
   */
  recoverAtHome(relayAddress: string, boxPassword: string): Promise<string>;
  /** Demo only: plays the relay's part of pairing on the QR; JSON {agentId, name, kind, ed25519, x25519, code}. */
  demoRelayRedeem(invite: string): Promise<string>;
  // This phone as the household's relay (issues #43, #45). Android only for
  // now: the iPhone relay is a separate issue.
  /** Redeems the owner's QR (kgp1.…); JSON {agentId, tenantId, code}: the six digits to compare. */
  relayEnroll?(hubUrl: string, invite: string, name: string): Promise<string>;
  relayEnrolled?(): Promise<boolean>;
  /** JSON {version, trusted}: trusted once the owner confirmed the six digits. */
  relayCheckTrust?(): Promise<string>;
  /** JSON {running, state, reason, check, autoReject, at}. Never a secret. */
  relayStatus?(): Promise<string>;
  /** Saves the relay settings and starts RelayService (foreground, connectedDevice). */
  startRelay?(config: RelaySettings): Promise<void>;
  /** Stops RelayService: the lease goes back to the hub at once. */
  stopRelay?(): Promise<void>;
  relayEnabled?(): boolean;
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

/** What RelayService needs to run with the app closed: the vault key and its notification's texts. */
export type RelaySettings = {
  vaultKey: string;
  title: string;
  channel: string;
  stopText: string;
  startingText: string;
  activeText: string;
  activeBoxOnlyText: string;
  standbyText: string;
  wrongNetworkText: string;
  offlineText: string;
  noWifiText: string;
  noBoxText: string;
};

export default requireNativeModule<KerguiCoreModule>('KerguiCore');
