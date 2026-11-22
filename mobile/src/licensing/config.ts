import Constants from 'expo-constants';

import { makeRing, type TrustedKey } from './keyring';

// The build's licensing settings, set by app.config.js from the environment
// (see config/licensing.js) and read back through expo-constants.

export type LicensingConfig = {
  /** No trailing slash; empty when this build has no hub (no account features). */
  hubUrl: string;
  /**
   * The hub's Ed25519 public keys embedded in the build, with their key ids
   * (several during a signing key rotation); empty when the build has none.
   */
  publicKeys: TrustedKey[];
};

type Extra = { licensing?: { hubUrl?: string; publicKeys?: TrustedKey[] } };

export function licensingConfig(extra: Extra | undefined = Constants.expoConfig?.extra as Extra | undefined): LicensingConfig {
  const l = extra?.licensing ?? {};
  return {
    hubUrl: (l.hubUrl ?? '').replace(/\/+$/, ''),
    publicKeys: makeRing(Array.isArray(l.publicKeys) ? l.publicKeys : []).keys,
  };
}

export const config = licensingConfig();
