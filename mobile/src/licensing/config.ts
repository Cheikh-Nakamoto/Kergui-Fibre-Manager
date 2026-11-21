import Constants from 'expo-constants';

import { decodeBase64 } from './base64';

// The build's licensing settings, set by app.config.js from the environment
// (see config/licensing.js) and read back through expo-constants.

export type Flavor = 'play' | 'naboopay';

export type LicensingConfig = {
  flavor: Flavor;
  /** No trailing slash; empty when this build has no hub (Pro unavailable). */
  hubUrl: string;
  /** The hub's Ed25519 public key, or null when the build has none. */
  publicKey: Uint8Array | null;
};

type Extra = { licensing?: { flavor?: string; hubUrl?: string; publicKey?: string } };

export function licensingConfig(extra: Extra | undefined = Constants.expoConfig?.extra as Extra | undefined): LicensingConfig {
  const l = extra?.licensing ?? {};
  const key = l.publicKey ? decodeBase64(l.publicKey) : null;
  return {
    flavor: l.flavor === 'naboopay' ? 'naboopay' : 'play',
    hubUrl: (l.hubUrl ?? '').replace(/\/+$/, ''),
    publicKey: key && key.length === 32 ? key : null,
  };
}

export const config = licensingConfig();
