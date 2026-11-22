// App push settings for a build (issue #49), read from the environment by
// app.config.js:
//
//   KERGUI_EAS_PROJECT_ID        the EAS project's id (a UUID). Expo push
//                                tokens are issued for it; without it the app
//                                registers no token and the hub's alerts
//                                reach the app only while it runs (the
//                                Android watch, the background check).
//   KERGUI_GOOGLE_SERVICES_FILE  path to the Firebase project's
//                                google-services.json, which Android needs
//                                for FCM. Kept out of the repository.
//
// Both empty is a valid build: push is off, as on the hub without an Expo
// configuration.

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function readPushConfig(env) {
  const projectId = (env.KERGUI_EAS_PROJECT_ID || '').trim();
  if (projectId && !UUID.test(projectId)) throw new Error(`KERGUI_EAS_PROJECT_ID must be the EAS project's UUID, not "${projectId}"`);
  const googleServicesFile = (env.KERGUI_GOOGLE_SERVICES_FILE || '').trim();
  return { projectId, googleServicesFile };
}

function applyPush(config, env) {
  const { projectId, googleServicesFile } = readPushConfig(env);
  const out = { ...config };
  if (projectId) out.extra = { ...config.extra, eas: { ...config.extra?.eas, projectId } };
  if (googleServicesFile) out.android = { ...config.android, googleServicesFile };
  return out;
}

module.exports = { readPushConfig, applyPush };
