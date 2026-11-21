import Constants from 'expo-constants';

import { decodeBase64 } from './base64';

// The build's licensing settings, set by app.config.js from the environment
// (see config/licensing.js) and read back through expo-constants.

export type LicensingConfig = {
  /** No trailing slash; empty when this build has no hub (no account features). */
  hubUrl: string;
  /** The hub's Ed25519 public key, or null when the build has none. */
  publicKey: Uint8Array | null;
};

type Extra = { licensing?: { hubUrl?: string; publicKey?: string } };

export function licensingConfig(extra: Extra | undefined = Constants.expoConfig?.extra as Extra | undefined): LicensingConfig {
  const l = extra?.licensing ?? {};
  const key = l.publicKey ? decodeBase64(l.publicKey) : null;
  return {
    hubUrl: (l.hubUrl ?? '').replace(/\/+$/, ''),
    publicKey: key && key.length === 32 ? key : null,
  };
}

export const config = licensingConfig();
