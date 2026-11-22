// Licensing settings for a build, read from the environment by app.config.js
// and handed to the app through `extra.licensing` (expo-constants):
//
//   KERGUI_HUB_URL               the licensing hub, https (http only for a
//                                local hub: localhost, 127.0.0.1, 10.0.2.2).
//                                Empty: no account features, Free works.
//   KERGUI_LICENSING_PUBLIC_KEY  the hub's Ed25519 public keys the build trusts,
//                                raw 32 bytes in standard base64, as
//                                GET /licensing/v1/public-keys serves them,
//                                separated by commas or spaces: the active key,
//                                and during a rotation the next one too
//                                (docs/licensing-key-rotation.md). Empty: no
//                                token can verify.
//
// There is one build, the free Google Play app (RFC-006, decision of
// 2026-11-21): it carries no payment code. Passes are bought on the hub's web
// page; the app only reads the account's entitlement.

const { createHash } = require('crypto');

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

// keyId names a public key as the hub does (internal/entitlement KeyID): the
// first 8 bytes of its SHA-256, in hex. It is worked out here, at build time,
// so the app needs no SHA-256 of its own.
function keyId(raw) {
  return createHash('sha256').update(raw).digest('hex').slice(0, 16);
}

function readPublicKeys(env) {
  const out = [];
  for (const k of (env.KERGUI_LICENSING_PUBLIC_KEY || '').split(/[\s,]+/).filter(Boolean)) {
    const ok = /^[A-Za-z0-9+/]+={0,2}$/.test(k) && k.length % 4 === 0 && Buffer.from(k, 'base64').length === 32;
    if (!ok) throw new Error('KERGUI_LICENSING_PUBLIC_KEY must list raw Ed25519 public keys (32 bytes) in standard base64');
    const id = keyId(Buffer.from(k, 'base64'));
    if (!out.some((p) => p.keyId === id)) out.push({ keyId: id, publicKey: k });
  }
  return out;
}

function readLicensingConfig(env) {
  return { hubUrl: readHubUrl(env), publicKeys: readPublicKeys(env) };
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
