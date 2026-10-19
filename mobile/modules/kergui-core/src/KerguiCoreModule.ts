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
}

export default requireNativeModule<KerguiCoreModule>('KerguiCore');
