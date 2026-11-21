// Release builds take their numbers from CI (mobile-release.yml):
//   KERGUI_VERSION       the tag without "mobile-v", e.g. 1.2.0 (shown in the stores)
//   KERGUI_BUILD_NUMBER  github.run_number (versionCode / CFBundleVersion, must grow)
// Licensing (flavor, hub URL, hub public key) comes from the environment too:
// see config/licensing.js. Everything else lives in app.json.
const { applyLicensing } = require('./config/licensing');

module.exports = ({ config }) => {
  const version = process.env.KERGUI_VERSION || config.version;
  const build = Number(process.env.KERGUI_BUILD_NUMBER || 1);
  const withLicensing = applyLicensing(config, process.env);
  return {
    ...withLicensing,
    version,
    android: { ...withLicensing.android, versionCode: build },
    ios: { ...config.ios, buildNumber: String(build) },
  };
};
