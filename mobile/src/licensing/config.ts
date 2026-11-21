import Constants from 'expo-constants';

import { decodeBase64 } from './base64';

// The build's licensing settings, set by app.config.js from the environment
// (see config/licensing.js) and read back through expo-constants.

export type LicensingConfig = {
  /** No trailing slash; empty when this build has no hub (no account features). */
  hubUrl: string;
  /** The hub's Ed25519 public key, or null when the build has none. */
  publicKey: Uint8Array | null;
  /** The same key in standard base64, as the Go core takes it; '' when none. */
  publicKeyBase64: string;
};

type Extra = { licensing?: { hubUrl?: string; publicKey?: string } };

export function licensingConfig(extra: Extra | undefined = Constants.expoConfig?.extra as Extra | undefined): LicensingConfig {
  const l = extra?.licensing ?? {};
  const key = l.publicKey ? decodeBase64(l.publicKey) : null;
  const ok = !!key && key.length === 32;
  return {
    hubUrl: (l.hubUrl ?? '').replace(/\/+$/, ''),
    publicKey: ok ? key : null,
    publicKeyBase64: ok ? (l.publicKey ?? '') : '',
  };
}

export const config = licensingConfig();
