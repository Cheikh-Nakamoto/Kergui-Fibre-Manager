import * as Crypto from 'expo-crypto';
import * as SecureStore from 'expo-secure-store';

// A light lock against an APK passed around without the owner's consent: the
// app asks for an activation key once, then remembers it. Only the key's
// SHA-256 ships in the bundle. This is not DRM: anyone with the key, or with
// the patience to patch the bundle, gets through.
const KEY_SHA256 = '0681c0241122e5269610eb79ef8345e16cf813d680942aa37cb6718b5e36ff36';
const ACTIVATED_KEY = 'kergui.activated';

export async function isValidKey(input: string): Promise<boolean> {
  const digest = await Crypto.digestStringAsync(Crypto.CryptoDigestAlgorithm.SHA256, input.trim());
  return digest.toLowerCase() === KEY_SHA256;
}

export async function isActivated(): Promise<boolean> {
  try {
    return (await SecureStore.getItemAsync(ACTIVATED_KEY)) === '1';
  } catch {
    return false;
  }
}

export async function activate(input: string): Promise<boolean> {
  if (!(await isValidKey(input))) return false;
  await SecureStore.setItemAsync(ACTIVATED_KEY, '1');
  return true;
}
