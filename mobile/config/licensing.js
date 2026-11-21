// Licensing settings for a build, read from the environment by app.config.js
// and handed to the app through `extra.licensing` (expo-constants):
//
//   KERGUI_HUB_URL               the licensing hub, https (http only for a
//                                local hub: localhost, 127.0.0.1, 10.0.2.2).
//                                Empty: no account features, Free works.
//   KERGUI_LICENSING_PUBLIC_KEY  the hub's Ed25519 public key, raw 32 bytes in
//                                standard base64, as GET /licensing/v1/public-key
//                                serves it. Empty: no token can verify.
//
// There is one build, the free Google Play app (RFC-006, decision of
// 2026-11-21): it carries no payment code. Passes are bought on the hub's web
// page; the app only reads the account's entitlement.

const LOCAL_HOSTS = ['localhost', '127.0.0.1', '10.0.2.2'];

function readHubUrl(env) {
  const raw = (env.KERGUI_HUB_URL || '').trim().replace(/\/+$/, '');
  if (!raw) return '';
  let u;
  try {
    u = new URL(raw);
  } catch {
    throw new Error(`KERGUI_HUB_URL must be an https URL, not "${raw}"`);
  }
  const local = u.protocol === 'http:' && LOCAL_HOSTS.includes(u.hostname);
  if (u.protocol !== 'https:' && !local) throw new Error(`KERGUI_HUB_URL must be an https URL, not "${raw}"`);
  return raw;
}

function readPublicKey(env) {
  const raw = (env.KERGUI_LICENSING_PUBLIC_KEY || '').trim();
  if (!raw) return '';
  const ok = /^[A-Za-z0-9+/]+={0,2}$/.test(raw) && raw.length % 4 === 0 && Buffer.from(raw, 'base64').length === 32;
  if (!ok) throw new Error('KERGUI_LICENSING_PUBLIC_KEY must be a raw Ed25519 public key (32 bytes) in standard base64');
  return raw;
}

function readLicensingConfig(env) {
  return { hubUrl: readHubUrl(env), publicKey: readPublicKey(env) };
}

// The app has no Play Billing. Blocking the permission keeps any library
// that might bring it in from adding it to the manifest.
const BILLING = 'com.android.vending.BILLING';

function applyLicensing(config, env) {
  const licensing = readLicensingConfig(env);
  const blocked = config.android?.blockedPermissions ?? [];
  return {
    ...config,
    android: {
      ...config.android,
      blockedPermissions: blocked.includes(BILLING) ? blocked : [...blocked, BILLING],
    },
    extra: { ...config.extra, licensing },
  };
}

module.exports = { readLicensingConfig, applyLicensing };
