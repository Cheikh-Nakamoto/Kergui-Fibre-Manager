// Licensing settings for a build, read from the environment by app.config.js
// and handed to the app through `extra.licensing` (expo-constants):
//
//   KERGUI_FLAVOR                play (default) | naboopay
//                                - play: Google Play build, Play Billing
//                                  subscriptions; no NabooPay code in the bundle.
//                                - naboopay: direct APK, NabooPay passes.
//                                The default is `play` on purpose: a build that
//                                forgets the variable must never carry NabooPay
//                                into the Play Store (RFC-006, Two channels).
//   KERGUI_HUB_URL               the licensing hub, https (http only for a
//                                local hub: localhost, 127.0.0.1, 10.0.2.2).
//                                Empty: Pro is unavailable, Free works.
//   KERGUI_LICENSING_PUBLIC_KEY  the hub's Ed25519 public key, raw 32 bytes in
//                                standard base64, as GET /licensing/v1/public-key
//                                serves it. Empty: no token can verify.
//
// babel.config.js reads KERGUI_FLAVOR through the same function, so the bundle
// and extra always agree.

const FLAVORS = ['play', 'naboopay'];
const LOCAL_HOSTS = ['localhost', '127.0.0.1', '10.0.2.2'];

function readFlavor(env) {
  const raw = (env.KERGUI_FLAVOR || '').trim().toLowerCase();
  if (!raw) return 'play';
  if (!FLAVORS.includes(raw)) throw new Error(`KERGUI_FLAVOR must be one of ${FLAVORS.join(', ')}, not "${env.KERGUI_FLAVOR}"`);
  return raw;
}

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
  return { flavor: readFlavor(env), hubUrl: readHubUrl(env), publicKey: readPublicKey(env) };
}

// Play's billing permission comes with expo-iap's manifest. The NabooPay APK
// never opens Play Billing, so it does not ask for it. The application id
// stays the same in both flavors: one app, two ways to pay.
const BILLING = 'com.android.vending.BILLING';

function applyLicensing(config, env) {
  const licensing = readLicensingConfig(env);
  const blocked = config.android?.blockedPermissions ?? [];
  return {
    ...config,
    android: {
      ...config.android,
      blockedPermissions: licensing.flavor === 'naboopay' && !blocked.includes(BILLING) ? [...blocked, BILLING] : blocked,
    },
    extra: { ...config.extra, licensing },
  };
}

module.exports = { FLAVORS, readFlavor, readLicensingConfig, applyLicensing };
