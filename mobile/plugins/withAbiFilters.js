// Lets a build pick which CPU architectures it packages, through the Gradle
// property kergui.abiFilters (e.g. -Pkergui.abiFilters=armeabi-v7a,arm64-v8a).
// Without the property nothing changes: the Play bundle keeps every ABI and
// Google Play serves each phone its own. The APK of the website passes it, so
// it carries no x86 / x86_64 libraries, which only emulators use (69 MB of
// the 141 MB it used to weigh). Prebuilt libraries (React Native, the Go
// core's AAR) honour ndk.abiFilters, not reactNativeArchitectures alone.
const { withAppBuildGradle } = require('expo/config-plugins');

const MARKER = '// kergui-abi-filters';

const FILTERS = `
        ${MARKER}
        if (findProperty('kergui.abiFilters')) {
            ndk { abiFilters(*findProperty('kergui.abiFilters').split(',').collect { it.trim() }) }
        }`;

function addAbiFilters(gradle) {
  if (gradle.includes(MARKER)) return gradle;
  const out = gradle.replace(/defaultConfig\s*\{/, (m) => m + FILTERS);
  if (out === gradle) throw new Error('withAbiFilters: no defaultConfig block in app/build.gradle');
  return out;
}

const withAbiFilters = (config) =>
  withAppBuildGradle(config, (cfg) => {
    if (cfg.modResults.language !== 'groovy') throw new Error('withAbiFilters: expected a Groovy build.gradle');
    cfg.modResults.contents = addAbiFilters(cfg.modResults.contents);
    return cfg;
  });

module.exports = withAbiFilters;
module.exports.addAbiFilters = addAbiFilters;
