// Injects a release signingConfig into android/app/build.gradle at prebuild
// time. Values come from the environment (set by mobile-release.yml from the
// secrets created by scripts/mobile/setup-signing.sh), so nothing secret is in
// the repo and android/ can stay generated. Without KERGUI_UPLOAD_STORE_FILE
// the release build keeps Expo's default (debug-signed), which CI uses for
// unsigned test builds.
const { withAppBuildGradle } = require('expo/config-plugins');

const MARKER = '// kergui-release-signing';

const SIGNING_CONFIG = `
        release { ${MARKER}
            if (System.getenv("KERGUI_UPLOAD_STORE_FILE")) {
                storeFile file(System.getenv("KERGUI_UPLOAD_STORE_FILE"))
                storePassword System.getenv("KERGUI_UPLOAD_STORE_PASSWORD")
                keyAlias System.getenv("KERGUI_UPLOAD_KEY_ALIAS")
                keyPassword System.getenv("KERGUI_UPLOAD_KEY_PASSWORD")
            }
        }`;

function addSigning(gradle) {
  if (gradle.includes(MARKER)) return gradle;
  const withConfig = gradle.replace(/signingConfigs\s*\{/, (m) => m + SIGNING_CONFIG);
  if (withConfig === gradle) throw new Error('withReleaseSigning: no signingConfigs block in app/build.gradle');
  const release = /(buildTypes\s*\{[\s\S]*?release\s*\{[\s\S]*?)signingConfig\s+signingConfigs\.debug/;
  if (!release.test(withConfig)) throw new Error('withReleaseSigning: release buildType not found in app/build.gradle');
  return withConfig.replace(
    release,
    '$1signingConfig System.getenv("KERGUI_UPLOAD_STORE_FILE") ? signingConfigs.release : signingConfigs.debug',
  );
}

const withReleaseSigning = (config) =>
  withAppBuildGradle(config, (cfg) => {
    if (cfg.modResults.language !== 'groovy') throw new Error('withReleaseSigning: expected a Groovy build.gradle');
    cfg.modResults.contents = addSigning(cfg.modResults.contents);
    return cfg;
  });

module.exports = withReleaseSigning;
module.exports.addSigning = addSigning;
